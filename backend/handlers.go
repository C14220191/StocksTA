package main

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"golang.org/x/sync/singleflight"
)

const (
	analysisTimeframe = "1h"
	candleDuration    = time.Hour // harus sama dengan analysisTimeframe
	analysisCandles   = 14
	promptVersion     = "v0"
)

type App struct {
	store  *Store
	policy CachePolicy
	ai     AIClient
	flight singleflight.Group
}

type AnalyzeResponse struct {
	Symbol          string    `json:"symbol"`
	Condition       string    `json:"condition"`
	CacheStatus     string    `json:"cache_status"`
	Signal          string    `json:"signal"`
	Summary         string    `json:"summary"`
	LatencyMS       int64     `json:"latency_ms"`
	TokensIn        int       `json:"tokens_in"`
	TokensOut       int       `json:"tokens_out"`
	CacheReadTokens int       `json:"cache_read_tokens"`
	CostUSD         float64   `json:"cost_usd"`
	DataAgeSeconds  int       `json:"data_age_seconds"`
	SourceDataTS    time.Time `json:"source_data_ts"`
	Support         *float64  `json:"support"`    // dari AI
	Resistance      *float64  `json:"resistance"` // dari AI
}

// genOut: hasil satu kali pembuatan summary, dibagikan ke request yang menunggu.
type genOut struct {
	summary   CachedSummary
	tokensIn  int
	tokensOut int
	cacheRead int
	cost      float64
	ttl       *int
	fromCache bool
}

func (a *App) Analyze(c echo.Context) error {
	start := time.Now() // latency dihitung dari awal, termasuk baca database
	ctx := c.Request().Context()
	symbol := strings.ToUpper(c.Param("symbol"))

	instID, err := a.store.InstrumentID(ctx, symbol)
	if errors.Is(err, ErrNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "instrument tidak ditemukan"})
	}
	if err != nil {
		return a.fail(c, err)
	}

	candles, err := a.store.LatestCandles(ctx, instID, analysisTimeframe, analysisCandles)
	if err != nil {
		return a.fail(c, err)
	}
	if len(candles) < 3 {
		return c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "data OHLCV belum cukup, jalankan ingest.py"})
	}
	vol := realizedVolatility(candles)

	var (
		status    = "MISS"
		summary   CachedSummary
		tokensIn  int
		tokensOut int
		cacheRead int
		cost      float64
		ttl       *int
	)
	if a.policy.Name() == "no_cache" {
		status = "BYPASS" // cache memang tidak dipakai di kondisi ini
	}

	// generate: membuat summary baru lewat AI. Dipakai langsung (no_cache)
	// atau lewat singleflight (kondisi cache) supaya request bersamaan tidak memanggil AI berulang.
	generate := func() (*genOut, error) {
		gctx := context.WithoutCancel(ctx)                  // pembatalan satu client tidak menggagalkan yang menunggu
		if cached, hit := a.policy.Get(gctx, symbol); hit { // cek ulang: request lain mungkin baru selesai
			return &genOut{summary: *cached, fromCache: true}, nil
		}
		res, err := a.ai.Analyze(gctx, symbol, candles)
		if err != nil {
			return nil, err
		}
		last := candles[len(candles)-1]
		pv := res.PromptVersion
		if pv == "" {
			pv = promptVersion
		}
		id, genAt, err := a.store.InsertSummary(gctx, instID, last.TS,
			pv, res.Model, res.Content, res.Signal, last.Close, res.Support, res.Resistance)
		if err != nil {
			return nil, err
		}
		s := CachedSummary{
			SummaryID: id, Content: res.Content, Signal: res.Signal, Model: res.Model,
			SourceDataTS: last.TS, GeneratedAt: genAt,
			Support: res.Support, Resistance: res.Resistance,
		}
		return &genOut{
			summary: s, tokensIn: res.TokensIn, tokensOut: res.TokensOut,
			cacheRead: res.CacheReadTokens, cost: res.CostUSD,
			ttl: a.policy.Set(gctx, symbol, s, vol),
		}, nil
	}

	if cached, hit := a.policy.Get(ctx, symbol); hit {
		status = "HIT"
		summary = *cached
	} else {
		var out *genOut
		led := false // true hanya untuk request yang benar-benar memanggil AI
		if a.policy.Name() == "no_cache" {
			out, err = generate()
			led = true
		} else {
			v, ferr, _ := a.flight.Do(symbol, func() (interface{}, error) {
				led = true
				return generate()
			})
			err = ferr
			if err == nil {
				out = v.(*genOut)
			}
		}
		if err != nil {
			return a.fail(c, err)
		}
		summary = out.summary
		if led && !out.fromCache {
			tokensIn, tokensOut, cacheRead, cost = out.tokensIn, out.tokensOut, out.cacheRead, out.cost
			ttl = out.ttl
		} else {
			status = "HIT" // dilayani dari hasil yang dibuat request lain
		}
	}

	latency := time.Since(start)
	// Freshness: waktu sejak candle terakhir di balik summary itu tutup.
	// Catatan: untuk saham IDX di akhir pekan/libur, nilai ini besar karena bursa tutup, bukan karena cache.
	dataAge := int(time.Since(summary.SourceDataTS.Add(candleDuration)).Seconds())

	logEntry := InferenceLog{
		InstrumentID:    instID,
		Condition:       a.policy.Name(),
		CacheStatus:     status,
		LatencyMS:       int(latency.Milliseconds()),
		Model:           summary.Model,
		TokensIn:        tokensIn,
		TokensOut:       tokensOut,
		CacheReadTokens: cacheRead,
		CostUSD:         cost,
		DataAgeSeconds:  dataAge,
		TTLSeconds:      ttl,
		Volatility:      vol,
	}
	if status == "HIT" {
		logEntry.Model = "" // tidak ada pemanggilan model saat HIT
	}
	if err := a.store.LogInference(ctx, logEntry); err != nil {
		slog.Error("gagal menulis inference_log", "err", err) // jangan gagalkan request
	}

	return c.JSON(http.StatusOK, AnalyzeResponse{
		Symbol: symbol, Condition: a.policy.Name(), CacheStatus: status,
		Signal: summary.Signal, Summary: summary.Content,
		LatencyMS: latency.Milliseconds(), TokensIn: tokensIn, TokensOut: tokensOut,
		CacheReadTokens: cacheRead, CostUSD: cost, DataAgeSeconds: dataAge, SourceDataTS: summary.SourceDataTS,
		Support: summary.Support, Resistance: summary.Resistance,
	})
}

func (a *App) fail(c echo.Context, err error) error {
	slog.Error("request gagal", "err", err)
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
}

// realizedVolatility: standar deviasi log return antar candle (dipakai nanti untuk dynamic TTL).
func realizedVolatility(c []Candle) float64 {
	var rets []float64
	for i := 1; i < len(c); i++ {
		if c[i-1].Close > 0 && c[i].Close > 0 {
			rets = append(rets, math.Log(c[i].Close/c[i-1].Close))
		}
	}
	if len(rets) < 2 {
		return 0
	}
	var mean float64
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))
	var variance float64
	for _, r := range rets {
		variance += (r - mean) * (r - mean)
	}
	variance /= float64(len(rets) - 1)
	v := math.Sqrt(variance)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

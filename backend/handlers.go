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
	promptVersion     = "v0" // cadangan kalau PROMPT_VERSION tidak di-set
)

type App struct {
	store  *Store
	policy CachePolicy
	ai     AIClient
	prices *PriceFeed
	flight singleflight.Group
}

type AnalyzeResponse struct {
	Symbol            string    `json:"symbol"`
	Condition         string    `json:"condition"`
	CacheStatus       string    `json:"cache_status"`
	Signal            string    `json:"signal"`
	Summary           string    `json:"summary"`
	LatencyMS         int64     `json:"latency_ms"`
	TokensIn          int       `json:"tokens_in"`
	TokensOut         int       `json:"tokens_out"`
	CacheReadTokens   int       `json:"cache_read_tokens"`
	CostUSD           float64   `json:"cost_usd"`
	DataAgeSeconds    int       `json:"data_age_seconds"`
	CacheAgeSeconds   int       `json:"cache_age_seconds"`
	SourceDataTS      time.Time `json:"source_data_ts"`
	GeneratedAt       time.Time `json:"generated_at"`
	PriceAtGeneration float64   `json:"price_at_generation"`
	PriceAtRequest    float64   `json:"price_at_request"`
	PriceDeviationPct *float64  `json:"price_deviation_pct"`
	Support           *float64  `json:"support"`    // dari AI
	Resistance        *float64  `json:"resistance"` // dari AI
}

// genOut: hasil satu kali pembuatan summary, dibagikan ke request yang menunggu.
type genOut struct {
	summary   CachedSummary
	res       *AIResult // nil kalau fromCache
	ttl       *int
	fromCache bool
}

var validTriggers = map[string]bool{"ui": true, "k6": true, "scheduled": true, "manual": true}

func (a *App) Analyze(c echo.Context) error {
	start := time.Now() // latency dihitung dari awal, termasuk baca database
	ctx := c.Request().Context()
	symbol := strings.ToUpper(c.Param("symbol"))

	// Sumber request (header X-TA-Trigger), supaya data k6 / pre-warm tidak bercampur dengan UI.
	trigger := strings.ToLower(c.Request().Header.Get("X-TA-Trigger"))
	if trigger == "" {
		trigger = "ui"
	}
	if !validTriggers[trigger] {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "X-TA-Trigger harus ui, k6, scheduled, atau manual"})
	}

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

	quote, err := a.CurrentQuote(ctx, symbol, instID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return a.fail(c, err)
	}

	logEntry := InferenceLog{
		InstrumentID:  instID,
		Condition:     a.policy.Name(),
		Volatility:    vol,
		Trigger:       trigger,
		PromptVersion: env("PROMPT_VERSION", promptVersion),
		Threshold:     a.policy.Threshold(),
		PriceSource:   quote.Source,
	}
	if quote.Price > 0 {
		logEntry.PriceAtRequest = &quote.Price
	}

	// generate: membuat summary baru lewat AI. Dipakai langsung (no_cache)
	// atau lewat singleflight (kondisi cache) supaya request bersamaan tidak memanggil AI berulang.
	generate := func() (*genOut, error) {
		gctx := context.WithoutCancel(ctx)                                         // pembatalan satu client tidak menggagalkan yang menunggu
		if cached, inv := a.policy.Get(gctx, symbol, quote.Price); cached != nil { // cek ulang: request lain mungkin baru selesai
			return &genOut{summary: *cached, fromCache: true}, nil
		} else if inv != nil {
			a.logCacheEvent(gctx, instID, inv)
		}
		res, err := a.ai.Analyze(gctx, symbol, candles, quote)
		if err != nil {
			return nil, err
		}
		last := candles[len(candles)-1]
		pv := res.PromptVersion
		if pv == "" {
			pv = logEntry.PromptVersion
		}
		priceAtGen := quote.Price // harga yang dilihat AI; cadangan close candle terakhir
		if priceAtGen <= 0 {
			priceAtGen = last.Close
		}
		id, genAt, err := a.store.InsertSummary(gctx, instID, last.TS,
			pv, res.Model, res.Content, res.Signal, priceAtGen, res.Support, res.Resistance)
		if err != nil {
			return nil, err
		}
		s := CachedSummary{
			SummaryID: id, Content: res.Content, Signal: res.Signal, Model: res.Model,
			PromptVersion: pv, SourceDataTS: last.TS, GeneratedAt: genAt,
			PriceAtGeneration: priceAtGen, Support: res.Support, Resistance: res.Resistance,
		}
		return &genOut{summary: s, res: res, ttl: a.policy.Set(gctx, symbol, s, vol)}, nil
	}

	var (
		status  = "MISS"
		summary CachedSummary
		res     *AIResult // terisi hanya kalau request ini yang memanggil AI
	)
	if a.policy.Name() == "no_cache" {
		status = "BYPASS" // cache memang tidak dipakai di kondisi ini
	}

	if cached, inv := a.policy.Get(ctx, symbol, quote.Price); cached != nil {
		status = "HIT"
		summary = *cached
	} else {
		if inv != nil {
			a.logCacheEvent(ctx, instID, inv)
		}
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
			logEntry.CacheStatus = "ERROR"
			logEntry.LatencyMS = int(time.Since(start).Milliseconds())
			logEntry.Error = truncate(err.Error(), 500)
			a.logInference(ctx, logEntry)
			return a.fail(c, err)
		}
		summary = out.summary
		if led && !out.fromCache {
			res = out.res
			logEntry.TTLSeconds = out.ttl
		} else {
			status = "HIT" // dilayani dari hasil yang dibuat request lain
		}
	}

	latency := time.Since(start)
	// Freshness berbasis waktu: sejak candle terakhir di balik summary itu tutup.
	// = umur data saat summary dibuat + cache_age (lama summary tinggal di cache).
	// Catatan: untuk saham IDX di akhir pekan/libur, nilai ini besar karena bursa tutup, bukan karena cache.
	dataAge := int(time.Since(summary.SourceDataTS.Add(candleDuration)).Seconds())
	cacheAge := 0
	if status == "HIT" {
		cacheAge = max(int(time.Since(summary.GeneratedAt).Seconds()), 0)
	}
	// Freshness berbasis harga: seberapa jauh harga sudah bergerak sejak summary dibuat.
	var devPtr *float64
	if dev, ok := priceDeviationPct(summary.PriceAtGeneration, quote.Price); ok {
		devPtr = &dev
	}

	logEntry.CacheStatus = status
	logEntry.LatencyMS = int(latency.Milliseconds())
	logEntry.DataAgeSeconds = &dataAge
	logEntry.CacheAgeSeconds = &cacheAge
	logEntry.SummaryID = &summary.SummaryID
	logEntry.PriceDeviationPct = devPtr
	if summary.PriceAtGeneration > 0 {
		logEntry.PriceAtGeneration = &summary.PriceAtGeneration
	}
	if summary.PromptVersion != "" {
		logEntry.PromptVersion = summary.PromptVersion
	}
	if res != nil { // token, biaya, dan model hanya untuk request yang memanggil AI
		logEntry.Model = res.Model
		logEntry.TokensIn, logEntry.TokensOut = res.TokensIn, res.TokensOut
		logEntry.CacheReadTokens, logEntry.CostUSD = res.CacheReadTokens, res.CostUSD
		logEntry.AIQueueMS, logEntry.AIExecMS, logEntry.AIFormatOK = res.QueueMS, res.ExecMS, res.FormatOK
	}
	a.logInference(ctx, logEntry)

	resp := AnalyzeResponse{
		Symbol: symbol, Condition: a.policy.Name(), CacheStatus: status,
		Signal: summary.Signal, Summary: summary.Content,
		LatencyMS: latency.Milliseconds(), DataAgeSeconds: dataAge, CacheAgeSeconds: cacheAge,
		SourceDataTS: summary.SourceDataTS, GeneratedAt: summary.GeneratedAt,
		PriceAtGeneration: summary.PriceAtGeneration, PriceAtRequest: quote.Price, PriceDeviationPct: devPtr,
		Support: summary.Support, Resistance: summary.Resistance,
	}
	if res != nil {
		resp.TokensIn, resp.TokensOut = res.TokensIn, res.TokensOut
		resp.CacheReadTokens, resp.CostUSD = res.CacheReadTokens, res.CostUSD
	}
	return c.JSON(http.StatusOK, resp)
}

// logInference: kegagalan menulis log tidak menggagalkan request.
func (a *App) logInference(ctx context.Context, l InferenceLog) {
	if err := a.store.LogInference(context.WithoutCancel(ctx), l); err != nil {
		slog.Error("gagal menulis inference_log", "err", err)
	}
}

func (a *App) logCacheEvent(ctx context.Context, instID int, inv *Invalidation) {
	if err := a.store.LogCacheEvent(context.WithoutCancel(ctx), instID, inv.Reason, inv.ChangePct); err != nil {
		slog.Error("gagal menulis cache_events", "err", err)
	}
}

func (a *App) Health(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	out := map[string]any{"status": "ok", "cache_mode": a.policy.Name(), "prompt_version": env("PROMPT_VERSION", promptVersion)}
	if th := a.policy.Threshold(); th != nil {
		out["invalidation_threshold_pct"] = *th
	}
	code := http.StatusOK
	if err := a.store.pool.Ping(ctx); err != nil {
		out["status"], out["db"], code = "degraded", err.Error(), http.StatusServiceUnavailable
	}
	if err := a.policy.Ping(ctx); err != nil {
		out["status"], out["redis"], code = "degraded", err.Error(), http.StatusServiceUnavailable
	}
	if a.prices != nil {
		out["price_feed"] = a.prices.Status()
	}
	return c.JSON(code, out)
}

func (a *App) fail(c echo.Context, err error) error {
	slog.Error("request gagal", "err", err)
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// realizedVolatility: standar deviasi log return antar candle (dipakai dynamic TTL dan σ_ref).
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

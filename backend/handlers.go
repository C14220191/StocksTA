package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const (
	analysisTimeframe = "1h"
	candleDuration    = time.Hour // harus sama dengan analysisTimeframe
	analysisCandles   = 14        // candle yang dikirim ke prompt dan jendela σ
	promptVersion     = "v0"      // cadangan kalau PROMPT_VERSION tidak di-set
)

type App struct {
	store  *Store
	policy CachePolicy
	cfg    policyConfig
	ai     AIClient
	prices *PriceFeed
	rdb    *redis.Client // popularitas + API Health admin (ada juga saat no_cache); nil kalau Redis mati
	pregen *PregenConfig // nil kalau PREGEN=off
	flight singleflight.Group
	auth   *adminAuth    // login halaman admin
	ingest *ingestRunner // backfill instrumen yang ditambahkan lewat admin
}

type AnalyzeResponse struct {
	Symbol            string          `json:"symbol"`
	Method            string          `json:"method"` // debate | mtf | grounded
	Condition         string          `json:"condition"`
	CacheStatus       string          `json:"cache_status"`
	Origin            string          `json:"origin"` // ondemand | pregen
	Signal            string          `json:"signal"` // BUY | SELL | HOLD = bias bullish | bearish | neutral
	Confidence        *int            `json:"confidence"`
	Summary           string          `json:"summary"`
	Detail            json.RawMessage `json:"detail"` // isi terstruktur metode; null kalau output AI bukan JSON
	Model             string          `json:"model"`
	FormatOK          *bool           `json:"format_ok,omitempty"` // hanya saat AI dipanggil request ini
	LatencyMS         int64           `json:"latency_ms"`
	TokensIn          int             `json:"tokens_in"`
	TokensOut         int             `json:"tokens_out"`
	CacheReadTokens   int             `json:"cache_read_tokens"`
	CostUSD           float64         `json:"cost_usd"`
	DataAgeSeconds    int             `json:"data_age_seconds"`
	CacheAgeSeconds   int             `json:"cache_age_seconds"`
	SourceDataTS      time.Time       `json:"source_data_ts"`
	GeneratedAt       time.Time       `json:"generated_at"`
	PriceAtGeneration float64         `json:"price_at_generation"`
	PriceAtRequest    float64         `json:"price_at_request"`
	PriceDeviationPct *float64        `json:"price_deviation_pct"`
	StateAtGeneration string          `json:"state_at_generation"`
	StateAtRequest    string          `json:"state_at_request"`
	Support           *float64        `json:"support"`    // dari AI
	Resistance        *float64        `json:"resistance"` // dari AI
}

// genOut: hasil satu kali pembuatan summary, dibagikan ke request yang menunggu (singleflight).
type genOut struct {
	summary   CachedSummary
	res       *AIResult // nil kalau fromCache
	ttl       *int
	fromCache bool
}

// genInput: semua yang dibutuhkan untuk membuat satu summary.
type genInput struct {
	symbol  string
	method  string
	instID  int
	candles []Candle // candle 1h tertutup, lama -> baru (stateCandles)
	quote   Quote
	vol     float64
	state   string // S saat generate
	origin  string // ondemand | pregen
	force   bool   // pre-generation refresh-ahead: generate walau entri lama masih berlaku
}

var validTriggers = map[string]bool{"ui": true, "k6": true, "scheduled": true, "manual": true, "pilot": true}

// Input sanitization: simbol hanya huruf besar/angka dengan sufiks bursa opsional (BBCA.JK).
var symbolRe = regexp.MustCompile(`^[A-Z0-9]{2,15}(\.[A-Z]{1,4})?$`)

func validSymbol(s string) bool { return symbolRe.MatchString(s) }

// analysisContext: data pasar terkini untuk satu instrument (dipakai request dan pre-generation).
type analysisContext struct {
	instID  int
	candles []Candle
	quote   Quote
	vol     float64
	state   string
}

var errNotEnoughData = errors.New("data OHLCV belum cukup")

func (a *App) loadAnalysisContext(ctx context.Context, symbol string) (*analysisContext, error) {
	instID, err := a.store.InstrumentID(ctx, symbol)
	if err != nil {
		return nil, err
	}
	candles, err := a.store.LatestCandles(ctx, instID, analysisTimeframe, stateCandles)
	if err != nil {
		return nil, err
	}
	if len(candles) < 3 {
		return nil, errNotEnoughData
	}
	quote, err := a.CurrentQuote(ctx, symbol, instID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	price := quote.Price
	if price <= 0 {
		price = candles[len(candles)-1].Close
	}
	return &analysisContext{
		instID: instID, candles: candles, quote: quote,
		vol:   realizedVolatility(tail(candles, analysisCandles)),
		state: newIndicatorModel(candles).State(price),
	}, nil
}

func tail(c []Candle, n int) []Candle {
	if len(c) > n {
		return c[len(c)-n:]
	}
	return c
}

// Analyze: alur cache-first (Bab 3.3.3). Popularitas dicatat, cache diperiksa (TTL, drift harga,
// kondisi S), dan kalau MISS hanya satu panggilan Hermes yang dijalankan per simbol (singleflight).
func (a *App) Analyze(c echo.Context) error {
	start := time.Now() // latency dihitung dari awal, termasuk baca database
	ctx := c.Request().Context()
	symbol := strings.ToUpper(c.Param("symbol"))
	if !validSymbol(symbol) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "format simbol tidak valid"})
	}

	method := strings.ToLower(c.QueryParam("method"))
	if !validMethod(method) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "method harus salah satu dari " + methodsList})
	}

	// Sumber request (header X-TA-Trigger), supaya data k6 / pilot tidak bercampur dengan UI.
	trigger := strings.ToLower(c.Request().Header.Get("X-TA-Trigger"))
	if trigger == "" {
		trigger = "ui"
	}
	if !validTriggers[trigger] || trigger == "scheduled" { // scheduled khusus pre-generation internal
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "X-TA-Trigger harus ui, k6, manual, atau pilot"})
	}

	ac, err := a.loadAnalysisContext(ctx, symbol)
	switch {
	case errors.Is(err, ErrNotFound):
		return c.JSON(http.StatusNotFound, map[string]string{"error": "instrument tidak ditemukan"})
	case errors.Is(err, errNotEnoughData):
		return c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "data OHLCV belum cukup, jalankan ingest.py"})
	case err != nil:
		return a.fail(c, err)
	}
	if trigger == "ui" || trigger == "k6" {
		a.recordAccess(ctx, symbol) // popularitas untuk pre-generation (Lapis 1)
	}

	logEntry := a.baseLog(ac, trigger, method)
	var (
		status  = "MISS"
		summary CachedSummary
		res     *AIResult // terisi hanya kalau request ini yang memanggil AI
	)
	if a.policy.Name() == "no_cache" {
		status = "BYPASS" // cache memang tidak dipakai di kondisi ini
	}

	if cached, inv := a.policy.Get(ctx, symbol, method, ac.quote.Price, ac.state); cached != nil {
		status = "HIT"
		summary = *cached
	} else {
		if inv != nil {
			a.logInvalidationFor(ctx, ac.instID, inv, "request")
		}
		out, led, err := a.produce(ctx, genInput{
			symbol: symbol, method: method, instID: ac.instID, candles: ac.candles, quote: ac.quote,
			vol: ac.vol, state: ac.state, origin: "ondemand",
		})
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
			status = "HIT" // dilayani dari hasil yang dibuat request lain (menunggu singleflight)
		}
	}

	resp := a.finishLog(&logEntry, status, time.Since(start), summary, ac, res)
	a.logInference(ctx, logEntry)
	resp.Symbol = symbol
	return c.JSON(http.StatusOK, resp)
}

// baseLog: kolom inference_log yang sama untuk semua status.
func (a *App) baseLog(ac *analysisContext, trigger, method string) InferenceLog {
	l := InferenceLog{
		Method:         method,
		InstrumentID:   ac.instID,
		Condition:      a.policy.Name(),
		Volatility:     ac.vol,
		Trigger:        trigger,
		PromptVersion:  env("PROMPT_VERSION", promptVersion),
		Threshold:      a.policy.Threshold(),
		PriceSource:    ac.quote.Source,
		Pregen:         a.cfg.pregen,
		StateAtRequest: ac.state,
	}
	if ac.quote.Price > 0 {
		p := ac.quote.Price
		l.PriceAtRequest = &p
	}
	return l
}

// finishLog melengkapi metrik freshness dan biaya, lalu menyusun respons.
func (a *App) finishLog(l *InferenceLog, status string, latency time.Duration, s CachedSummary,
	ac *analysisContext, res *AIResult) AnalyzeResponse {
	// Freshness berbasis waktu: sejak candle terakhir di balik summary itu tutup
	// = umur data saat summary dibuat + cache_age (lama summary tinggal di cache).
	// Catatan: untuk saham IDX di akhir pekan/libur, nilai ini besar karena bursa tutup, bukan karena cache.
	dataAge := int(time.Since(s.SourceDataTS.Add(candleDuration)).Seconds())
	cacheAge := 0
	if status == "HIT" {
		cacheAge = max(int(time.Since(s.GeneratedAt).Seconds()), 0)
	}
	// Freshness berbasis harga: D_serve = drift sejak summary dibuat.
	var devPtr *float64
	if dev, ok := priceDeviationPct(s.PriceAtGeneration, ac.quote.Price); ok {
		devPtr = &dev
	}
	l.CacheStatus = status
	l.LatencyMS = int(latency.Milliseconds())
	l.DataAgeSeconds = &dataAge
	l.CacheAgeSeconds = &cacheAge
	l.SummaryID = &s.SummaryID
	l.PriceDeviationPct = devPtr
	l.StateAtGeneration = s.State
	if s.State != "" && ac.state != "" {
		changed := s.State != ac.state
		l.StateChanged = &changed
	}
	if s.PriceAtGeneration > 0 {
		p := s.PriceAtGeneration
		l.PriceAtGeneration = &p
	}
	if s.PromptVersion != "" {
		l.PromptVersion = s.PromptVersion
	}
	resp := AnalyzeResponse{
		Method: s.Method, Condition: a.policy.Name(), CacheStatus: status, Origin: s.Origin,
		Signal: s.Signal, Confidence: s.Confidence, Summary: s.Content, Detail: s.Detail, Model: s.Model,
		LatencyMS: latency.Milliseconds(), DataAgeSeconds: dataAge, CacheAgeSeconds: cacheAge,
		SourceDataTS: s.SourceDataTS, GeneratedAt: s.GeneratedAt,
		PriceAtGeneration: s.PriceAtGeneration, PriceAtRequest: ac.quote.Price, PriceDeviationPct: devPtr,
		StateAtGeneration: s.State, StateAtRequest: ac.state,
		Support: s.Support, Resistance: s.Resistance,
	}
	if res != nil { // token, biaya, dan model hanya untuk request yang memanggil AI
		l.Model = res.Model
		l.TokensIn, l.TokensOut = res.TokensIn, res.TokensOut
		l.CacheReadTokens, l.CostUSD = res.CacheReadTokens, res.CostUSD
		l.AIQueueMS, l.AIExecMS, l.AIFormatOK = res.QueueMS, res.ExecMS, res.FormatOK
		resp.TokensIn, resp.TokensOut = res.TokensIn, res.TokensOut
		resp.CacheReadTokens, resp.CostUSD = res.CacheReadTokens, res.CostUSD
		resp.FormatOK = res.FormatOK
	}
	return resp
}

// produce: generate lewat singleflight (kondisi cache) supaya request bersamaan untuk simbol dan
// metode yang sama hanya memanggil Hermes sekali (mencegah cache stampede). led = request ini yang
// memanggil AI.
func (a *App) produce(ctx context.Context, in genInput) (out *genOut, led bool, err error) {
	if a.policy.Name() == "no_cache" {
		out, err = a.generate(ctx, in)
		return out, true, err
	}
	v, err, _ := a.flight.Do(in.symbol+"|"+in.method, func() (interface{}, error) {
		led = true
		return a.generate(ctx, in)
	})
	if err != nil {
		return nil, led, err
	}
	return v.(*genOut), led, nil
}

// generate membuat summary baru lewat AI dan menyimpannya ke database + cache.
func (a *App) generate(ctx context.Context, in genInput) (*genOut, error) {
	gctx := context.WithoutCancel(ctx) // pembatalan satu client tidak menggagalkan yang menunggu
	if !in.force {
		// cek ulang: request lain mungkin baru selesai membuat summary
		if cached, inv := a.policy.Get(gctx, in.symbol, in.method, in.quote.Price, in.state); cached != nil {
			return &genOut{summary: *cached, fromCache: true}, nil
		} else if inv != nil {
			a.logInvalidationFor(gctx, in.instID, inv, "request")
		}
	}
	snap := indicatorSnapshot(in.candles)
	mc, err := a.marketContext(gctx, in.instID, in.vol)
	if err != nil {
		return nil, err
	}
	res, err := a.ai.Analyze(gctx, AIInput{
		Symbol: in.symbol, Method: in.method, Candles: tail(in.candles, analysisCandles),
		Quote: in.quote, Indicators: &snap, Context: mc,
	})
	if err != nil {
		return nil, err
	}
	last := in.candles[len(in.candles)-1]
	pv := res.PromptVersion
	if pv == "" {
		pv = env("PROMPT_VERSION", promptVersion)
	}
	priceAtGen := in.quote.Price // harga yang dilihat AI; cadangan close candle terakhir
	if priceAtGen <= 0 {
		priceAtGen = last.Close
	}
	id, genAt, err := a.store.InsertSummary(gctx, NewSummary{
		InstrumentID: in.instID, SourceTS: last.TS, PromptVersion: pv, Model: res.Model,
		Content: res.Content, Signal: res.Signal, PriceAtGen: priceAtGen,
		Support: res.Support, Resistance: res.Resistance,
		Origin: in.origin, State: in.state, Indicators: &snap,
		Method: in.method, Confidence: res.Confidence, Detail: res.Detail,
	})
	if err != nil {
		return nil, err
	}
	s := CachedSummary{
		SummaryID: id, InstrumentID: in.instID, Content: res.Content, Signal: res.Signal, Model: res.Model,
		PromptVersion: pv, Origin: in.origin, SourceDataTS: last.TS, GeneratedAt: genAt,
		PriceAtGeneration: priceAtGen, State: in.state, Support: res.Support, Resistance: res.Resistance,
		TokensIn: res.TokensIn, TokensOut: res.TokensOut, CostUSD: res.CostUSD,
		Method: in.method, Confidence: res.Confidence, Detail: res.Detail,
	}
	out := &genOut{summary: s, res: res}
	if res.valid() {
		out.ttl = a.policy.Set(gctx, in.symbol, in.method, s, in.vol)
	} else {
		// Tetap tersimpan di ai_summaries dan inference_log (ai_format_ok = false) untuk diukur,
		// tetapi tidak di-cache: request berikutnya mencoba generate lagi.
		slog.Warn("output AI tidak valid, tidak di-cache", "symbol", in.symbol, "method", in.method, "summary_id", id)
	}
	return out, nil
}

// logInference: kegagalan menulis log tidak menggagalkan request.
func (a *App) logInference(ctx context.Context, l InferenceLog) {
	if err := a.store.LogInference(context.WithoutCancel(ctx), l); err != nil {
		slog.Error("gagal menulis inference_log", "err", err)
	}
}

func (a *App) logInvalidationFor(ctx context.Context, instID int, inv *Invalidation, detectedBy string) {
	if inv.InstrumentID == 0 {
		inv.InstrumentID = instID // entri lama di Redis belum membawa InstrumentID
	}
	a.logInvalidation(ctx, inv, detectedBy)
}

// logInvalidation mencatat satu invalidation ke cache_events beserta kondisi eksperimen instance ini.
func (a *App) logInvalidation(ctx context.Context, inv *Invalidation, detectedBy string) {
	e := CacheEvent{
		InstrumentID: inv.InstrumentID, Reason: inv.Reason, Condition: a.policy.Name(),
		Threshold: a.policy.Threshold(), Pregen: a.cfg.pregen, DetectedBy: detectedBy,
		StateFrom: inv.StateFrom, StateTo: inv.StateTo, Method: inv.Method,
	}
	if inv.Reason != "manual" || inv.ChangePct != 0 {
		chg := inv.ChangePct
		e.ChangePct = &chg
	}
	if inv.SummaryID > 0 {
		id := inv.SummaryID
		e.SummaryID = &id
	}
	if inv.CrossedAt != nil {
		e.CrossedAt = inv.CrossedAt
		lat := float64(time.Since(*inv.CrossedAt).Microseconds()) / 1000
		e.LatencyMS = &lat
	}
	if err := a.store.LogCacheEvent(context.WithoutCancel(ctx), e); err != nil {
		slog.Error("gagal menulis cache_events", "err", err)
	}
}

func (a *App) Health(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()
	out := map[string]any{"status": "ok", "cache_mode": a.policy.Name(), "instance": a.cfg.instanceKey(),
		"prompt_version": env("PROMPT_VERSION", promptVersion), "pregen": a.cfg.pregen}
	if th := a.policy.Threshold(); th != nil {
		out["invalidation_threshold_pct"] = *th
		out["invalidate_on_state"] = a.cfg.checkState
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

// realizedVolatility: standar deviasi sampel log return antar candle (dipakai dynamic TTL dan σ_ref).
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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"
)

// Endpoint halaman admin (/admin di UI). Isinya membaca data pengukuran, ditambah satu aksi
// (invalidate manual). Tidak ada autentikasi: backend hanya listen di 127.0.0.1, jadi admin
// diakses dari mesin yang sama atau lewat SSH tunnel.

// Zona waktu tampilan admin (pengelompokan per jam/hari dan "hari ini").
const adminTZ = "Asia/Jakarta"

type period struct {
	interval string // untuk now() - interval
	bucket   string // date_trunc: hour | day
	buckets  int
}

var periods = map[string]period{
	"24h": {"24 hours", "hour", 24},
	"7d":  {"7 days", "day", 7},
	"30d": {"30 days", "day", 30},
}

// adminFilter membaca ?period= dan ?trigger= (all | ui | k6 | scheduled | manual).
func adminFilter(c echo.Context) (string, period, string, error) {
	pk := c.QueryParam("period")
	if pk == "" {
		pk = "24h"
	}
	p, ok := periods[pk]
	if !ok {
		return "", p, "", errors.New("period harus 24h, 7d, atau 30d")
	}
	trig := strings.ToLower(c.QueryParam("trigger"))
	if trig == "" {
		trig = "all"
	}
	if trig != "all" && !validTriggers[trig] {
		return "", p, "", errors.New("trigger harus all, ui, k6, scheduled, atau manual")
	}
	return pk, p, trig, nil
}

func badRequest(c echo.Context, err error) error {
	return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
}

// aiBaseURL: AI_URL tanpa /analyze, mis. http://127.0.0.1:8000
func aiBaseURL() string {
	return strings.TrimSuffix(strings.TrimRight(env("AI_URL", "http://127.0.0.1:8000/analyze"), "/"), "/analyze")
}

// aiHealth memanggil /health AI service. Dipakai Cache & Job (antrean) dan API Health.
func aiHealth(ctx context.Context) (map[string]any, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, aiBaseURL()+"/health", nil)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	lat := time.Since(start)
	if err != nil {
		return nil, lat, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, lat, err
	}
	return out, lat, nil
}

func f64(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// ---- Dashboard ----

type overviewKPI struct {
	Requests        int      `json:"requests"`
	Hits            int      `json:"hits"`
	AICalls         int      `json:"ai_calls"` // MISS + BYPASS
	Errors          int      `json:"errors"`
	HitRatio        *float64 `json:"hit_ratio"` // HIT / (HIT + MISS), tanpa BYPASS
	Tokens          int64    `json:"tokens"`
	CostUSD         float64  `json:"cost_usd"`
	TokensPerCall   *float64 `json:"tokens_per_call"`
	CostPerCall     *float64 `json:"cost_per_call"`
	TokensSaved     float64  `json:"tokens_saved"` // estimasi: HIT x rata-rata token per panggilan AI
	CostSaved       float64  `json:"cost_saved"`
	LatencyAvg      *float64 `json:"latency_avg_ms"`
	LatencyP50      *float64 `json:"latency_p50_ms"`
	LatencyP95      *float64 `json:"latency_p95_ms"`
	LatencyHitAvg   *float64 `json:"latency_hit_avg_ms"`
	LatencyAIAvg    *float64 `json:"latency_ai_avg_ms"`
	Visits          int      `json:"visits"`       // request dari UI (1 kunjungan Detail = 1 analyze)
	VisitsToday     int      `json:"visits_today"` // sejak 00:00 WIB
	PerCallFallback bool     `json:"per_call_from_all_time"`
}

type seriesPoint struct {
	T       string `json:"t"` // waktu lokal awal bucket, "2006-01-02T15:04"
	Hits    int    `json:"hits"`
	AICalls int    `json:"ai_calls"`
	Errors  int    `json:"errors"`
}

type topInstrument struct {
	Symbol     string   `json:"symbol"`
	Requests   int      `json:"requests"`
	Hits       int      `json:"hits"`
	LatencyAvg *float64 `json:"latency_avg_ms"`
	Signal     *string  `json:"signal"`
}

// GET /api/admin/overview?period=24h|7d|30d&trigger=all|ui|k6|scheduled|manual
func (a *App) AdminOverview(c echo.Context) error {
	ctx := c.Request().Context()
	pk, p, trig, err := adminFilter(c)
	if err != nil {
		return badRequest(c, err)
	}

	var k overviewKPI
	err = a.store.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE cache_status <> 'PREGEN'),
		       count(*) FILTER (WHERE cache_status = 'HIT'),
		       count(*) FILTER (WHERE cache_status IN ('MISS', 'BYPASS')),
		       count(*) FILTER (WHERE cache_status = 'ERROR'),
		       coalesce(sum(tokens_in + tokens_out), 0),
		       coalesce(sum(cost_usd), 0)::float8,
		       avg(tokens_in + tokens_out) FILTER (WHERE cache_status IN ('MISS', 'BYPASS'))::float8,
		       avg(cost_usd) FILTER (WHERE cache_status IN ('MISS', 'BYPASS'))::float8,
		       avg(latency_ms) FILTER (WHERE cache_status <> 'ERROR')::float8,
		       percentile_cont(0.5) WITHIN GROUP (ORDER BY latency_ms) FILTER (WHERE cache_status <> 'ERROR'),
		       percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms) FILTER (WHERE cache_status <> 'ERROR'),
		       avg(latency_ms) FILTER (WHERE cache_status = 'HIT')::float8,
		       avg(latency_ms) FILTER (WHERE cache_status IN ('MISS', 'BYPASS'))::float8,
		       count(*) FILTER (WHERE trigger = 'ui')
		FROM inference_log
		WHERE created_at >= now() - $1::interval AND ($2 = 'all' OR trigger = $2)`,
		p.interval, trig).Scan(&k.Requests, &k.Hits, &k.AICalls, &k.Errors, &k.Tokens, &k.CostUSD,
		&k.TokensPerCall, &k.CostPerCall, &k.LatencyAvg, &k.LatencyP50, &k.LatencyP95,
		&k.LatencyHitAvg, &k.LatencyAIAvg, &k.Visits)
	if err != nil {
		return a.fail(c, err)
	}
	// Tanpa panggilan AI di periode ini (mis. semua HIT), pakai rata-rata sepanjang waktu untuk estimasi.
	if k.TokensPerCall == nil {
		k.PerCallFallback = true
		if err := a.store.pool.QueryRow(ctx, `
			SELECT avg(tokens_in + tokens_out)::float8, avg(cost_usd)::float8
			FROM inference_log WHERE cache_status IN ('MISS', 'BYPASS') AND tokens_in > 0`).
			Scan(&k.TokensPerCall, &k.CostPerCall); err != nil {
			return a.fail(c, err)
		}
	}
	k.TokensSaved = float64(k.Hits) * f64(k.TokensPerCall)
	k.CostSaved = float64(k.Hits) * f64(k.CostPerCall)
	if miss := k.AICalls; k.Hits+miss > 0 {
		var bypass int
		_ = a.store.pool.QueryRow(ctx, `
			SELECT count(*) FROM inference_log
			WHERE cache_status = 'BYPASS' AND created_at >= now() - $1::interval AND ($2 = 'all' OR trigger = $2)`,
			p.interval, trig).Scan(&bypass)
		if den := k.Hits + miss - bypass; den > 0 {
			r := float64(k.Hits) / float64(den)
			k.HitRatio = &r
		}
	}
	if err := a.store.pool.QueryRow(ctx, `
		SELECT count(*) FROM inference_log
		WHERE trigger = 'ui'
		  AND created_at >= date_trunc('day', now() AT TIME ZONE $1) AT TIME ZONE $1`, adminTZ).
		Scan(&k.VisitsToday); err != nil {
		return a.fail(c, err)
	}

	rows, err := a.store.pool.Query(ctx, `
		WITH b AS (
		    SELECT generate_series(
		        date_trunc($1, now() AT TIME ZONE $4) - ($3 - 1) * ('1 ' || $1)::interval,
		        date_trunc($1, now() AT TIME ZONE $4),
		        ('1 ' || $1)::interval) AS t
		)
		SELECT b.t,
		       count(l.id) FILTER (WHERE l.cache_status = 'HIT'),
		       count(l.id) FILTER (WHERE l.cache_status IN ('MISS', 'BYPASS')),
		       count(l.id) FILTER (WHERE l.cache_status = 'ERROR')
		FROM b
		LEFT JOIN inference_log l
		       ON date_trunc($1, l.created_at AT TIME ZONE $4) = b.t
		      AND ($2 = 'all' OR l.trigger = $2)
		GROUP BY b.t ORDER BY b.t`, p.bucket, trig, p.buckets, adminTZ)
	if err != nil {
		return a.fail(c, err)
	}
	series := []seriesPoint{}
	for rows.Next() {
		var t time.Time
		var sp seriesPoint
		if err := rows.Scan(&t, &sp.Hits, &sp.AICalls, &sp.Errors); err != nil {
			rows.Close()
			return a.fail(c, err)
		}
		sp.T = t.Format("2006-01-02T15:04")
		series = append(series, sp)
	}
	rows.Close()

	rows, err = a.store.pool.Query(ctx, `
		SELECT i.symbol, count(*),
		       count(*) FILTER (WHERE l.cache_status = 'HIT'),
		       avg(l.latency_ms) FILTER (WHERE l.cache_status <> 'ERROR')::float8,
		       (SELECT s.signal FROM ai_summaries s WHERE s.instrument_id = i.id ORDER BY s.generated_at DESC LIMIT 1)
		FROM inference_log l JOIN instruments i ON i.id = l.instrument_id
		WHERE l.created_at >= now() - $1::interval AND ($2 = 'all' OR l.trigger = $2)
		GROUP BY i.id, i.symbol ORDER BY count(*) DESC, i.symbol LIMIT 10`, p.interval, trig)
	if err != nil {
		return a.fail(c, err)
	}
	top, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (topInstrument, error) {
		var t topInstrument
		err := r.Scan(&t.Symbol, &t.Requests, &t.Hits, &t.LatencyAvg, &t.Signal)
		return t, err
	})
	if err != nil {
		return a.fail(c, err)
	}

	return c.JSON(http.StatusOK, map[string]any{
		"period": pk, "trigger": trig, "bucket": p.bucket, "timezone": adminTZ,
		"cache_mode": a.policy.Name(), "kpi": k, "series": series, "top_instruments": top,
	})
}

// ---- Cache & Job ----

type cacheRow struct {
	Symbol            string   `json:"symbol"`
	Method            string   `json:"method"`
	AssetType         string   `json:"asset_type"`
	Status            string   `json:"status"` // cached | expiring | empty | disabled
	TTLSeconds        *int     `json:"ttl_seconds"`
	GeneratedAt       *string  `json:"generated_at"`
	Signal            string   `json:"signal,omitempty"`
	PriceAtGeneration *float64 `json:"price_at_generation"`
	PriceNow          *float64 `json:"price_now"`
	PriceSource       string   `json:"price_source,omitempty"`
	DeviationPct      *float64 `json:"deviation_pct"`
	LastTTLApplied    *int     `json:"last_ttl_applied"`
	Origin            string   `json:"origin,omitempty"`    // ondemand | pregen
	StateGen          string   `json:"state_gen,omitempty"` // S saat generate
	StateNow          string   `json:"state_now,omitempty"` // S dengan harga sekarang
	Access            float64  `json:"access"`              // popularitas (akses dalam window pre-generation)
}

type jobStatus struct {
	Job        string  `json:"job"`
	LastRun    *string `json:"last_run"`
	LastStatus *string `json:"last_status"`
	Runs24h    int     `json:"runs_24h"`
	Fails24h   int     `json:"fails_24h"`
	Expected   string  `json:"expected"` // jadwal yang diharapkan
	Stale      bool    `json:"stale"`
}

// instanceFilter: baris milik instance ini saja ($1 kondisi, $2 θ, $3 pregen), karena beberapa
// instance kondisi bisa berjalan paralel dengan database yang sama.
const instanceFilter = `experiment_condition = $1
	AND invalidation_threshold_pct IS NOT DISTINCT FROM $2::numeric AND pregen = $3`

// expiringTTL: entry dengan sisa umur di bawah ini ditandai "expiring" (Short TTL di mockup).
const expiringTTL = 5 * time.Minute

// GET /api/admin/cache
func (a *App) AdminCache(c echo.Context) error {
	ctx := c.Request().Context()

	type inst struct {
		id                int
		symbol, assetType string
	}
	rows, err := a.store.pool.Query(ctx, `SELECT id, symbol, asset_type FROM instruments ORDER BY id`)
	if err != nil {
		return a.fail(c, err)
	}
	insts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (inst, error) {
		var i inst
		err := r.Scan(&i.id, &i.symbol, &i.assetType)
		return i, err
	})
	if err != nil {
		return a.fail(c, err)
	}

	type instMethod struct {
		id     int
		method string
	}
	lastTTL := map[instMethod]int{}
	rows, err = a.store.pool.Query(ctx, `
		SELECT DISTINCT ON (instrument_id, method) instrument_id, method, ttl_applied_seconds
		FROM inference_log WHERE ttl_applied_seconds IS NOT NULL AND experiment_condition = $1
		ORDER BY instrument_id, method, created_at DESC`, a.policy.Name())
	if err != nil {
		return a.fail(c, err)
	}
	for rows.Next() {
		var k instMethod
		var ttl int
		if err := rows.Scan(&k.id, &k.method, &ttl); err == nil {
			lastTTL[k] = ttl
		}
	}
	rows.Close()

	disabled := a.policy.Name() == "no_cache"
	window := 24
	if a.pregen != nil {
		window = a.pregen.Window
	}
	pop, _ := a.popular(ctx, window)
	access := map[string]float64{}
	for _, p := range pop {
		access[p.Symbol] = p.Hits
	}
	// Satu baris per (instrument, metode); harga dan S sekarang dihitung sekali per instrument.
	out := []cacheRow{}
	for _, in := range insts {
		base := cacheRow{Symbol: in.symbol, AssetType: in.assetType, Status: "empty", Access: access[in.symbol]}
		if q, err := a.CurrentQuote(ctx, in.symbol, in.id); err == nil && q.Price > 0 {
			base.PriceNow, base.PriceSource = &q.Price, q.Source
		}
		stateNow, stateLoaded := "", false
		for _, method := range analysisMethods {
			r := base
			r.Method = method
			if t, ok := lastTTL[instMethod{in.id, method}]; ok {
				r.LastTTLApplied = &t
			}
			if disabled {
				r.Status = "disabled"
				out = append(out, r)
				continue
			}
			s, ttl, err := a.policy.Inspect(ctx, in.symbol, method)
			if err != nil {
				return a.fail(c, err)
			}
			if s != nil {
				r.Status = "cached"
				if ttl >= 0 {
					sec := int(ttl.Seconds())
					r.TTLSeconds = &sec
					if ttl < expiringTTL {
						r.Status = "expiring"
					}
				}
				g := s.GeneratedAt.Format(time.RFC3339)
				r.GeneratedAt, r.Signal, r.Origin, r.StateGen = &g, s.Signal, s.Origin, s.State
				if !stateLoaded {
					if ac, err := a.loadAnalysisContext(ctx, in.symbol); err == nil {
						stateNow = ac.state
					}
					stateLoaded = true
				}
				r.StateNow = stateNow
				if s.PriceAtGeneration > 0 {
					p := s.PriceAtGeneration
					r.PriceAtGeneration = &p
					if dev, ok := priceDeviationPct(p, f64(r.PriceNow)); ok {
						r.DeviationPct = &dev
					}
				}
			}
			out = append(out, r)
		}
	}

	var hits, misses, invalidations, scheduled, backtestPending int
	if err := a.store.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE cache_status = 'HIT'),
		       count(*) FILTER (WHERE cache_status = 'MISS'),
		       count(*) FILTER (WHERE trigger = 'scheduled')
		FROM inference_log WHERE created_at >= now() - interval '24 hours' AND `+instanceFilter,
		a.policy.Name(), a.policy.Threshold(), a.cfg.pregen).
		Scan(&hits, &misses, &scheduled); err != nil {
		return a.fail(c, err)
	}
	if err := a.store.pool.QueryRow(ctx, `
		SELECT count(*) FROM cache_events WHERE created_at >= now() - interval '24 hours' AND `+instanceFilter,
		a.policy.Name(), a.policy.Threshold(), a.cfg.pregen).
		Scan(&invalidations); err != nil {
		return a.fail(c, err)
	}
	if err := a.store.pool.QueryRow(ctx, `
		SELECT count(*) FROM ai_summaries
		WHERE price_after_24h IS NULL AND generated_at <= now() - interval '24 hours'`).
		Scan(&backtestPending); err != nil {
		return a.fail(c, err)
	}
	var hitRatio *float64
	if hits+misses > 0 {
		r := float64(hits) / float64(hits+misses)
		hitRatio = &r
	}

	// Antrean Hermes dari AI service (kosong kalau service mati atau AI_MODE=stub).
	var queue map[string]any
	if h, _, err := aiHealth(ctx); err == nil {
		queue, _ = h["queue"].(map[string]any)
	}

	type event struct {
		At         time.Time `json:"at"`
		Symbol     string    `json:"symbol"`
		Method     *string   `json:"method"`
		Reason     string    `json:"reason"`
		ChangePct  *float64  `json:"change_pct"`
		DetectedBy *string   `json:"detected_by"`
		LatencyMS  *float64  `json:"l_inv_ms"`
		StateFrom  *string   `json:"state_from"`
		StateTo    *string   `json:"state_to"`
	}
	// Hanya event instance ini (instance lain bisa berjalan paralel di database yang sama).
	rows, err = a.store.pool.Query(ctx, `
		SELECT e.created_at, i.symbol, e.method, e.reason, e.price_change_pct::float8, e.detected_by,
		       e.invalidation_latency_ms::float8, e.state_from, e.state_to
		FROM cache_events e JOIN instruments i ON i.id = e.instrument_id
		WHERE e.experiment_condition = $1
		  AND e.invalidation_threshold_pct IS NOT DISTINCT FROM $2::numeric AND e.pregen = $3
		ORDER BY e.created_at DESC LIMIT 20`, a.policy.Name(), a.policy.Threshold(), a.cfg.pregen)
	if err != nil {
		return a.fail(c, err)
	}
	events, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (event, error) {
		var e event
		err := r.Scan(&e.At, &e.Symbol, &e.Method, &e.Reason, &e.ChangePct, &e.DetectedBy, &e.LatencyMS, &e.StateFrom, &e.StateTo)
		return e, err
	})
	if err != nil {
		return a.fail(c, err)
	}

	jobs, runs, jobErr := a.jobRuns(ctx)

	resp := map[string]any{
		"cache_mode":                 a.policy.Name(),
		"invalidation_threshold_pct": a.policy.Threshold(),
		"prompt_version":             env("PROMPT_VERSION", promptVersion),
		"instance":                   a.cfg.instanceKey(),
		"invalidate_on_state":        a.cfg.checkState,
		"params": map[string]string{
			"TTL_BASE_SECONDS":        env("TTL_BASE_SECONDS", env("STATIC_TTL_SECONDS", "3600")),
			"DYNAMIC_TTL_K":           env("DYNAMIC_TTL_K", "1"),
			"DYNAMIC_TTL_MIN_SECONDS": env("DYNAMIC_TTL_MIN_SECONDS", "300"),
			"DYNAMIC_TTL_MAX_SECONDS": env("DYNAMIC_TTL_MAX_SECONDS", "3600"),
			"SIGMA_REF_STAT":          env("SIGMA_REF_STAT", "mean"),
			"EVENT_MAX_TTL_SECONDS":   env("EVENT_MAX_TTL_SECONDS", "86400"),
		},
		"pregen": a.pregen,
		"kpi": map[string]any{
			"hit_ratio_24h": hitRatio, "hits_24h": hits, "misses_24h": misses,
			"invalidations_24h": invalidations, "scheduled_24h": scheduled,
			"backtest_pending": backtestPending, "ai_queue": queue,
		},
		"instruments": out,
		"events":      events,
		"jobs":        jobs,
		"job_runs":    runs,
	}
	if jobErr != nil {
		resp["jobs_error"] = "tabel job_runs belum ada, jalankan migrasi 006_job_runs.sql"
	}
	return c.JSON(http.StatusOK, resp)
}

type jobRun struct {
	Job        string    `json:"job"`
	Target     *string   `json:"target"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Status     string    `json:"status"`
	Rows       int       `json:"rows"`
	Message    *string   `json:"message"`
}

// jobRuns: ringkasan per job + 30 eksekusi terakhir. Jadwal yang diharapkan: ingest tiap jam,
// backtest tiap jam (boleh lebih jarang; ditandai basi setelah 26 jam).
func (a *App) jobRuns(ctx context.Context) ([]jobStatus, []jobRun, error) {
	expected := []struct {
		job, label string
		staleAfter time.Duration
	}{
		{"ingest_1h", "tiap jam (menit ke-5)", 75 * time.Minute},
		{"ingest_1d", "tiap jam / harian", 26 * time.Hour},
		{"backtest", "tiap jam / harian", 26 * time.Hour},
		{"fundamentals", "harian", 50 * time.Hour},
	}
	jobs := []jobStatus{}
	for _, e := range expected {
		js := jobStatus{Job: e.job, Expected: e.label}
		var last *time.Time
		err := a.store.pool.QueryRow(ctx, `
			SELECT max(finished_at),
			       (SELECT status FROM job_runs WHERE job = $1 ORDER BY finished_at DESC LIMIT 1),
			       count(*) FILTER (WHERE finished_at >= now() - interval '24 hours'),
			       count(*) FILTER (WHERE finished_at >= now() - interval '24 hours' AND status = 'fail')
			FROM job_runs WHERE job = $1`, e.job).Scan(&last, &js.LastStatus, &js.Runs24h, &js.Fails24h)
		if err != nil {
			return nil, nil, err
		}
		if last != nil {
			s := last.Format(time.RFC3339)
			js.LastRun = &s
		}
		js.Stale = last == nil || time.Since(*last) > e.staleAfter
		jobs = append(jobs, js)
	}
	rows, err := a.store.pool.Query(ctx, `
		SELECT job, target, started_at, finished_at, status, rows, message
		FROM job_runs ORDER BY finished_at DESC, id DESC LIMIT 30`)
	if err != nil {
		return nil, nil, err
	}
	runs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (jobRun, error) {
		var j jobRun
		err := r.Scan(&j.Job, &j.Target, &j.StartedAt, &j.FinishedAt, &j.Status, &j.Rows, &j.Message)
		return j, err
	})
	return jobs, runs, err
}

// POST /api/admin/cache/:symbol/invalidate?method=debate — buang entry cache satu instrument
// (reason manual). Tanpa method: semua metode.
func (a *App) AdminInvalidate(c echo.Context) error {
	ctx := c.Request().Context()
	symbol := strings.ToUpper(c.Param("symbol"))
	methods := analysisMethods
	if m := strings.ToLower(c.QueryParam("method")); m != "" {
		if !validMethod(m) {
			return badRequest(c, errors.New("method harus salah satu dari "+methodsList))
		}
		methods = []string{m}
	}
	instID, err := a.store.InstrumentID(ctx, symbol)
	if errors.Is(err, ErrNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "instrument tidak ditemukan"})
	}
	if err != nil {
		return a.fail(c, err)
	}
	if a.policy.Name() == "no_cache" {
		return c.JSON(http.StatusConflict, map[string]string{"error": "cache tidak aktif (no_cache)"})
	}
	removed := []string{}
	for _, method := range methods {
		s, _, err := a.policy.Inspect(ctx, symbol, method)
		if err != nil {
			return a.fail(c, err)
		}
		ok, err := a.policy.Invalidate(ctx, symbol, method)
		if err != nil {
			return a.fail(c, err)
		}
		if !ok {
			continue
		}
		removed = append(removed, method)
		inv := &Invalidation{Reason: "manual", InstrumentID: instID, Method: method}
		if s != nil {
			inv.SummaryID = s.SummaryID
			if q, err := a.CurrentQuote(ctx, symbol, instID); err == nil {
				if dev, ok := priceDeviationPct(s.PriceAtGeneration, q.Price); ok {
					inv.ChangePct = dev
				}
			}
		}
		a.logInvalidation(ctx, inv, "admin")
	}
	return c.JSON(http.StatusOK, map[string]any{"symbol": symbol, "removed": len(removed) > 0, "methods": removed})
}

// ---- Prompt ----

// GET /api/admin/prompt?symbol=BTCUSDT&method=debate — prompt aktif satu metode dirender dengan data
// terkini (tanpa memanggil model, tanpa token), isi skill Hermes-nya, dan riwayat versi prompt.
func (a *App) AdminPrompt(c echo.Context) error {
	ctx := c.Request().Context()
	symbol := strings.ToUpper(c.QueryParam("symbol"))
	if symbol == "" {
		symbol = "BTCUSDT"
	}
	method := strings.ToLower(c.QueryParam("method"))
	if method == "" {
		method = analysisMethods[0]
	}
	if !validMethod(method) {
		return badRequest(c, errors.New("method harus salah satu dari "+methodsList))
	}
	resp := map[string]any{"active_version": env("PROMPT_VERSION", promptVersion), "symbol": symbol,
		"method": method, "methods": analysisMethods}

	instID, err := a.store.InstrumentID(ctx, symbol)
	if errors.Is(err, ErrNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "instrument tidak ditemukan"})
	}
	if err != nil {
		return a.fail(c, err)
	}
	candles, err := a.store.LatestCandles(ctx, instID, analysisTimeframe, stateCandles)
	if err != nil {
		return a.fail(c, err)
	}
	quote, _ := a.CurrentQuote(ctx, symbol, instID)
	snap := indicatorSnapshot(candles)
	mc, err := a.marketContext(ctx, instID, realizedVolatility(tail(candles, analysisCandles)))
	if err != nil {
		return a.fail(c, err)
	}
	in := AIInput{Symbol: symbol, Method: method, Candles: tail(candles, analysisCandles), Quote: quote,
		Indicators: &snap, Context: mc}
	if preview, err := promptPreview(ctx, newAIRequest(in)); err != nil {
		resp["preview_error"] = "AI service tidak bisa dihubungi: " + err.Error()
	} else {
		resp["preview"] = preview
	}

	type version struct {
		Version      string    `json:"version"`
		Method       string    `json:"method"`
		Summaries    int       `json:"summaries"`
		FirstUsed    time.Time `json:"first_used"`
		LastUsed     time.Time `json:"last_used"`
		Models       []string  `json:"models"`
		Tested       int       `json:"backtested"`
		Correct      int       `json:"correct"`
		NoSignal     int       `json:"no_signal"`
		TokensInAvg  *float64  `json:"tokens_in_avg"`
		TokensOutAvg *float64  `json:"tokens_out_avg"`
		ExecMSAvg    *float64  `json:"exec_ms_avg"`
		FormatOKRate *float64  `json:"format_ok_rate"`
	}
	rows, err := a.store.pool.Query(ctx, `
		SELECT s.prompt_version, s.method, count(*), min(s.generated_at), max(s.generated_at),
		       array_agg(DISTINCT s.model),
		       count(*) FILTER (WHERE s.signal_correct IS NOT NULL),
		       count(*) FILTER (WHERE s.signal_correct),
		       count(*) FILTER (WHERE s.signal IS NULL),
		       avg(l.tokens_in)::float8, avg(l.tokens_out)::float8, avg(l.ai_exec_ms)::float8,
		       avg(CASE WHEN l.ai_format_ok THEN 1.0 WHEN NOT l.ai_format_ok THEN 0.0 END)::float8
		FROM ai_summaries s
		LEFT JOIN LATERAL (
		    SELECT tokens_in, tokens_out, ai_exec_ms, ai_format_ok FROM inference_log
		    WHERE summary_id = s.id AND cache_status IN ('MISS', 'BYPASS') LIMIT 1
		) l ON true
		GROUP BY s.prompt_version, s.method ORDER BY max(s.generated_at) DESC, s.method`)
	if err != nil {
		return a.fail(c, err)
	}
	versions, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (version, error) {
		var v version
		err := r.Scan(&v.Version, &v.Method, &v.Summaries, &v.FirstUsed, &v.LastUsed, &v.Models, &v.Tested,
			&v.Correct, &v.NoSignal, &v.TokensInAvg, &v.TokensOutAvg, &v.ExecMSAvg, &v.FormatOKRate)
		return v, err
	})
	if err != nil {
		return a.fail(c, err)
	}
	resp["versions"] = versions
	return c.JSON(http.StatusOK, resp)
}

func promptPreview(ctx context.Context, req aiRequest) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, aiBaseURL()+"/prompt/preview", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var out map[string]any
	return out, json.Unmarshal(raw, &out)
}

// ---- Evaluation ----

// evalRow: satu kondisi eksperimen (kondisi x θ x pre-generation). Metrik mengikuti Bab 2.7:
// H, latency (avg/p50/p95/p99), N_gen = N_miss + N_pre, C, S, W, D_serve, SSR(θ_s), SFR, L_inv.
type evalRow struct {
	Condition string   `json:"condition"`
	Threshold *float64 `json:"threshold_pct"`
	Pregen    bool     `json:"pregen"`

	// Request pengguna (semua trigger kecuali scheduled)
	Requests   int      `json:"requests"` // N_total
	Hits       int      `json:"hits"`
	Misses     int      `json:"misses"`
	Bypass     int      `json:"bypass"`
	Errors     int      `json:"errors"`
	HitRate    *float64 `json:"hit_rate_pct"` // H = N_hit / N_total
	LatencyAvg *float64 `json:"latency_avg_ms"`
	LatencyP50 *float64 `json:"latency_p50_ms"`
	LatencyP95 *float64 `json:"latency_p95_ms"`
	LatencyP99 *float64 `json:"latency_p99_ms"`
	LatencyHit *float64 `json:"latency_hit_avg_ms"`
	LatencyAI  *float64 `json:"latency_ai_avg_ms"`

	// Cost: semua analisis yang benar-benar di-generate (termasuk pre-generation)
	NGen         int      `json:"n_gen"` // N_miss + N_pre (BYPASS pada no_cache)
	NPre         int      `json:"n_pre"`
	TokensIn     int64    `json:"tokens_in"`
	TokensOut    int64    `json:"tokens_out"`
	CostUSD      float64  `json:"cost_usd"`
	TokensPerGen *float64 `json:"tokens_per_gen"`
	TokensPerReq *float64 `json:"tokens_per_request"`
	Savings      *float64 `json:"savings_pct"`          // S = (C_nc - C) / C_nc = (N - N_gen) / N
	NPreEnded    int      `json:"n_pre_ended"`          // entri pregen yang sudah kedaluwarsa / tergantikan
	NPreUnused   int      `json:"n_pre_unused"`         // ... dan tidak pernah disajikan
	Wasted       *float64 `json:"wasted_inference_pct"` // W

	// Freshness
	DataAgeAvg  *float64           `json:"data_age_avg_s"`
	CacheAgeAvg *float64           `json:"cache_age_avg_s"` // pada HIT
	CacheAgeP95 *float64           `json:"cache_age_p95_s"`
	DServeAvg   *float64           `json:"d_serve_avg_pct"` // |drift| pada HIT
	DServeP95   *float64           `json:"d_serve_p95_pct"`
	DServeMax   *float64           `json:"d_serve_max_pct"`
	SSR         map[string]float64 `json:"ssr_pct"` // stale serve rate per θ_s ("1", "2", "5", "10")
	SFR         *float64           `json:"sfr_pct"` // S_serve != S_gen pada HIT
	SFRBase     int                `json:"sfr_base"`

	// Invalidation
	InvPrice     int      `json:"inv_price_move"`
	InvState     int      `json:"inv_state_change"`
	InvManual    int      `json:"inv_manual"`
	InvByWatcher int      `json:"inv_by_watcher"`
	LInvAvg      *float64 `json:"l_inv_avg_ms"`
	LInvP50      *float64 `json:"l_inv_p50_ms"`
	LInvP95      *float64 `json:"l_inv_p95_ms"`

	FirstAt       string   `json:"first_at"`
	LastAt        string   `json:"last_at"`
	PromptVersion []string `json:"prompt_versions"`
}

var promptVersionRe = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,40}$`)

// θ_s yang dilaporkan untuk SSR (Bab 2.7); pada kondisi event/hybrid yang relevan adalah θ_s = θ.
var ssrThresholds = []float64{1, 2, 5, 10}

func groupKey(cond string, th *float64, pg bool) string {
	return fmt.Sprintf("%s|%v|%v", cond, f64(th), pg)
}

// GET /api/admin/evaluation?days=30&trigger=all&prompt=current|all&method=all|debate|mtf|grounded
// Agregasi per kondisi eksperimen: bahan tabel dan kurva trade-off Bab 4. method menyaring semua
// metrik ke satu metode analisis (all = gabungan; satu kunjungan halaman = satu request per metode).
func (a *App) AdminEvaluation(c echo.Context) error {
	ctx := c.Request().Context()
	days := queryInt(c, "days", 30, 1, 3650)
	trig := strings.ToLower(c.QueryParam("trigger"))
	if trig == "" {
		trig = "all"
	}
	if trig != "all" && (!validTriggers[trig] || trig == "scheduled") {
		return badRequest(c, errors.New("trigger harus all, ui, k6, manual, atau pilot"))
	}
	// prompt: current (versi aktif, default) | all | versi tertentu, mis. "stub" untuk uji pipeline lab
	prompt := env("PROMPT_VERSION", promptVersion)
	switch q := c.QueryParam("prompt"); {
	case q == "all":
		prompt = ""
	case q != "" && q != "current":
		if !promptVersionRe.MatchString(q) {
			return badRequest(c, errors.New("prompt tidak valid"))
		}
		prompt = q
	}
	method := strings.ToLower(c.QueryParam("method"))
	if method == "" {
		method = "all"
	}
	if method != "all" && method != "summary" && !validMethod(method) {
		return badRequest(c, errors.New("method harus all, summary, atau salah satu dari "+methodsList))
	}
	// $1 hari, $2 trigger, $3 prompt, $4 metode; request pengguna = semua trigger kecuali scheduled.
	const reqFilter = `created_at >= now() - make_interval(days => $1)
		AND trigger <> 'scheduled' AND ($2 = 'all' OR trigger = $2)
		AND ($3 = '' OR prompt_version = $3) AND ($4 = 'all' OR method = $4)`

	rows, err := a.store.pool.Query(ctx, `
		SELECT experiment_condition, invalidation_threshold_pct::float8, pregen,
		       count(*),
		       count(*) FILTER (WHERE cache_status = 'HIT'),
		       count(*) FILTER (WHERE cache_status = 'MISS'),
		       count(*) FILTER (WHERE cache_status = 'BYPASS'),
		       count(*) FILTER (WHERE cache_status = 'ERROR'),
		       avg(latency_ms) FILTER (WHERE cache_status <> 'ERROR')::float8,
		       percentile_cont(0.50) WITHIN GROUP (ORDER BY latency_ms) FILTER (WHERE cache_status <> 'ERROR'),
		       percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms) FILTER (WHERE cache_status <> 'ERROR'),
		       percentile_cont(0.99) WITHIN GROUP (ORDER BY latency_ms) FILTER (WHERE cache_status <> 'ERROR'),
		       avg(latency_ms) FILTER (WHERE cache_status = 'HIT')::float8,
		       avg(latency_ms) FILTER (WHERE cache_status IN ('MISS', 'BYPASS'))::float8,
		       avg(data_age_seconds) FILTER (WHERE cache_status <> 'ERROR')::float8,
		       avg(cache_age_seconds) FILTER (WHERE cache_status = 'HIT')::float8,
		       percentile_cont(0.95) WITHIN GROUP (ORDER BY cache_age_seconds) FILTER (WHERE cache_status = 'HIT'),
		       avg(abs(price_deviation_pct)) FILTER (WHERE cache_status = 'HIT')::float8,
		       percentile_cont(0.95) WITHIN GROUP (ORDER BY abs(price_deviation_pct)) FILTER (WHERE cache_status = 'HIT'),
		       max(abs(price_deviation_pct)) FILTER (WHERE cache_status = 'HIT')::float8,
		       count(*) FILTER (WHERE cache_status = 'HIT' AND abs(price_deviation_pct) > 1),
		       count(*) FILTER (WHERE cache_status = 'HIT' AND abs(price_deviation_pct) > 2),
		       count(*) FILTER (WHERE cache_status = 'HIT' AND abs(price_deviation_pct) > 5),
		       count(*) FILTER (WHERE cache_status = 'HIT' AND abs(price_deviation_pct) > 10),
		       count(*) FILTER (WHERE cache_status = 'HIT' AND state_changed IS NOT NULL),
		       count(*) FILTER (WHERE cache_status = 'HIT' AND state_changed),
		       min(created_at), max(created_at),
		       array_agg(DISTINCT coalesce(prompt_version, '-'))
		FROM inference_log
		WHERE `+reqFilter+`
		GROUP BY 1, 2, 3
		ORDER BY array_position(ARRAY['no_cache','static_ttl','dynamic_ttl','event_only','hybrid'], experiment_condition), 2, 3`,
		days, trig, prompt, method)
	if err != nil {
		return a.fail(c, err)
	}
	out := []*evalRow{}
	byKey := map[string]*evalRow{}
	for rows.Next() {
		e := &evalRow{SSR: map[string]float64{}}
		var ssr [4]int
		var stateChanged int
		var first, last time.Time
		if err := rows.Scan(&e.Condition, &e.Threshold, &e.Pregen, &e.Requests, &e.Hits, &e.Misses, &e.Bypass, &e.Errors,
			&e.LatencyAvg, &e.LatencyP50, &e.LatencyP95, &e.LatencyP99, &e.LatencyHit, &e.LatencyAI,
			&e.DataAgeAvg, &e.CacheAgeAvg, &e.CacheAgeP95, &e.DServeAvg, &e.DServeP95, &e.DServeMax,
			&ssr[0], &ssr[1], &ssr[2], &ssr[3], &e.SFRBase, &stateChanged,
			&first, &last, &e.PromptVersion); err != nil {
			rows.Close()
			return a.fail(c, err)
		}
		e.FirstAt, e.LastAt = first.Format(time.RFC3339), last.Format(time.RFC3339)
		if e.Requests > 0 && e.Condition != "no_cache" {
			h := float64(e.Hits) / float64(e.Requests) * 100
			e.HitRate = &h
		}
		if e.Hits > 0 {
			for i, th := range ssrThresholds {
				e.SSR[strconv.FormatFloat(th, 'f', -1, 64)] = float64(ssr[i]) / float64(e.Hits) * 100
			}
		}
		if e.SFRBase > 0 {
			v := float64(stateChanged) / float64(e.SFRBase) * 100
			e.SFR = &v
		}
		out = append(out, e)
		byKey[groupKey(e.Condition, e.Threshold, e.Pregen)] = e
	}
	rows.Close()

	// Cost: N_gen = MISS/BYPASS dari request yang lolos filter + PREGEN (terjadwal) di kondisi yang sama.
	if rows, err = a.store.pool.Query(ctx, `
		SELECT experiment_condition, invalidation_threshold_pct::float8, pregen,
		       count(*), count(*) FILTER (WHERE cache_status = 'PREGEN'),
		       coalesce(sum(tokens_in), 0), coalesce(sum(tokens_out), 0), coalesce(sum(cost_usd), 0)::float8
		FROM inference_log
		WHERE created_at >= now() - make_interval(days => $1) AND ($3 = '' OR prompt_version = $3)
		  AND ($4 = 'all' OR method = $4)
		  AND (cache_status = 'PREGEN'
		       OR (cache_status IN ('MISS', 'BYPASS') AND trigger <> 'scheduled' AND ($2 = 'all' OR trigger = $2)))
		GROUP BY 1, 2, 3`, days, trig, prompt, method); err != nil {
		return a.fail(c, err)
	}
	for rows.Next() {
		var cond string
		var th *float64
		var pg bool
		var n, pre int
		var in, outTok int64
		var cost float64
		if rows.Scan(&cond, &th, &pg, &n, &pre, &in, &outTok, &cost) != nil {
			continue
		}
		if e := byKey[groupKey(cond, th, pg)]; e != nil {
			e.NGen, e.NPre, e.TokensIn, e.TokensOut, e.CostUSD = n, pre, in, outTok, cost
		}
	}
	rows.Close()

	// Wasted inference: entri pregen yang masa hidupnya sudah berakhir (kedaluwarsa atau tergantikan
	// generate berikutnya) dan tidak pernah disajikan sebagai HIT ke pengguna.
	if rows, err = a.store.pool.Query(ctx, `
		WITH gens AS (
		    SELECT experiment_condition, invalidation_threshold_pct, pregen, summary_id, cache_status,
		           coalesce(lead(created_at) OVER (
		                        PARTITION BY experiment_condition, invalidation_threshold_pct, pregen, instrument_id, method
		                        ORDER BY created_at),
		                    created_at + make_interval(secs => coalesce(ttl_applied_seconds, 0))) <= now() AS ended
		    FROM inference_log
		    WHERE cache_status IN ('MISS', 'PREGEN') AND created_at >= now() - make_interval(days => $1)
		      AND ($2 = '' OR prompt_version = $2) AND ($3 = 'all' OR method = $3)
		)
		SELECT experiment_condition, invalidation_threshold_pct::float8, pregen,
		       count(*) FILTER (WHERE ended),
		       count(*) FILTER (WHERE ended AND NOT EXISTS (
		           SELECT 1 FROM inference_log h
		           WHERE h.summary_id = gens.summary_id AND h.cache_status = 'HIT' AND h.trigger <> 'scheduled'))
		FROM gens WHERE cache_status = 'PREGEN'
		GROUP BY 1, 2, 3`, days, prompt, method); err != nil {
		return a.fail(c, err)
	}
	for rows.Next() {
		var cond string
		var th *float64
		var pg bool
		var ended, unused int
		if rows.Scan(&cond, &th, &pg, &ended, &unused) == nil {
			if e := byKey[groupKey(cond, th, pg)]; e != nil {
				e.NPreEnded, e.NPreUnused = ended, unused
			}
		}
	}
	rows.Close()

	// Invalidation per kondisi; L_inv hanya dari watcher (yang mengetahui t_crossing).
	if rows, err = a.store.pool.Query(ctx, `
		SELECT experiment_condition, invalidation_threshold_pct::float8, pregen,
		       count(*) FILTER (WHERE reason = 'price_move'),
		       count(*) FILTER (WHERE reason = 'state_change'),
		       count(*) FILTER (WHERE reason = 'manual'),
		       count(*) FILTER (WHERE detected_by = 'watcher'),
		       avg(invalidation_latency_ms)::float8,
		       percentile_cont(0.5) WITHIN GROUP (ORDER BY invalidation_latency_ms),
		       percentile_cont(0.95) WITHIN GROUP (ORDER BY invalidation_latency_ms)
		FROM cache_events
		WHERE experiment_condition IS NOT NULL AND created_at >= now() - make_interval(days => $1)
		  AND ($2 = 'all' OR coalesce(method, 'summary') = $2)
		GROUP BY 1, 2, 3`, days, method); err != nil {
		return a.fail(c, err)
	}
	for rows.Next() {
		var cond string
		var th *float64
		var pg bool
		var r evalRow
		if rows.Scan(&cond, &th, &pg, &r.InvPrice, &r.InvState, &r.InvManual, &r.InvByWatcher,
			&r.LInvAvg, &r.LInvP50, &r.LInvP95) != nil {
			continue
		}
		if e := byKey[groupKey(cond, th, pg)]; e != nil {
			e.InvPrice, e.InvState, e.InvManual, e.InvByWatcher = r.InvPrice, r.InvState, r.InvManual, r.InvByWatcher
			e.LInvAvg, e.LInvP50, e.LInvP95 = r.LInvAvg, r.LInvP50, r.LInvP95
		}
	}
	rows.Close()

	for _, e := range out {
		if e.NGen > 0 {
			t := float64(e.TokensIn+e.TokensOut) / float64(e.NGen)
			e.TokensPerGen = &t
		}
		if served := e.Requests - e.Errors; served > 0 {
			t := float64(e.TokensIn+e.TokensOut) / float64(served)
			e.TokensPerReq = &t
			// S = (C_nc - C) / C_nc dengan C_nc = N x c̄ dan C = N_gen x c̄ (c̄ sama: model dibekukan).
			// Bisa negatif kalau pre-generation membuat lebih banyak analisis daripada jumlah request.
			sv := float64(served-e.NGen) / float64(served) * 100
			e.Savings = &sv
		}
		if e.NPreEnded > 0 {
			w := float64(e.NPreUnused) / float64(e.NPreEnded) * 100
			e.Wasted = &w
		}
	}

	type accRow struct {
		Symbol    string   `json:"symbol"`
		Method    string   `json:"method"`
		Signal    string   `json:"signal"`
		N         int      `json:"n"`
		Correct   int      `json:"correct"`
		AvgReturn *float64 `json:"avg_return_pct"`
	}
	rows, err = a.store.pool.Query(ctx, `
		SELECT i.symbol, s.method, s.signal, count(*), count(*) FILTER (WHERE s.signal_correct),
		       avg((s.price_after_24h / s.price_at_generation - 1) * 100)::float8
		FROM ai_summaries s JOIN instruments i ON i.id = s.instrument_id
		WHERE s.signal_correct IS NOT NULL
		  AND s.generated_at >= now() - make_interval(days => $1)
		  AND ($2 = '' OR s.prompt_version = $2) AND ($3 = 'all' OR s.method = $3)
		GROUP BY 1, 2, 3 ORDER BY 1, 2, 3`, days, prompt, method)
	if err != nil {
		return a.fail(c, err)
	}
	acc, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (accRow, error) {
		var x accRow
		err := r.Scan(&x.Symbol, &x.Method, &x.Signal, &x.N, &x.Correct, &x.AvgReturn)
		return x, err
	})
	if err != nil {
		return a.fail(c, err)
	}

	return c.JSON(http.StatusOK, map[string]any{
		"days": days, "trigger": trig, "prompt_version": prompt, "method": method, "ssr_thresholds": ssrThresholds,
		"conditions": out, "accuracy": acc,
	})
}

// ---- API Health ----

type component struct {
	Name      string   `json:"name"`
	Status    string   `json:"status"` // ok | warn | down
	Detail    string   `json:"detail"`
	LatencyMS *float64 `json:"latency_ms,omitempty"`
}

func ms(d time.Duration) *float64 {
	v := math.Round(float64(d.Microseconds())/100) / 10
	return &v
}

// GET /api/admin/health
func (a *App) AdminHealth(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	comps := []component{}

	start := time.Now()
	if err := a.store.pool.Ping(ctx); err != nil {
		comps = append(comps, component{Name: "PostgreSQL", Status: "down", Detail: err.Error()})
	} else {
		st := a.store.pool.Stat()
		comps = append(comps, component{Name: "PostgreSQL", Status: "ok", LatencyMS: ms(time.Since(start)),
			Detail: fmt.Sprintf("koneksi %d/%d dipakai", st.AcquiredConns(), st.MaxConns())})
	}

	if a.rdb != nil {
		start = time.Now()
		if err := a.rdb.Ping(ctx).Err(); err != nil {
			comps = append(comps, component{Name: "Redis", Status: "down", Detail: err.Error()})
		} else {
			n, _ := a.rdb.Keys(ctx, "ta:summary:*").Result()
			detail := fmt.Sprintf("%d entry cache summary", len(n))
			if a.policy.Name() == "no_cache" {
				detail += " · tidak dipakai (no_cache)"
			}
			comps = append(comps, component{Name: "Redis", Status: "ok", LatencyMS: ms(time.Since(start)), Detail: detail})
		}
	} else {
		comps = append(comps, component{Name: "Redis", Status: "down", Detail: "tidak bisa terhubung saat backend start"})
	}

	if h, lat, err := aiHealth(ctx); err != nil {
		st := "down"
		if env("AI_MODE", "stub") != "http" {
			st = "warn"
		}
		comps = append(comps, component{Name: "AI service (Hermes)", Status: st, Detail: err.Error()})
	} else {
		st := "ok"
		if h["status"] != "ok" {
			st = "warn"
		}
		detail := fmt.Sprintf("model %v · prompt %v", h["model"], h["prompt_version"])
		if q, ok := h["queue"].(map[string]any); ok {
			detail += fmt.Sprintf(" · antrean %v jalan / %v menunggu", q["running"], q["waiting"])
		}
		if hb, ok := h["hermes_bin"].(string); ok && strings.HasPrefix(hb, "TIDAK") {
			detail = hb
		}
		comps = append(comps, component{Name: "AI service (Hermes)", Status: st, LatencyMS: ms(lat), Detail: detail})
	}
	if env("AI_MODE", "stub") != "http" {
		comps[len(comps)-1].Detail += " · backend memakai AI_MODE=stub"
	}

	if a.prices != nil {
		ages := a.prices.Ages()
		label := map[string]string{"spot": "Binance spot WebSocket (price feed)", "futures": "Binance futures WebSocket (price feed)"}
		for _, st := range a.prices.Streams() {
			// Status ditentukan kesehatan koneksi market, bukan simbol yang jarang bertransaksi:
			// harga transaksi terakhir tetap dipakai sampai simbol diam > PRICE_IDLE_MAX_SECONDS.
			parts, idle := []string{}, 0
			for _, sym := range st.Symbols {
				age, ok := ages[sym]
				switch {
				case !ok:
					idle++
					parts = append(parts, sym+" belum ada harga")
				case age > a.prices.idleMax.Seconds():
					idle++
					parts = append(parts, fmt.Sprintf("%s diam %s (pakai harga DB)", sym, fmtSeconds(age)))
				default:
					parts = append(parts, fmt.Sprintf("%s %s lalu", sym, fmtSeconds(age)))
				}
			}
			status := "ok"
			switch {
			case !st.Connected:
				status = "down"
			case st.Messages == 0 && st.UpSeconds > 60:
				// Koneksi terbuka tapi tidak ada data sama sekali: biasanya URL/path stream salah.
				status = "down"
			case st.LastMsgAgo != nil && *st.LastMsgAgo > a.prices.maxAge.Seconds():
				status = "warn" // tersambung tapi tidak ada pesan apa pun beberapa saat
			case idle > 0:
				status = "warn"
			}
			detail := fmt.Sprintf("aggTrade · %d pesan · reconnect %d", st.Messages, st.Reconnects)
			if st.Connected {
				detail = fmt.Sprintf("tersambung %s · ", time.Duration(st.UpSeconds)*time.Second) + detail
			} else if st.LastError != "" {
				detail += " · " + truncate(st.LastError, 100)
			}
			comps = append(comps, component{Name: label[st.Market], Status: status,
				Detail: detail + " · " + strings.Join(parts, " · ")})
		}
	}

	// Ingestion per sumber dari job_runs (eksekusi ingest_1h terakhir per sumber).
	rows, err := a.store.pool.Query(ctx, `
		SELECT i.source,
		       max(j.finished_at) FILTER (WHERE j.status = 'ok'),
		       count(*) FILTER (WHERE j.status = 'fail' AND j.finished_at >= now() - interval '24 hours'),
		       (SELECT message FROM job_runs j2 JOIN instruments i2 ON i2.symbol = j2.target
		        WHERE i2.source = i.source AND j2.status = 'fail' ORDER BY j2.finished_at DESC LIMIT 1),
		       max(o.max_ts)
		FROM instruments i
		LEFT JOIN job_runs j ON j.target = i.symbol AND j.job = 'ingest_1h'
		LEFT JOIN LATERAL (SELECT max(ts) AS max_ts FROM ohlcv WHERE instrument_id = i.id AND timeframe = '1h') o ON true
		GROUP BY i.source ORDER BY i.source`)
	if err != nil {
		comps = append(comps, component{Name: "Ingestion", Status: "warn", Detail: "riwayat job belum tersedia (migrasi 006_job_runs.sql)"})
	} else {
		label := map[string]string{"binance": "Ingestion Binance spot", "binance_futures": "Ingestion Binance futures", "yahoo": "Ingestion Yahoo Finance"}
		for rows.Next() {
			var src string
			var lastOK, lastCandle *time.Time
			var fails int
			var lastErr *string
			if err := rows.Scan(&src, &lastOK, &fails, &lastErr, &lastCandle); err != nil {
				continue
			}
			comp := component{Name: label[src], Status: "ok"}
			if comp.Name == "" {
				comp.Name = "Ingestion " + src
			}
			switch {
			case lastOK == nil:
				comp.Status, comp.Detail = "down", "belum pernah berhasil (job_runs kosong)"
			default:
				age := time.Since(*lastOK)
				comp.Detail = "sync terakhir " + humanAge(age)
				if age > 3*time.Hour {
					comp.Status = "down"
				} else if age > 75*time.Minute {
					comp.Status = "warn"
				}
			}
			if lastCandle != nil {
				comp.Detail += " · candle 1h terbaru " + idDateTime(lastCandle.In(jakarta())) + " WIB"
			}
			if fails > 0 {
				if comp.Status == "ok" {
					comp.Status = "warn"
				}
				comp.Detail += fmt.Sprintf(" · %d gagal/24 jam", fails)
				if lastErr != nil {
					comp.Detail += ": " + truncate(*lastErr, 120)
				}
			}
			comps = append(comps, comp)
		}
		rows.Close()
	}

	type logEntry struct {
		At      time.Time `json:"at"`
		Source  string    `json:"source"`
		Message string    `json:"message"`
	}
	logs := []logEntry{}
	if rows, err := a.store.pool.Query(ctx, `
		SELECT l.created_at, 'analyze ' || coalesce(i.symbol, '?'), coalesce(l.error, 'error')
		FROM inference_log l LEFT JOIN instruments i ON i.id = l.instrument_id
		WHERE l.cache_status = 'ERROR' AND l.created_at >= now() - interval '7 days'
		ORDER BY l.created_at DESC LIMIT 30`); err == nil {
		for rows.Next() {
			var e logEntry
			if rows.Scan(&e.At, &e.Source, &e.Message) == nil {
				logs = append(logs, e)
			}
		}
		rows.Close()
	}
	if rows, err := a.store.pool.Query(ctx, `
		SELECT finished_at, job || coalesce(' ' || target, ''), coalesce(message, 'gagal')
		FROM job_runs WHERE status = 'fail' AND finished_at >= now() - interval '7 days'
		ORDER BY finished_at DESC LIMIT 30`); err == nil {
		for rows.Next() {
			var e logEntry
			if rows.Scan(&e.At, &e.Source, &e.Message) == nil {
				logs = append(logs, e)
			}
		}
		rows.Close()
	}
	// Gabungan dua sumber, terbaru di atas.
	for i := 1; i < len(logs); i++ {
		for j := i; j > 0 && logs[j].At.After(logs[j-1].At); j-- {
			logs[j], logs[j-1] = logs[j-1], logs[j]
		}
	}
	if len(logs) > 30 {
		logs = logs[:30]
	}

	return c.JSON(http.StatusOK, map[string]any{"checked_at": time.Now(), "components": comps, "errors": logs})
}

// fmtSeconds: 45 -> "45 dtk", 190 -> "3 mnt", 7200 -> "2 jam".
func fmtSeconds(sec float64) string {
	switch {
	case sec < 60:
		return fmt.Sprintf("%.0f dtk", sec)
	case sec < 3600:
		return fmt.Sprintf("%.0f mnt", sec/60)
	}
	return fmt.Sprintf("%.0f jam", sec/3600)
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "baru saja"
	case d < time.Hour:
		return fmt.Sprintf("%d menit lalu", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d jam lalu", int(d.Hours()))
	}
	return fmt.Sprintf("%d hari lalu", int(d.Hours()/24))
}

// idDateTime: "30 Okt 16:00" (nama bulan Indonesia).
func idDateTime(t time.Time) string {
	months := [...]string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}
	return fmt.Sprintf("%02d %s %s", t.Day(), months[t.Month()-1], t.Format("15:04"))
}

func jakarta() *time.Location {
	if loc, err := time.LoadLocation(adminTZ); err == nil {
		return loc
	}
	return time.FixedZone("WIB", 7*3600)
}

// newAdminRedis: klien Redis untuk API Health, juga saat CACHE_MODE=no_cache. nil kalau tidak tersambung.
func newAdminRedis() *redis.Client {
	rdb, err := newRedis()
	if err != nil {
		return nil
	}
	return rdb
}

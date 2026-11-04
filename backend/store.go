package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("tidak ditemukan")

type Candle struct {
	TS     time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

// InferenceLog adalah satu baris pengukuran per request (tabel inference_log).
type InferenceLog struct {
	InstrumentID    int
	Condition       string
	CacheStatus     string
	LatencyMS       int
	Model           string
	TokensIn        int
	TokensOut       int
	CacheReadTokens int
	CostUSD         float64
	DataAgeSeconds  *int
	TTLSeconds      *int
	Volatility      float64

	Trigger           string
	PromptVersion     string
	Threshold         *float64
	SummaryID         *int64
	PriceAtGeneration *float64
	PriceAtRequest    *float64
	PriceSource       string
	PriceDeviationPct *float64
	CacheAgeSeconds   *int
	AIQueueMS         *int
	AIExecMS          *int
	AIFormatOK        *bool
	Error             string

	Pregen            bool
	StateAtGeneration string
	StateAtRequest    string
	StateChanged      *bool
	Method            string // debate | mtf | grounded
}

// CacheEvent: satu invalidation (tabel cache_events). L_inv = waktu dicatat - CrossedAt.
type CacheEvent struct {
	InstrumentID int
	Reason       string // price_move | state_change | manual
	ChangePct    *float64
	Condition    string
	Threshold    *float64
	Pregen       bool
	SummaryID    *int64
	DetectedBy   string // watcher | request | scheduler | admin
	CrossedAt    *time.Time
	LatencyMS    *float64
	StateFrom    string
	StateTo      string
	Method       string
}

type Store struct {
	pool *pgxpool.Pool
}

func (s *Store) InstrumentID(ctx context.Context, symbol string) (int, error) {
	var id int
	err := s.pool.QueryRow(ctx, `SELECT id FROM instruments WHERE symbol = $1`, symbol).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

func (s *Store) SymbolsBySource(ctx context.Context, source string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT symbol FROM instruments WHERE source = $1 ORDER BY id`, source)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// LatestCandles mengembalikan n candle terbaru yang SUDAH TUTUP, urut dari lama ke baru.
// Syarat fetched_at >= ts + durasi candle membuang candle yang masih berjalan
// (dan salinan parsialnya) sehingga umur data (age) punya makna yang bersih.
func (s *Store) LatestCandles(ctx context.Context, instID int, timeframe string, n int) ([]Candle, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ts, open::float8, high::float8, low::float8, close::float8, volume::float8
		FROM ohlcv
		WHERE instrument_id = $1 AND timeframe = $2
		  AND fetched_at >= ts + make_interval(secs => $4)
		ORDER BY ts DESC
		LIMIT $3`, instID, timeframe, n, timeframeDuration(timeframe).Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Candle
	for rows.Next() {
		var c Candle
		if err := rows.Scan(&c.TS, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // balik ke urutan lama -> baru
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// InsertSummary menyimpan hasil AI dan mengembalikan id serta generated_at.
// NewSummary: satu hasil AI yang akan disimpan ke ai_summaries.
type NewSummary struct {
	InstrumentID   int
	SourceTS       time.Time
	PromptVersion  string
	Model, Content string
	Signal         string
	PriceAtGen     float64
	Support        *float64
	Resistance     *float64
	Origin         string // ondemand | pregen
	State          string
	Indicators     *IndicatorSnapshot
	Method         string
	Confidence     *int
	Detail         json.RawMessage // nil -> NULL (output AI bukan JSON)
}

func (s *Store) InsertSummary(ctx context.Context, n NewSummary) (int64, time.Time, error) {
	instID, sourceTS, pv, model, content, signal := n.InstrumentID, n.SourceTS, n.PromptVersion, n.Model, n.Content, n.Signal
	priceAtGen, support, resistance := n.PriceAtGen, n.Support, n.Resistance

	var sig any // nil -> NULL (tabel hanya menerima BUY, SELL, HOLD)
	switch signal {
	case "BUY", "SELL", "HOLD":
		sig = signal
	}
	var detail any // json.RawMessage nil harus jadi NULL, bukan string kosong
	if len(n.Detail) > 0 {
		detail = string(n.Detail)
	}
	var id int64
	var genAt time.Time
	err := s.pool.QueryRow(ctx, `
		INSERT INTO ai_summaries
		    (instrument_id, source_data_ts, prompt_version, model, content, signal, price_at_generation,
		     support, resistance, origin, state_at_generation, indicators, method, confidence, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, ''), $12, $13, $14, $15::jsonb)
		RETURNING id, generated_at`,
		instID, sourceTS, pv, model, content, sig, priceAtGen, support, resistance,
		n.Origin, n.State, n.Indicators, n.Method, n.Confidence, detail).Scan(&id, &genAt)
	return id, genAt, err
}

func (s *Store) LogInference(ctx context.Context, l InferenceLog) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO inference_log
		    (instrument_id, experiment_condition, cache_status, latency_ms,
		     model, tokens_in, tokens_out, cache_read_tokens, cost_usd,
		     data_age_seconds, ttl_applied_seconds, realized_volatility,
		     trigger, prompt_version, invalidation_threshold_pct, summary_id,
		     price_at_generation, price_at_request, price_source, price_deviation_pct,
		     cache_age_seconds, ai_queue_ms, ai_exec_ms, ai_format_ok, error,
		     pregen, state_at_generation, state_at_request, state_changed, method)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10, $11, $12,
		        $13, NULLIF($14, ''), $15, $16, $17, $18, NULLIF($19, ''), $20,
		        $21, $22, $23, $24, NULLIF($25, ''),
		        $26, NULLIF($27, ''), NULLIF($28, ''), $29, $30)`,
		l.InstrumentID, l.Condition, l.CacheStatus, l.LatencyMS,
		l.Model, l.TokensIn, l.TokensOut, l.CacheReadTokens, l.CostUSD,
		l.DataAgeSeconds, l.TTLSeconds, l.Volatility,
		l.Trigger, l.PromptVersion, l.Threshold, l.SummaryID,
		l.PriceAtGeneration, l.PriceAtRequest, l.PriceSource, l.PriceDeviationPct,
		l.CacheAgeSeconds, l.AIQueueMS, l.AIExecMS, l.AIFormatOK, l.Error,
		l.Pregen, l.StateAtGeneration, l.StateAtRequest, l.StateChanged, l.Method)
	return err
}

func (s *Store) LogCacheEvent(ctx context.Context, e CacheEvent) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO cache_events
		    (instrument_id, reason, price_change_pct, experiment_condition, invalidation_threshold_pct, pregen,
		     summary_id, detected_by, crossed_at, invalidation_latency_ms, state_from, state_to, method)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, ''), NULLIF($12, ''), NULLIF($13, ''))`,
		e.InstrumentID, e.Reason, e.ChangePct, e.Condition, e.Threshold, e.Pregen,
		e.SummaryID, e.DetectedBy, e.CrossedAt, e.LatencyMS, e.StateFrom, e.StateTo, e.Method)
	return err
}

// LatestPrice: close candle 1h terbaru (termasuk yang masih berjalan) sebagai harga cadangan
// kalau price feed tidak tersedia. TS = fetched_at, yaitu kapan harga itu benar-benar diambil.
func (s *Store) LatestPrice(ctx context.Context, instID int) (Quote, error) {
	q := Quote{Source: "db"}
	err := s.pool.QueryRow(ctx, `
		SELECT close::float8, fetched_at FROM ohlcv
		WHERE instrument_id = $1 AND timeframe = '1h'
		ORDER BY ts DESC LIMIT 1`, instID).Scan(&q.Price, &q.TS)
	if errors.Is(err, pgx.ErrNoRows) {
		return q, ErrNotFound
	}
	return q, err
}

// SigmaRef menghitung σ_ref per symbol dari realized volatility rolling (window n candle 1h
// yang sudah tutup) selama 1 tahun terakhir. stat: "mean" (rata-rata historis, Bab 3) atau "pNN".
func (s *Store) SigmaRef(ctx context.Context, n int, stat string) (map[string]float64, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT i.symbol, o.close::float8
		FROM ohlcv o JOIN instruments i ON i.id = o.instrument_id
		WHERE o.timeframe = '1h' AND o.ts >= now() - interval '365 days'
		  AND o.fetched_at >= o.ts + interval '1 hour'
		ORDER BY i.symbol, o.ts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	closes := map[string][]Candle{}
	for rows.Next() {
		var sym string
		var c Candle
		if err := rows.Scan(&sym, &c.Close); err != nil {
			return nil, err
		}
		closes[sym] = append(closes[sym], c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := map[string]float64{}
	for sym, cs := range closes {
		var vols []float64
		for i := n; i <= len(cs); i++ {
			vols = append(vols, realizedVolatility(cs[i-n:i]))
		}
		sort.Float64s(vols)
		v, err := sigmaRefStat(vols, stat)
		if err != nil {
			return nil, err
		}
		out[sym] = v
	}
	return out, nil
}

// timeframeDuration: lama satu candle, dipakai untuk memastikan hanya candle yang sudah tutup yang dianalisis.
func timeframeDuration(tf string) time.Duration {
	switch tf {
	case "1d":
		return 24 * time.Hour
	default:
		return time.Hour
	}
}

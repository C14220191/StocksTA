package main

import (
	"context"
	"errors"
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
	DataAgeSeconds  int
	TTLSeconds      *int
	Volatility      float64
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
func (s *Store) InsertSummary(ctx context.Context, instID int, sourceTS time.Time,
	pv, model, content, signal string, priceAtGen float64, support, resistance *float64) (int64, time.Time, error) {

	var sig any // nil -> NULL (tabel hanya menerima BUY, SELL, HOLD)
	switch signal {
	case "BUY", "SELL", "HOLD":
		sig = signal
	}
	var id int64
	var genAt time.Time
	err := s.pool.QueryRow(ctx, `
		INSERT INTO ai_summaries
		    (instrument_id, source_data_ts, prompt_version, model, content, signal, price_at_generation, support, resistance)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, generated_at`,
		instID, sourceTS, pv, model, content, sig, priceAtGen, support, resistance).Scan(&id, &genAt)
	return id, genAt, err
}

func (s *Store) LogInference(ctx context.Context, l InferenceLog) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO inference_log
		    (instrument_id, experiment_condition, cache_status, latency_ms,
		     model, tokens_in, tokens_out, cache_read_tokens, cost_usd,
		     data_age_seconds, ttl_applied_seconds, realized_volatility)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10, $11, $12)`,
		l.InstrumentID, l.Condition, l.CacheStatus, l.LatencyMS,
		l.Model, l.TokensIn, l.TokensOut, l.CacheReadTokens, l.CostUSD,
		l.DataAgeSeconds, l.TTLSeconds, l.Volatility)
	return err
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

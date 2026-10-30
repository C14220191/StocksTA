package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// CachedSummary adalah hasil AI inference yang disimpan di cache.
type CachedSummary struct {
	SummaryID    int64
	Content      string
	Signal       string
	Model        string
	SourceDataTS time.Time // ts candle terakhir yang dipakai saat summary dibuat
	GeneratedAt  time.Time
	Support      *float64 // level dari AI
	Resistance   *float64 // level dari AI
}

// CachePolicy adalah satu-satunya titik yang berbeda antar kondisi eksperimen.
// Semua kondisi (no_cache, static_ttl, dynamic_ttl, event_only, hybrid)
// mengimplementasikan interface ini, sehingga handler dan alur data sama persis.
type CachePolicy interface {
	// Name dipakai sebagai experiment_condition di inference_log.
	Name() string
	Get(ctx context.Context, symbol string) (*CachedSummary, bool)
	// Set mengembalikan TTL yang diterapkan (detik), nil kalau tidak ada TTL.
	Set(ctx context.Context, symbol string, s CachedSummary, volatility float64) *int
}

// NewPolicy sengaja gagal untuk mode yang belum diimplementasikan,
// supaya eksperimen tidak diam-diam berjalan dengan kondisi yang salah.
func NewPolicy(mode string) (CachePolicy, error) {
	switch mode {
	case "no_cache":
		return NoCache{}, nil
	case "static_ttl":
		rdb, err := newRedis()
		if err != nil {
			return nil, err
		}
		sec, err := strconv.Atoi(env("STATIC_TTL_SECONDS", "3600"))
		if err != nil || sec <= 0 {
			return nil, fmt.Errorf("STATIC_TTL_SECONDS tidak valid")
		}
		return &StaticTTL{rdb: rdb, ttl: time.Duration(sec) * time.Second}, nil
	default:
		return nil, fmt.Errorf("CACHE_MODE %q belum diimplementasikan", mode)
	}
}

func newRedis() (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{Addr: env("REDIS_ADDR", "127.0.0.1:6379")})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("tidak bisa konek ke Redis: %w", err)
	}
	return rdb, nil
}

// NoCache: setiap request selalu memanggil AI (baseline).
type NoCache struct{}

func (NoCache) Name() string { return "no_cache" }

func (NoCache) Get(context.Context, string) (*CachedSummary, bool) { return nil, false }

func (NoCache) Set(context.Context, string, CachedSummary, float64) *int { return nil }

// StaticTTL: summary disimpan di Redis dengan TTL tetap yang sama untuk semua instrument.
type StaticTTL struct {
	rdb *redis.Client
	ttl time.Duration
}

func (p *StaticTTL) Name() string { return "static_ttl" }

func (p *StaticTTL) key(symbol string) string {
	return "ta:summary:" + symbol + ":" + analysisTimeframe
}

func (p *StaticTTL) Get(ctx context.Context, symbol string) (*CachedSummary, bool) {
	b, err := p.rdb.Get(ctx, p.key(symbol)).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.Error("redis get gagal", "err", err)
		}
		return nil, false
	}
	var s CachedSummary
	if err := json.Unmarshal(b, &s); err != nil {
		slog.Error("cache rusak, dianggap miss", "err", err)
		return nil, false
	}
	return &s, true
}

func (p *StaticTTL) Set(ctx context.Context, symbol string, s CachedSummary, _ float64) *int {
	b, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	if err := p.rdb.Set(ctx, p.key(symbol), b, p.ttl).Err(); err != nil {
		slog.Error("redis set gagal", "err", err)
		return nil
	}
	sec := int(p.ttl.Seconds())
	return &sec
}

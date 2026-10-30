package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// CachedSummary adalah hasil AI inference yang disimpan di cache.
type CachedSummary struct {
	SummaryID         int64
	Content           string
	Signal            string
	Model             string
	PromptVersion     string
	SourceDataTS      time.Time // ts candle terakhir yang dipakai saat summary dibuat
	GeneratedAt       time.Time
	PriceAtGeneration float64  // harga terkini yang dilihat AI (dasar event invalidation)
	Support           *float64 // level dari AI
	Resistance        *float64 // level dari AI
}

// Invalidation menjelaskan kenapa entry cache dibuang saat Get (dicatat ke cache_events).
type Invalidation struct {
	Reason    string  // "price_move"
	ChangePct float64 // bertanda
}

// CachePolicy adalah satu-satunya titik yang berbeda antar kondisi eksperimen.
// Semua kondisi (no_cache, static_ttl, dynamic_ttl, event_only, hybrid)
// mengimplementasikan interface ini, sehingga handler dan alur data sama persis.
type CachePolicy interface {
	// Name dipakai sebagai experiment_condition di inference_log.
	Name() string
	// Threshold: ambang event invalidation (persen), nil kalau kondisi ini tidak memakainya.
	Threshold() *float64
	// Get mengembalikan summary kalau HIT. Kalau entry ada tapi dibuang karena event,
	// summary nil dan inv terisi.
	Get(ctx context.Context, symbol string, price float64) (s *CachedSummary, inv *Invalidation)
	// Set mengembalikan TTL yang diterapkan (detik), nil kalau tidak ada TTL.
	Set(ctx context.Context, symbol string, s CachedSummary, volatility float64) *int
	Ping(ctx context.Context) error
}

// NewPolicy sengaja gagal untuk mode yang tidak dikenal,
// supaya eksperimen tidak diam-diam berjalan dengan kondisi yang salah.
func NewPolicy(ctx context.Context, mode string, store *Store) (CachePolicy, error) {
	if mode == "no_cache" {
		return NoCache{}, nil
	}

	p := &RedisPolicy{name: mode, version: env("PROMPT_VERSION", promptVersion)}
	switch mode {
	case "static_ttl":
		sec, err := envSeconds("STATIC_TTL_SECONDS", 3600)
		if err != nil {
			return nil, err
		}
		p.ttl = func(string, float64) time.Duration { return sec }
	case "dynamic_ttl", "hybrid":
		dt, err := newDynamicTTL(ctx, store)
		if err != nil {
			return nil, err
		}
		p.ttl = dt.TTL
	case "event_only":
		// Tanpa kedaluwarsa berbasis waktu; batas atas hanya pengaman supaya Redis tidak menumpuk entry.
		sec, err := envSeconds("EVENT_MAX_TTL_SECONDS", 86400)
		if err != nil {
			return nil, err
		}
		p.ttl = func(string, float64) time.Duration { return sec }
	default:
		return nil, fmt.Errorf("CACHE_MODE %q tidak dikenal", mode)
	}
	if mode == "event_only" || mode == "hybrid" {
		th, err := strconv.ParseFloat(env("INVALIDATION_THRESHOLD_PCT", "2"), 64)
		if err != nil || th <= 0 {
			return nil, fmt.Errorf("INVALIDATION_THRESHOLD_PCT tidak valid")
		}
		p.threshold = th
	}

	rdb, err := newRedis()
	if err != nil {
		return nil, err
	}
	p.rdb = rdb
	return p, nil
}

func envSeconds(key string, def int) (time.Duration, error) {
	sec, err := strconv.Atoi(env(key, strconv.Itoa(def)))
	if err != nil || sec <= 0 {
		return 0, fmt.Errorf("%s tidak valid", key)
	}
	return time.Duration(sec) * time.Second, nil
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

func (NoCache) Name() string                                             { return "no_cache" }
func (NoCache) Threshold() *float64                                      { return nil }
func (NoCache) Ping(context.Context) error                               { return nil }
func (NoCache) Set(context.Context, string, CachedSummary, float64) *int { return nil }
func (NoCache) Get(context.Context, string, float64) (*CachedSummary, *Invalidation) {
	return nil, nil
}

// RedisPolicy dipakai oleh empat kondisi cache. Perbedaannya hanya di fungsi TTL dan ambang event:
//
//	static_ttl  : TTL tetap,     tanpa event
//	dynamic_ttl : TTL adaptif,   tanpa event
//	event_only  : TTL pengaman,  dengan event
//	hybrid      : TTL adaptif,   dengan event
type RedisPolicy struct {
	name      string
	version   string // prompt_version, bagian dari key supaya summary prompt lama tidak terpakai
	rdb       *redis.Client
	ttl       func(symbol string, volatility float64) time.Duration
	threshold float64 // persen; 0 = tanpa event invalidation
}

func (p *RedisPolicy) Name() string { return p.name }

func (p *RedisPolicy) Threshold() *float64 {
	if p.threshold <= 0 {
		return nil
	}
	return &p.threshold
}

func (p *RedisPolicy) Ping(ctx context.Context) error { return p.rdb.Ping(ctx).Err() }

func (p *RedisPolicy) key(symbol string) string {
	return "ta:summary:" + p.version + ":" + symbol + ":" + analysisTimeframe
}

func (p *RedisPolicy) Get(ctx context.Context, symbol string, price float64) (*CachedSummary, *Invalidation) {
	b, err := p.rdb.Get(ctx, p.key(symbol)).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.Error("redis get gagal", "err", err)
		}
		return nil, nil
	}
	var s CachedSummary
	if err := json.Unmarshal(b, &s); err != nil {
		slog.Error("cache rusak, dianggap miss", "err", err)
		return nil, nil
	}
	if p.threshold > 0 {
		if dev, ok := priceDeviationPct(s.PriceAtGeneration, price); ok && math.Abs(dev) >= p.threshold {
			// DEL mengembalikan 1 hanya untuk request yang benar-benar membuang entry,
			// jadi request bersamaan tidak mencatat event yang sama berkali-kali.
			n, err := p.rdb.Del(ctx, p.key(symbol)).Result()
			if err != nil {
				slog.Error("redis del gagal", "err", err)
			}
			if n == 1 {
				return nil, &Invalidation{Reason: "price_move", ChangePct: dev}
			}
			return nil, nil
		}
	}
	return &s, nil
}

func (p *RedisPolicy) Set(ctx context.Context, symbol string, s CachedSummary, vol float64) *int {
	b, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	ttl := p.ttl(symbol, vol)
	if err := p.rdb.Set(ctx, p.key(symbol), b, ttl).Err(); err != nil {
		slog.Error("redis set gagal", "err", err)
		return nil
	}
	sec := int(ttl.Seconds())
	return &sec
}

// priceDeviationPct = (now / atGen - 1) * 100. ok=false kalau salah satu harga tidak diketahui.
func priceDeviationPct(atGen, now float64) (float64, bool) {
	if atGen <= 0 || now <= 0 {
		return 0, false
	}
	return (now/atGen - 1) * 100, true
}

// ---- dynamic TTL ----

// DynamicTTL: TTL = TTL_max - norm * (TTL_max - TTL_min), norm = clamp(σ / σ_ref, 0, 1).
// σ = realized volatility 14 candle 1h terakhir (sama dengan realizedVolatility di handler),
// σ_ref = persentil ke-90 σ historis instrument itu (rolling window 14 candle, 1 tahun).
// Volatilitas tinggi -> TTL pendek (summary cepat basi), pasar tenang -> TTL panjang.
type DynamicTTL struct {
	min, max time.Duration
	ref      map[string]float64 // symbol -> σ_ref
}

func newDynamicTTL(ctx context.Context, store *Store) (*DynamicTTL, error) {
	minTTL, err := envSeconds("DYNAMIC_TTL_MIN_SECONDS", 300)
	if err != nil {
		return nil, err
	}
	maxTTL, err := envSeconds("DYNAMIC_TTL_MAX_SECONDS", 3600)
	if err != nil {
		return nil, err
	}
	if minTTL > maxTTL {
		return nil, fmt.Errorf("DYNAMIC_TTL_MIN_SECONDS > DYNAMIC_TTL_MAX_SECONDS")
	}
	pct, err := strconv.ParseFloat(env("SIGMA_REF_PERCENTILE", "90"), 64)
	if err != nil || pct <= 0 || pct > 100 {
		return nil, fmt.Errorf("SIGMA_REF_PERCENTILE tidak valid")
	}
	ref, err := store.SigmaRef(ctx, analysisCandles, pct/100)
	if err != nil {
		return nil, fmt.Errorf("hitung sigma_ref: %w", err)
	}
	// Dicatat di log supaya nilai parameter bisa ditulis di Metodologi.
	slog.Info("dynamic TTL", "ttl_min", minTTL, "ttl_max", maxTTL, "percentile", pct, "sigma_ref", ref)
	return &DynamicTTL{min: minTTL, max: maxTTL, ref: ref}, nil
}

func (d *DynamicTTL) TTL(symbol string, vol float64) time.Duration {
	return dynamicTTL(vol, d.ref[symbol], d.min, d.max)
}

func dynamicTTL(vol, ref float64, minTTL, maxTTL time.Duration) time.Duration {
	if ref <= 0 {
		return minTTL // σ_ref tidak diketahui: pilih yang paling aman untuk freshness
	}
	norm := math.Min(math.Max(vol/ref, 0), 1)
	ttl := maxTTL - time.Duration(norm*float64(maxTTL-minTTL))
	return ttl.Round(time.Second)
}

// percentile dengan interpolasi linear; v harus sudah terurut naik.
func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	pos := p * float64(len(v)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return v[lo] + (v[hi]-v[lo])*(pos-float64(lo))
}

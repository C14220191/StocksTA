package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// CachedSummary adalah hasil AI inference yang disimpan di cache. Seperti Cortex (Ruan et al., 2026),
// setiap entri membawa metadata performanya sendiri: token, biaya, harga, dan waktu generate.
type CachedSummary struct {
	SummaryID         int64
	InstrumentID      int
	Content           string
	Signal            string
	Model             string
	PromptVersion     string
	Origin            string    // ondemand | pregen (Lapis 2 / Lapis 1)
	SourceDataTS      time.Time // ts candle terakhir yang dipakai saat summary dibuat
	GeneratedAt       time.Time
	PriceAtGeneration float64  // P_gen: harga terkini yang dilihat AI (dasar event invalidation)
	State             string   // S_gen: vektor kondisi indikator saat generate
	Support           *float64 // level dari AI
	Resistance        *float64 // level dari AI
	TokensIn          int
	TokensOut         int
	CostUSD           float64
	Method            string          // debate | mtf | grounded
	Confidence        *int            // 0-100 dari AI
	Detail            json.RawMessage `json:",omitempty"` // isi terstruktur per metode
}

// Invalidation menjelaskan kenapa entry cache dibuang (dicatat ke cache_events).
type Invalidation struct {
	Reason       string  // price_move | state_change | manual
	ChangePct    float64 // drift harga bertanda, (P / P_gen - 1) x 100
	StateFrom    string
	StateTo      string
	SummaryID    int64
	InstrumentID int
	Method       string
	CrossedAt    *time.Time // t_crossing; nil kalau tidak diketahui (mis. pemeriksaan saat request)
}

// CachePolicy adalah satu-satunya titik yang berbeda antar kondisi eksperimen.
// Semua kondisi (no_cache, static_ttl, dynamic_ttl, event_only, hybrid)
// mengimplementasikan interface ini, sehingga handler dan alur data sama persis.
type CachePolicy interface {
	// Name dipakai sebagai experiment_condition di inference_log.
	Name() string
	// Threshold: ambang event invalidation θ (persen), nil kalau kondisi ini tidak memakainya.
	Threshold() *float64
	// Satu entry per (symbol, metode analisis).
	// Get mengembalikan summary kalau HIT. Kalau entry ada tapi tidak lolos pemeriksaan
	// (drift harga >= θ atau S berubah), entry dibuang, summary nil dan inv terisi.
	Get(ctx context.Context, symbol, method string, price float64, state string) (s *CachedSummary, inv *Invalidation)
	// Set mengembalikan TTL yang diterapkan (detik), nil kalau tidak ada TTL.
	Set(ctx context.Context, symbol, method string, s CachedSummary, volatility float64) *int
	Ping(ctx context.Context) error
	// Inspect membaca entry tanpa efek samping (halaman admin). ttl < 0 = tanpa kedaluwarsa.
	Inspect(ctx context.Context, symbol, method string) (s *CachedSummary, ttl time.Duration, err error)
	// Invalidate membuang entry secara manual; true kalau memang ada yang dibuang.
	Invalidate(ctx context.Context, symbol, method string) (bool, error)
}

// policyConfig: kondisi eksperimen satu instance.
type policyConfig struct {
	mode       string
	threshold  float64 // θ; 0 = tanpa event invalidation
	checkState bool    // pemeriksaan ketiga (S berubah)
	pregen     bool
}

// instanceKey membedakan entri Redis antar instance yang berjalan paralel (live paralel),
// mis. "hybrid-2-pg" = hybrid, θ 2%, dengan pre-generation.
func (c policyConfig) instanceKey() string {
	k := c.mode
	if c.threshold > 0 {
		k += "-" + strconv.FormatFloat(c.threshold, 'f', -1, 64)
	}
	if c.pregen {
		k += "-pg"
	}
	return k
}

func loadPolicyConfig() (policyConfig, error) {
	c := policyConfig{mode: env("CACHE_MODE", "no_cache"), pregen: env("PREGEN", "off") == "on"}
	switch c.mode {
	case "no_cache", "static_ttl", "dynamic_ttl", "event_only", "hybrid":
	default:
		return c, fmt.Errorf("CACHE_MODE %q tidak dikenal", c.mode)
	}
	if c.mode == "event_only" || c.mode == "hybrid" {
		th, err := strconv.ParseFloat(env("INVALIDATION_THRESHOLD_PCT", "2"), 64)
		if err != nil || th <= 0 {
			return c, fmt.Errorf("INVALIDATION_THRESHOLD_PCT tidak valid")
		}
		c.threshold = th
		// Bab 3.3.3: pada kondisi yang hanya menguji TTL, pemeriksaan harga dan S dinonaktifkan.
		c.checkState = env("INVALIDATE_ON_STATE", "on") == "on"
	}
	if c.pregen && c.mode == "no_cache" {
		return c, fmt.Errorf("PREGEN=on butuh CACHE_MODE dengan cache (bukan no_cache)")
	}
	return c, nil
}

// NewPolicy sengaja gagal untuk mode yang tidak dikenal,
// supaya eksperimen tidak diam-diam berjalan dengan kondisi yang salah.
func NewPolicy(ctx context.Context, cfg policyConfig, store *Store) (CachePolicy, error) {
	if cfg.mode == "no_cache" {
		return NoCache{}, nil
	}

	base, err := envSeconds("TTL_BASE_SECONDS", envInt("STATIC_TTL_SECONDS", 3600))
	if err != nil {
		return nil, err
	}
	p := &RedisPolicy{
		name: cfg.mode, prefix: cfg.instanceKey(), version: env("PROMPT_VERSION", promptVersion),
		threshold: cfg.threshold, checkState: cfg.checkState,
	}
	switch cfg.mode {
	case "static_ttl":
		// TTL statis (pembanding): TTL = TTL_base untuk semua instrument.
		p.ttl = func(string, float64) time.Duration { return base }
	case "dynamic_ttl", "hybrid":
		dt, err := newDynamicTTL(ctx, store, base)
		if err != nil {
			return nil, err
		}
		p.ttl, p.dyn = dt.TTL, dt
	case "event_only":
		// Tanpa kedaluwarsa berbasis waktu; batas atas hanya pengaman supaya Redis tidak menumpuk entry.
		sec, err := envSeconds("EVENT_MAX_TTL_SECONDS", 86400)
		if err != nil {
			return nil, err
		}
		p.ttl = func(string, float64) time.Duration { return sec }
	}

	rdb, err := newRedis()
	if err != nil {
		return nil, err
	}
	p.rdb = rdb
	return p, nil
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(env(key, "")); err == nil {
		return v
	}
	return def
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

func (NoCache) Name() string                                                     { return "no_cache" }
func (NoCache) Threshold() *float64                                              { return nil }
func (NoCache) Ping(context.Context) error                                       { return nil }
func (NoCache) Set(context.Context, string, string, CachedSummary, float64) *int { return nil }
func (NoCache) Inspect(context.Context, string, string) (*CachedSummary, time.Duration, error) {
	return nil, 0, nil
}
func (NoCache) Invalidate(context.Context, string, string) (bool, error) { return false, nil }
func (NoCache) Get(context.Context, string, string, float64, string) (*CachedSummary, *Invalidation) {
	return nil, nil
}

// RedisPolicy dipakai oleh empat kondisi cache. Perbedaannya hanya di fungsi TTL dan pemeriksaan:
//
//	static_ttl  : TTL = TTL_base,  tanpa pemeriksaan harga / S
//	dynamic_ttl : TTL adaptif,     tanpa pemeriksaan harga / S
//	event_only  : TTL pengaman,    drift harga >= θ atau S berubah -> dibuang
//	hybrid      : TTL adaptif,     drift harga >= θ atau S berubah -> dibuang
//
// Pemeriksaan harga/S dijalankan di dua tempat: proaktif oleh watcher di setiap tick (L_inv),
// dan saat Get sebagai pengaman (mis. saham yang harganya hanya berubah per jam).
type RedisPolicy struct {
	name       string
	prefix     string // instanceKey, supaya instance paralel tidak berbagi entri
	version    string // prompt_version, bagian dari key supaya summary prompt lama tidak terpakai
	rdb        *redis.Client
	ttl        func(symbol string, volatility float64) time.Duration
	dyn        *DynamicTTL // nil kalau kondisi tidak memakai TTL dinamis (admin: status σ_ref)
	threshold  float64     // persen; 0 = tanpa event invalidation
	checkState bool
	watcher    *Watcher // nil kalau kondisi tanpa event invalidation
}

func (p *RedisPolicy) Name() string { return p.name }

func (p *RedisPolicy) Threshold() *float64 {
	if p.threshold <= 0 {
		return nil
	}
	return &p.threshold
}

func (p *RedisPolicy) Ping(ctx context.Context) error { return p.rdb.Ping(ctx).Err() }

// key: satu entry per (instance, prompt_version, symbol, metode), mis.
// ta:summary:hybrid-2-pg:hermes-v5:BTCUSDT:debate
func (p *RedisPolicy) key(symbol, method string) string {
	return "ta:summary:" + p.prefix + ":" + p.version + ":" + symbol + ":" + method
}

// stale menilai pemeriksaan kedua dan ketiga (Gambar 3.4). nil = masih valid.
func (p *RedisPolicy) stale(s *CachedSummary, price float64, state string) *Invalidation {
	if p.threshold <= 0 {
		return nil
	}
	if dev, ok := priceDeviationPct(s.PriceAtGeneration, price); ok && math.Abs(dev) >= p.threshold {
		return &Invalidation{Reason: "price_move", ChangePct: dev, SummaryID: s.SummaryID,
			InstrumentID: s.InstrumentID, Method: s.Method}
	}
	if p.checkState && s.State != "" && state != "" && s.State != state {
		dev, _ := priceDeviationPct(s.PriceAtGeneration, price)
		return &Invalidation{Reason: "state_change", ChangePct: dev, StateFrom: s.State, StateTo: state,
			SummaryID: s.SummaryID, InstrumentID: s.InstrumentID, Method: s.Method}
	}
	return nil
}

func (p *RedisPolicy) Get(ctx context.Context, symbol, method string, price float64, state string) (*CachedSummary, *Invalidation) {
	b, err := p.rdb.Get(ctx, p.key(symbol, method)).Bytes()
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
	if inv := p.stale(&s, price, state); inv != nil {
		inv.Method = method
		// DEL mengembalikan 1 hanya untuk pemanggil yang benar-benar membuang entry (watcher atau
		// request lain bisa lebih dulu), jadi event yang sama tidak tercatat dua kali.
		if removed, _ := p.delIf(ctx, symbol, method, s.SummaryID); removed {
			if p.watcher != nil {
				p.watcher.Untrack(symbol, method)
			}
			return nil, inv
		}
		return nil, nil
	}
	return &s, nil
}

func (p *RedisPolicy) del(ctx context.Context, symbol, method string) (bool, error) {
	n, err := p.rdb.Del(ctx, p.key(symbol, method)).Result()
	if err != nil {
		slog.Error("redis del gagal", "err", err)
	}
	if n == 1 && p.watcher != nil {
		p.watcher.Untrack(symbol, method)
	}
	return n == 1, err
}

func (p *RedisPolicy) Inspect(ctx context.Context, symbol, method string) (*CachedSummary, time.Duration, error) {
	pipe := p.rdb.Pipeline()
	get := pipe.Get(ctx, p.key(symbol, method))
	ttl := pipe.PTTL(ctx, p.key(symbol, method))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, 0, err
	}
	b, err := get.Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var s CachedSummary
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, 0, err
	}
	return &s, ttl.Val(), nil
}

func (p *RedisPolicy) Invalidate(ctx context.Context, symbol, method string) (bool, error) {
	return p.del(ctx, symbol, method)
}

func (p *RedisPolicy) Set(ctx context.Context, symbol, method string, s CachedSummary, vol float64) *int {
	b, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	ttl := p.ttl(symbol, vol)
	if err := p.rdb.Set(ctx, p.key(symbol, method), b, ttl).Err(); err != nil {
		slog.Error("redis set gagal", "err", err)
		return nil
	}
	if p.watcher != nil {
		p.watcher.Track(symbol, method, s, time.Now().Add(ttl))
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

// DynamicTTL (Bab 2.5): TTL = clamp(TTL_base / (1 + k * σ / σ_ref), TTL_min, TTL_max).
// σ = realized volatility 14 candle 1h terakhir (sama dengan realizedVolatility di handler),
// σ_ref = volatilitas acuan per instrument dari data historis 1 tahun (default: rata-rata σ
// rolling window 14). Volatilitas tinggi -> TTL pendek, pasar tenang -> TTL mendekati TTL_base.
type DynamicTTL struct {
	base, min, max time.Duration
	k              float64
	ref            map[string]float64 // symbol -> σ_ref
}

func newDynamicTTL(ctx context.Context, store *Store, base time.Duration) (*DynamicTTL, error) {
	minTTL, err := envSeconds("DYNAMIC_TTL_MIN_SECONDS", 300)
	if err != nil {
		return nil, err
	}
	maxTTL, err := envSeconds("DYNAMIC_TTL_MAX_SECONDS", int(base.Seconds()))
	if err != nil {
		return nil, err
	}
	if minTTL > maxTTL {
		return nil, fmt.Errorf("DYNAMIC_TTL_MIN_SECONDS > DYNAMIC_TTL_MAX_SECONDS")
	}
	k, err := strconv.ParseFloat(env("DYNAMIC_TTL_K", "1"), 64)
	if err != nil || k < 0 {
		return nil, fmt.Errorf("DYNAMIC_TTL_K tidak valid")
	}
	stat := env("SIGMA_REF_STAT", "mean")
	ref, err := store.SigmaRef(ctx, analysisCandles, stat)
	if err != nil {
		return nil, fmt.Errorf("hitung sigma_ref: %w", err)
	}
	// Dicatat di log supaya nilai parameter bisa ditulis di Metodologi (Tabel parameter).
	slog.Info("dynamic TTL", "ttl_base", base, "k", k, "ttl_min", minTTL, "ttl_max", maxTTL,
		"sigma_ref_stat", stat, "sigma_ref", ref)
	return &DynamicTTL{base: base, min: minTTL, max: maxTTL, k: k, ref: ref}, nil
}

func (d *DynamicTTL) TTL(symbol string, vol float64) time.Duration {
	return dynamicTTL(vol, d.ref[symbol], d.base, d.k, d.min, d.max)
}

func dynamicTTL(vol, ref float64, base time.Duration, k float64, minTTL, maxTTL time.Duration) time.Duration {
	if ref <= 0 {
		return minTTL // σ_ref tidak diketahui: pilih yang paling aman untuk freshness
	}
	ttl := time.Duration(float64(base) / (1 + k*math.Max(vol, 0)/ref))
	return min(max(ttl, minTTL), maxTTL).Round(time.Second)
}

// sigmaRefStat: "mean" atau persentil "pNN" (mis. p90) dari kumpulan σ yang sudah terurut naik.
func sigmaRefStat(sorted []float64, stat string) (float64, error) {
	if len(sorted) == 0 {
		return 0, nil
	}
	if stat == "mean" {
		var s float64
		for _, v := range sorted {
			s += v
		}
		return s / float64(len(sorted)), nil
	}
	if strings.HasPrefix(stat, "p") {
		if q, err := strconv.ParseFloat(stat[1:], 64); err == nil && q > 0 && q <= 100 {
			return percentile(sorted, q/100), nil
		}
	}
	return 0, fmt.Errorf("SIGMA_REF_STAT %q tidak valid (mean atau pNN)", stat)
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

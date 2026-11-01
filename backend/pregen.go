package main

import (
	"context"
	"log/slog"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Pre-generation terjadwal (Lapis 1, Bab 3.3.4). Popularitas instrument dihitung dari access
// counter di Redis; setiap interval Δ, top-N instrument yang entrinya kosong, tidak valid, atau
// akan kedaluwarsa sebelum siklus berikutnya di-generate ulang (refresh-ahead).
//
// Entri hasil pre-generation ditandai origin = pregen ("used = false"). Entri dianggap terpakai
// saat pertama kali disajikan sebagai HIT (inference_log.summary_id). Entri pregen yang tidak
// pernah disajikan sampai kedaluwarsa atau digantikan dihitung sebagai wasted inference:
//
//	W = N_pre_unused / N_pre x 100%
type PregenConfig struct {
	Interval  time.Duration `json:"interval_ns"`  // Δ
	TopN      int           `json:"top_n"`        // N
	Window    int           `json:"window_hours"` // jam ke belakang untuk menghitung popularitas
	MinAccess int           `json:"min_access"`   // akses minimal supaya dianggap populer
}

func loadPregenConfig() (*PregenConfig, error) {
	interval, err := envSeconds("PREGEN_INTERVAL_SECONDS", 900)
	if err != nil {
		return nil, err
	}
	return &PregenConfig{
		Interval:  interval,
		TopN:      max(envInt("PREGEN_TOP_N", 3), 1),
		Window:    max(envInt("PREGEN_WINDOW_HOURS", 24), 1),
		MinAccess: max(envInt("PREGEN_MIN_ACCESS", 1), 1),
	}, nil
}

// accessKey: satu sorted set per jam per instance, mis. ta:access:hybrid-2-pg:2026110109.
func (a *App) accessKey(t time.Time) string {
	return "ta:access:" + a.cfg.instanceKey() + ":" + t.UTC().Format("2006010215")
}

// recordAccess: access counter (INCR per instrument) untuk menentukan popularitas.
// Hanya untuk kondisi dengan cache; kegagalan Redis tidak menggagalkan request.
func (a *App) recordAccess(ctx context.Context, symbol string) {
	if a.rdb == nil || a.policy.Name() == "no_cache" {
		return
	}
	key := a.accessKey(time.Now())
	pipe := a.rdb.Pipeline()
	pipe.ZIncrBy(ctx, key, 1, symbol)
	pipe.Expire(ctx, key, 49*time.Hour)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("gagal mencatat akses", "err", err)
	}
}

type popularity struct {
	Symbol string  `json:"symbol"`
	Hits   float64 `json:"access"`
}

// popular: jumlah akses per instrument dalam window jam terakhir, terurut menurun.
func (a *App) popular(ctx context.Context, windowHours int) ([]popularity, error) {
	if a.rdb == nil {
		return nil, nil
	}
	now := time.Now()
	pipe := a.rdb.Pipeline()
	cmds := make([]*redis.ZSliceCmd, windowHours)
	for i := range windowHours {
		cmds[i] = pipe.ZRangeWithScores(ctx, a.accessKey(now.Add(-time.Duration(i)*time.Hour)), 0, -1)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, err
	}
	sum := map[string]float64{}
	for _, c := range cmds {
		for _, z := range c.Val() {
			sum[z.Member.(string)] += z.Score
		}
	}
	out := make([]popularity, 0, len(sum))
	for s, n := range sum {
		out = append(out, popularity{s, n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Hits != out[j].Hits {
			return out[i].Hits > out[j].Hits
		}
		return out[i].Symbol < out[j].Symbol
	})
	return out, nil
}

// runPregen: goroutine penjadwal pre-generation.
func (a *App) runPregen(ctx context.Context) {
	cfg := a.pregen
	slog.Info("pre-generation aktif", "interval", cfg.Interval, "top_n", cfg.TopN,
		"window_jam", cfg.Window, "min_akses", cfg.MinAccess)
	t := time.NewTicker(cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		pop, err := a.popular(ctx, cfg.Window)
		if err != nil {
			slog.Error("pre-generation: gagal membaca popularitas", "err", err)
			continue
		}
		for i, p := range pop {
			if i >= cfg.TopN || p.Hits < float64(cfg.MinAccess) {
				break
			}
			a.pregenerateOne(ctx, p.Symbol)
		}
	}
}

// pregenerateOne membuat ulang analisis satu instrument kalau entrinya tidak akan bertahan
// sampai siklus berikutnya. Satu baris inference_log dengan cache_status PREGEN dan
// trigger scheduled dicatat untuk setiap generate (token dan biayanya masuk N_gen).
func (a *App) pregenerateOne(ctx context.Context, symbol string) {
	start := time.Now()
	ac, err := a.loadAnalysisContext(ctx, symbol)
	if err != nil {
		slog.Warn("pre-generation: data tidak tersedia", "symbol", symbol, "err", err)
		return
	}
	s, ttl, err := a.policy.Inspect(ctx, symbol)
	if err != nil {
		slog.Error("pre-generation: inspect gagal", "symbol", symbol, "err", err)
		return
	}
	if s != nil && (ttl < 0 || ttl >= a.pregen.Interval) {
		// Entri bertahan sampai siklus berikutnya; tetap jalankan pemeriksaan harga dan S.
		cached, inv := a.policy.Get(ctx, symbol, ac.quote.Price, ac.state)
		if cached != nil {
			return
		}
		if inv != nil {
			a.logInvalidationFor(ctx, ac.instID, inv, "scheduler")
		}
	}

	logEntry := a.baseLog(ac, "scheduled")
	out, led, err := a.produce(ctx, genInput{
		symbol: symbol, instID: ac.instID, candles: ac.candles, quote: ac.quote,
		vol: ac.vol, state: ac.state, origin: "pregen", force: true,
	})
	if err != nil {
		logEntry.CacheStatus = "ERROR"
		logEntry.LatencyMS = int(time.Since(start).Milliseconds())
		logEntry.Error = truncate("pre-generation: "+err.Error(), 500)
		a.logInference(ctx, logEntry)
		return
	}
	if !led || out.fromCache {
		return // request pengguna sedang membuat summary yang sama (singleflight)
	}
	logEntry.TTLSeconds = out.ttl
	a.finishLog(&logEntry, "PREGEN", time.Since(start), out.summary, ac, out.res)
	a.logInference(ctx, logEntry)
	slog.Info("pre-generation", "symbol", symbol, "summary_id", out.summary.SummaryID,
		"ttl", strconv.Itoa(derefInt(out.ttl))+"s")
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

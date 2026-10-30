package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Quote adalah harga terkini satu instrument beserta asalnya.
type Quote struct {
	Price  float64
	TS     time.Time
	Source string // "live" = price feed Binance, "db" = close candle 1h terbaru di database
}

// PriceFeed menyimpan harga terakhir kripto di memori dengan polling REST ticker Binance.
// Dipakai untuk (1) event-driven invalidation, (2) harga terkini di prompt AI, dan
// (3) metrik freshness berbasis harga. Polling (bukan WebSocket) dipilih karena memakai host
// yang sama dengan ingestion (data-api.binance.vision tidak diblokir) dan resolusi beberapa detik
// sudah jauh di bawah ambang invalidation terkecil (1%).
type PriceFeed struct {
	url      string
	symbols  []string
	interval time.Duration
	maxAge   time.Duration
	client   *http.Client

	mu     sync.RWMutex
	prices map[string]Quote
	lastOK time.Time
	errs   int
}

func NewPriceFeed(symbols []string) *PriceFeed {
	interval, _ := strconv.Atoi(env("PRICE_POLL_SECONDS", "2"))
	maxAge, _ := strconv.Atoi(env("PRICE_MAX_AGE_SECONDS", "30"))
	return &PriceFeed{
		url:      tickerURL(env("BINANCE_URL", "https://api.binance.com/api/v3/klines")),
		symbols:  symbols,
		interval: time.Duration(max(interval, 1)) * time.Second,
		maxAge:   time.Duration(max(maxAge, 1)) * time.Second,
		client:   &http.Client{Timeout: 5 * time.Second},
		prices:   map[string]Quote{},
	}
}

// tickerURL menurunkan endpoint ticker dari BINANCE_URL (klines) supaya host-nya sama.
func tickerURL(klines string) string {
	if i := strings.Index(klines, "/api/v3/"); i >= 0 {
		return klines[:i] + "/api/v3/ticker/price"
	}
	return "https://api.binance.com/api/v3/ticker/price"
}

// Run melakukan polling sampai ctx selesai. Dijalankan sebagai goroutine.
func (f *PriceFeed) Run(ctx context.Context) {
	if len(f.symbols) == 0 {
		return
	}
	t := time.NewTicker(f.interval)
	defer t.Stop()
	for {
		if err := f.poll(ctx); err != nil {
			f.mu.Lock()
			f.errs++
			n := f.errs
			f.mu.Unlock()
			if n == 1 || n%30 == 0 { // jangan banjiri log saat koneksi putus lama
				slog.Warn("price feed gagal", "err", err, "berturut", n)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (f *PriceFeed) poll(ctx context.Context) error {
	b, _ := json.Marshal(f.symbols)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url+"?symbols="+url.QueryEscape(string(b)), nil)
	if err != nil {
		return err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ticker status %d", resp.StatusCode)
	}
	var rows []struct {
		Symbol string `json:"symbol"`
		Price  string `json:"price"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return err
	}
	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range rows {
		if p, err := strconv.ParseFloat(r.Price, 64); err == nil && p > 0 {
			f.prices[r.Symbol] = Quote{Price: p, TS: now, Source: "live"}
		}
	}
	f.lastOK, f.errs = now, 0
	return nil
}

// Live mengembalikan harga feed kalau ada dan belum lebih tua dari PRICE_MAX_AGE_SECONDS.
func (f *PriceFeed) Live(symbol string) (Quote, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	q, ok := f.prices[symbol]
	if !ok || time.Since(q.TS) > f.maxAge {
		return Quote{}, false
	}
	return q, true
}

// Status untuk /health.
func (f *PriceFeed) Status() map[string]any {
	f.mu.RLock()
	defer f.mu.RUnlock()
	st := map[string]any{"symbols": f.symbols, "consecutive_errors": f.errs}
	if !f.lastOK.IsZero() {
		st["last_ok_seconds_ago"] = int(time.Since(f.lastOK).Seconds())
	}
	return st
}

// CurrentQuote: harga live kalau tersedia, selain itu close candle 1h terbaru (termasuk yang masih
// berjalan) dari database. Saham IDX selalu "db" karena tidak ada feed gratis.
func (a *App) CurrentQuote(ctx context.Context, symbol string, instID int) (Quote, error) {
	if a.prices != nil {
		if q, ok := a.prices.Live(symbol); ok {
			return q, nil
		}
	}
	return a.store.LatestPrice(ctx, instID)
}

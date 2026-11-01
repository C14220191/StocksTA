package main

import (
	"context"
	"encoding/json"
	"errors"
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

// PriceFeed menyimpan harga terakhir instrument Binance (spot dan USDⓈ-M futures) di memori
// dengan polling REST ticker. Dipakai untuk (1) event-driven invalidation, (2) harga terkini di
// prompt AI, dan (3) metrik freshness berbasis harga. Polling (bukan WebSocket) dipilih karena
// memakai host yang sama dengan ingestion (data-api.binance.vision tidak diblokir) dan resolusi
// beberapa detik sudah jauh di bawah ambang invalidation terkecil (1%).
type PriceFeed struct {
	spotURL    string
	futuresURL string
	spot       []string // source = binance
	futures    []string // source = binance_futures
	interval   time.Duration
	maxAge     time.Duration
	client     *http.Client

	mu     sync.RWMutex
	prices map[string]Quote
	lastOK time.Time
	errs   int
}

func NewPriceFeed(spot, futures []string) *PriceFeed {
	interval, _ := strconv.Atoi(env("PRICE_POLL_SECONDS", "2"))
	maxAge, _ := strconv.Atoi(env("PRICE_MAX_AGE_SECONDS", "30"))
	return &PriceFeed{
		spotURL: tickerURL(env("BINANCE_URL", "https://api.binance.com/api/v3/klines"),
			"https://api.binance.com/api/v3/ticker/price"),
		futuresURL: tickerURL(env("BINANCE_FUTURES_URL", "https://fapi.binance.com/fapi/v1/klines"),
			"https://fapi.binance.com/fapi/v1/ticker/price"),
		spot:     spot,
		futures:  futures,
		interval: time.Duration(max(interval, 1)) * time.Second,
		maxAge:   time.Duration(max(maxAge, 1)) * time.Second,
		client:   &http.Client{Timeout: 5 * time.Second},
		prices:   map[string]Quote{},
	}
}

// tickerURL menurunkan endpoint ticker dari URL klines (spot /api/v3/ atau futures /fapi/v1/)
// supaya host-nya sama dengan ingestion.
func tickerURL(klines, def string) string {
	for _, p := range []string{"/api/v3/", "/fapi/v1/"} {
		if i := strings.Index(klines, p); i >= 0 {
			return klines[:i] + p + "ticker/price"
		}
	}
	return def
}

// Run melakukan polling sampai ctx selesai. Dijalankan sebagai goroutine.
func (f *PriceFeed) Run(ctx context.Context) {
	if len(f.spot)+len(f.futures) == 0 {
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

type tickerRow struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

func (f *PriceFeed) poll(ctx context.Context) error {
	var rows []tickerRow
	var errs []error
	if len(f.spot) > 0 {
		b, _ := json.Marshal(f.spot)
		var r []tickerRow
		if err := f.get(ctx, f.spotURL+"?symbols="+url.QueryEscape(string(b)), &r); err != nil {
			errs = append(errs, fmt.Errorf("spot: %w", err))
		} else {
			rows = append(rows, r...)
		}
	}
	for _, sym := range f.futures { // ticker futures tidak menerima parameter symbols
		var r tickerRow
		if err := f.get(ctx, f.futuresURL+"?symbol="+url.QueryEscape(sym), &r); err != nil {
			errs = append(errs, fmt.Errorf("futures %s: %w", sym, err))
		} else {
			rows = append(rows, r)
		}
	}

	now := time.Now()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range rows {
		if p, err := strconv.ParseFloat(r.Price, 64); err == nil && p > 0 {
			f.prices[r.Symbol] = Quote{Price: p, TS: now, Source: "live"}
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	f.lastOK, f.errs = now, 0
	return nil
}

func (f *PriceFeed) get(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
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
	return json.NewDecoder(resp.Body).Decode(out)
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
	st := map[string]any{"spot": f.spot, "futures": f.futures, "consecutive_errors": f.errs}
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

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Quote adalah harga terkini satu instrument beserta asalnya.
type Quote struct {
	Price      float64
	TS         time.Time // waktu harga (live: waktu transaksi di bursa, dikonversi ke jam lokal; db: fetched_at)
	ReceivedAt time.Time // kapan backend menerima harga ini (untuk menilai basi)
	Source     string    // "live" = WebSocket Binance, "db" = close candle 1h terbaru di database
}

// PriceFeed menyimpan harga terakhir instrument Binance di memori dari WebSocket stream aggTrade
// (setiap transaksi, dengan waktu transaksi dari bursa). Satu koneksi per market:
//   - spot    (source binance)         : wss://data-stream.binance.vision (khusus market data)
//   - futures (source binance_futures) : wss://fstream.binance.com/market (USDⓈ-M, mis. XAUUSDT).
//     aggTrade futures hanya dikirim lewat path /market; di path lama koneksi terbuka tapi kosong.
//
// Dipakai untuk (1) event-driven invalidation lewat watcher (onTick), (2) harga terkini di prompt,
// dan (3) metrik freshness berbasis harga. Koneksi yang putus disambung ulang dengan backoff.
//
// Harga suatu simbol dianggap masih berlaku selama koneksi market-nya sehat (ada pesan dalam
// PRICE_MAX_AGE_SECONDS), walaupun simbol itu sendiri jarang bertransaksi (mis. tembaga, platinum):
// harga transaksi terakhir memang harga terkini. Batasnya PRICE_IDLE_MAX_SECONDS tanpa transaksi.
// Saat start, harga awal diisi dari REST ticker supaya request pertama tidak memakai harga DB.
type PriceFeed struct {
	markets  []*marketStream
	bySymbol map[string]*marketStream
	maxAge   time.Duration                                    // koneksi dianggap mati kalau tidak ada pesan selama ini
	idleMax  time.Duration                                    // harga simbol yang tidak bertransaksi selama ini tidak dipakai lagi
	onTick   func(symbol string, price float64, ts time.Time) // dipasang sebelum Run (watcher)

	// clockOffset = jam server Binance - jam lokal (ns). Waktu transaksi dari bursa dikonversi ke
	// jam lokal dengan offset ini, supaya t_crossing dan L_inv tidak terpengaruh jam PC yang meleset.
	clockOffset atomic.Int64
	timeURL     string

	mu     sync.RWMutex
	prices map[string]Quote
}

type marketStream struct {
	name    string // spot | futures
	url     string // WebSocket
	restURL string // REST ticker/price untuk harga awal
	symbols []string

	mu         sync.Mutex
	connected  bool
	since      time.Time
	lastMsg    time.Time
	reconnects int
	lastErr    string
	messages   int64
}

func NewPriceFeed(spot, futures []string) *PriceFeed {
	maxAge, _ := strconv.Atoi(env("PRICE_MAX_AGE_SECONDS", "30"))
	idle, _ := strconv.Atoi(env("PRICE_IDLE_MAX_SECONDS", "900"))
	f := &PriceFeed{
		maxAge:   time.Duration(max(maxAge, 1)) * time.Second,
		idleMax:  time.Duration(max(idle, maxAge, 1)) * time.Second,
		prices:   map[string]Quote{},
		bySymbol: map[string]*marketStream{},
		timeURL:  env("BINANCE_TIME_URL", "https://data-api.binance.vision/api/v3/time"),
	}
	if len(spot) == 0 {
		f.timeURL = env("BINANCE_TIME_URL", "https://fapi.binance.com/fapi/v1/time")
	}
	if len(spot) > 0 {
		f.markets = append(f.markets, &marketStream{name: "spot", symbols: spot,
			url:     strings.TrimRight(env("BINANCE_WS_URL", "wss://data-stream.binance.vision"), "/"),
			restURL: tickerURL(env("BINANCE_URL", "https://data-api.binance.vision/api/v3/klines"))})
	}
	if len(futures) > 0 {
		f.markets = append(f.markets, &marketStream{name: "futures", symbols: futures,
			url:     strings.TrimRight(env("BINANCE_FUTURES_WS_URL", "wss://fstream.binance.com/market"), "/"),
			restURL: tickerURL(env("BINANCE_FUTURES_URL", "https://fapi.binance.com/fapi/v1/klines"))})
	}
	for _, m := range f.markets {
		for _, s := range m.symbols {
			f.bySymbol[s] = m
		}
	}
	return f
}

// tickerURL: endpoint ticker/price di host yang sama dengan URL klines (ingestion).
func tickerURL(klines string) string {
	return strings.TrimSuffix(strings.TrimRight(klines, "/"), "klines") + "ticker/price"
}

// seed mengisi harga awal dari REST ticker (satu request per market).
func (f *PriceFeed) seed(ctx context.Context) {
	client := &http.Client{Timeout: 10 * time.Second}
	for _, m := range f.markets {
		u := m.restURL
		if m.name == "spot" { // spot menerima daftar simbol; futures tanpa parameter = semua simbol
			b, _ := json.Marshal(m.symbols)
			u += "?symbols=" + url.QueryEscape(string(b))
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		resp, err := client.Do(req)
		if err != nil {
			slog.Warn("harga awal gagal diambil", "market", m.name, "err", err)
			continue
		}
		var rows []struct {
			Symbol string `json:"symbol"`
			Price  string `json:"price"`
		}
		err = json.NewDecoder(resp.Body).Decode(&rows)
		resp.Body.Close()
		if err != nil {
			slog.Warn("harga awal tidak valid", "market", m.name, "err", err)
			continue
		}
		now := time.Now()
		n := 0
		f.mu.Lock()
		for _, r := range rows {
			if f.bySymbol[r.Symbol] != m {
				continue
			}
			if p, err := strconv.ParseFloat(r.Price, 64); err == nil && p > 0 {
				f.prices[r.Symbol] = Quote{Price: p, TS: now, ReceivedAt: now, Source: "live"}
				n++
			}
		}
		f.mu.Unlock()
		slog.Info("harga awal dari REST ticker", "market", m.name, "simbol", n)
	}
}

// Run membuka satu goroutine per market sampai ctx selesai.
func (f *PriceFeed) Run(ctx context.Context) {
	if len(f.markets) == 0 {
		return
	}
	f.syncClock(ctx) // sebelum stream, supaya tick pertama sudah memakai offset
	f.seed(ctx)
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				f.syncClock(ctx)
			}
		}
	}()
	for _, m := range f.markets {
		go f.stream(ctx, m)
	}
}

// syncClock mengukur offset jam lokal terhadap server Binance seperti NTP sederhana:
// offset = serverTime - (t_kirim + t_terima) / 2, median dari 5 sampel. Ketidakpastian ~RTT/2.
func (f *PriceFeed) syncClock(ctx context.Context) {
	client := &http.Client{Timeout: 5 * time.Second}
	var offs []time.Duration
	for range 5 {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.timeURL, nil)
		t0 := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		var body struct {
			ServerTime int64 `json:"serverTime"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		t1 := time.Now()
		if err != nil || body.ServerTime == 0 {
			continue
		}
		mid := t0.Add(t1.Sub(t0) / 2)
		offs = append(offs, time.UnixMilli(body.ServerTime).Sub(mid))
	}
	if len(offs) == 0 {
		slog.Warn("sinkronisasi jam Binance gagal; t_crossing memakai jam bursa apa adanya")
		return
	}
	sort.Slice(offs, func(i, j int) bool { return offs[i] < offs[j] })
	off := offs[len(offs)/2]
	f.clockOffset.Store(int64(off))
	slog.Info("offset jam Binance", "server_minus_lokal", off.Round(time.Millisecond), "sampel", len(offs))
}

// ClockOffset: jam server Binance - jam lokal.
func (f *PriceFeed) ClockOffset() time.Duration { return time.Duration(f.clockOffset.Load()) }

func (f *PriceFeed) stream(ctx context.Context, m *marketStream) {
	streams := make([]string, len(m.symbols))
	for i, s := range m.symbols {
		streams[i] = strings.ToLower(s) + "@aggTrade"
	}
	url := m.url + "/stream?streams=" + strings.Join(streams, "/")
	backoff := time.Second
	for ctx.Err() == nil {
		err := f.read(ctx, m, url)
		m.mu.Lock()
		m.connected = false
		m.reconnects++
		if err != nil {
			m.lastErr = err.Error()
		}
		n := m.reconnects
		m.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if n == 1 || n%10 == 0 {
			slog.Warn("price feed terputus, menyambung ulang", "market", m.name, "err", err, "ke", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// read membaca satu koneksi sampai putus. Binance memutus koneksi setiap 24 jam; ping/pong
// ditangani library selama Read berjalan.
func (f *PriceFeed) read(ctx context.Context, m *marketStream, url string) error {
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	c, _, err := websocket.Dial(dctx, url, nil)
	cancel()
	if err != nil {
		return err
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20)
	m.mu.Lock()
	m.connected, m.since, m.lastErr = true, time.Now(), ""
	m.mu.Unlock()

	var msg struct {
		Data struct {
			Symbol string `json:"s"`
			Price  string `json:"p"`
			Time   int64  `json:"T"` // waktu transaksi (ms, jam bursa)
		} `json:"data"`
	}
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return err
		}
		if json.Unmarshal(data, &msg) != nil || msg.Data.Symbol == "" {
			continue
		}
		p, err := strconv.ParseFloat(msg.Data.Price, 64)
		if err != nil || p <= 0 {
			continue
		}
		now := time.Now()
		ts := time.UnixMilli(msg.Data.Time).Add(-f.ClockOffset()) // waktu transaksi dalam jam lokal
		f.mu.Lock()
		f.prices[msg.Data.Symbol] = Quote{Price: p, TS: ts, ReceivedAt: now, Source: "live"}
		f.mu.Unlock()
		m.mu.Lock()
		m.messages++
		m.lastMsg = now
		m.mu.Unlock()
		if f.onTick != nil {
			f.onTick(msg.Data.Symbol, p, ts)
		}
	}
}

// Live mengembalikan harga feed kalau masih berlaku: diterima dalam PRICE_MAX_AGE_SECONDS, atau
// koneksi market-nya sehat dan simbol itu belum diam lebih dari PRICE_IDLE_MAX_SECONDS.
func (f *PriceFeed) Live(symbol string) (Quote, bool) {
	f.mu.RLock()
	q, ok := f.prices[symbol]
	f.mu.RUnlock()
	if !ok {
		return Quote{}, false
	}
	age := time.Since(q.ReceivedAt)
	if age <= f.maxAge {
		return q, true
	}
	if m := f.bySymbol[symbol]; m != nil && age <= f.idleMax {
		m.mu.Lock()
		healthy := m.connected && time.Since(m.lastMsg) <= f.maxAge
		m.mu.Unlock()
		if healthy {
			return q, true
		}
	}
	return Quote{}, false
}

// Ages: umur (detik) harga terakhir per simbol, untuk halaman admin API Health.
func (f *PriceFeed) Ages() map[string]float64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := map[string]float64{}
	for sym, q := range f.prices {
		out[sym] = time.Since(q.ReceivedAt).Seconds()
	}
	return out
}

type streamStatus struct {
	Market     string   `json:"market"`
	Symbols    []string `json:"symbols"`
	Connected  bool     `json:"connected"`
	UpSeconds  int      `json:"up_seconds"`
	Reconnects int      `json:"reconnects"`
	Messages   int64    `json:"messages"`
	LastMsgAgo *float64 `json:"last_message_seconds_ago"`
	LastError  string   `json:"last_error,omitempty"`
}

func (f *PriceFeed) Streams() []streamStatus {
	out := []streamStatus{}
	for _, m := range f.markets {
		m.mu.Lock()
		st := streamStatus{Market: m.name, Symbols: m.symbols, Connected: m.connected,
			Reconnects: m.reconnects, Messages: m.messages, LastError: m.lastErr}
		if m.connected {
			st.UpSeconds = int(time.Since(m.since).Seconds())
		}
		if !m.lastMsg.IsZero() {
			ago := time.Since(m.lastMsg).Seconds()
			st.LastMsgAgo = &ago
		}
		m.mu.Unlock()
		out = append(out, st)
	}
	return out
}

// Status untuk /health.
func (f *PriceFeed) Status() map[string]any {
	return map[string]any{"streams": f.Streams(), "clock_offset_ms": f.ClockOffset().Milliseconds()}
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

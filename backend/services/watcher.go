package services

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"ta-backend/models"
)

// Watcher menjalankan event-driven invalidation secara proaktif (Bab 3.3.3): setiap tick dari
// price feed dibandingkan dengan entri cache yang sedang hidup. Begitu drift harga >= θ atau
// vektor kondisi S berubah, entri dibuang saat itu juga dan dicatat dengan
//
//	t_crossing     = waktu transaksi (jam bursa) dari tick yang pertama kali melewati batas
//	L_inv          = waktu dicatat - t_crossing  (invalidation latency)
//
// Satu instrument punya satu entri per metode analisis; semuanya memakai aturan yang sama, jadi
// satu tick memeriksa semua metode dengan S yang dihitung sekali.
// Instrument tanpa tick (saham IDX, harga dari ingestion per jam) dan perubahan S karena candle
// baru tutup diperiksa oleh loop refresh tiap WATCHER_REFRESH_SECONDS.
type Watcher struct {
	svc    *AnalysisService
	policy *RedisPolicy

	// candles: candle 1h tertutup terakhir untuk model indikator (diganti di tes)
	candles func(ctx context.Context, instID int) ([]models.Candle, error)

	mu      sync.Mutex
	entries map[watchKey]*watchEntry
	models  map[string]*IndicatorModel // per symbol
}

type watchKey struct{ symbol, method string }

type watchEntry struct {
	summary   models.CachedSummary
	expiresAt time.Time
}

func NewWatcher(svc *AnalysisService, policy *RedisPolicy) *Watcher {
	w := &Watcher{svc: svc, policy: policy, entries: map[watchKey]*watchEntry{}, models: map[string]*IndicatorModel{}}
	w.candles = func(ctx context.Context, instID int) ([]models.Candle, error) {
		return svc.store.LatestCandles(ctx, instID, analysisTimeframe, stateCandles)
	}
	policy.watcher = w
	return w
}

func (w *Watcher) Track(symbol, method string, s models.CachedSummary, expiresAt time.Time) {
	w.mu.Lock()
	w.entries[watchKey{symbol, method}] = &watchEntry{summary: s, expiresAt: expiresAt}
	// Model indikator harus dibangun dari candle yang sama dengan saat summary dibuat (SourceDataTS =
	// candle tertutup terakhir). Run hanya me-refresh simbol yang punya entri hidup, jadi model simbol
	// yang entrinya sudah habis bisa tertinggal berjam-jam; S dari candle lama + harga sekarang membuat
	// entri baru langsung dibuang sebagai state_change. Model seperti itu dilepas dan dimuat ulang;
	// sampai siap, S tidak diperiksa (drift harga tetap diperiksa).
	reload := false
	if w.policy.checkState {
		if m := w.models[symbol]; m == nil || !m.LastClosedTS.Equal(s.SourceDataTS) {
			delete(w.models, symbol)
			reload = true
		}
	}
	w.mu.Unlock()
	if reload {
		go w.loadModel(context.Background(), symbol, s.InstrumentID)
	}
}

func (w *Watcher) Untrack(symbol, method string) {
	w.mu.Lock()
	delete(w.entries, watchKey{symbol, method})
	w.mu.Unlock()
}

// OnTick dipanggil price feed untuk setiap transaksi; harus cepat (O(1), tanpa I/O).
func (w *Watcher) OnTick(symbol string, price float64, ts time.Time) {
	w.check(symbol, price, ts)
}

func (w *Watcher) check(symbol string, price float64, crossedAt time.Time) {
	for _, inv := range w.due(symbol, price, crossedAt) {
		go w.invalidate(symbol, inv)
	}
}

// due: entri simbol ini yang harus dibuang pada harga ini (dan sudah dilepas dari pelacakan).
func (w *Watcher) due(symbol string, price float64, crossedAt time.Time) []*Invalidation {
	var fired []*Invalidation
	now := time.Now()
	w.mu.Lock()
	state, stateDone := "", false
	for _, method := range AnalysisMethods {
		k := watchKey{symbol, method}
		e := w.entries[k]
		if e == nil {
			continue
		}
		if now.After(e.expiresAt) { // sudah kedaluwarsa di Redis
			delete(w.entries, k)
			continue
		}
		if w.policy.checkState && !stateDone {
			state, stateDone = w.models[symbol].State(price), true // model nil -> "" -> S tidak diperiksa
		}
		if inv := w.policy.stale(&e.summary, price, state); inv != nil {
			delete(w.entries, k)
			inv.CrossedAt, inv.Method = &crossedAt, method
			fired = append(fired, inv)
		}
	}
	w.mu.Unlock()
	return fired
}

func (w *Watcher) invalidate(symbol string, inv *Invalidation) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if removed, err := w.policy.delIf(ctx, symbol, inv.Method, inv.SummaryID); err != nil || !removed {
		return // entri sudah diganti / dibuang pihak lain
	}
	w.svc.logInvalidation(ctx, inv, "watcher")
}

func (w *Watcher) loadModel(ctx context.Context, symbol string, instID int) *IndicatorModel {
	candles, err := w.candles(ctx, instID)
	if err != nil {
		slog.Warn("watcher: gagal memuat candle", "symbol", symbol, "err", err)
		return nil
	}
	m := newIndicatorModel(candles)
	w.mu.Lock()
	w.models[symbol] = m
	w.mu.Unlock()
	return m
}

// Run: refresh model indikator (candle baru tutup bisa mengubah S tanpa harga bergerak) dan
// periksa instrument yang harganya dari database.
func (w *Watcher) Run(ctx context.Context) {
	sec, _ := strconv.Atoi(env("WATCHER_REFRESH_SECONDS", "30"))
	t := time.NewTicker(time.Duration(max(sec, 5)) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		w.mu.Lock()
		tracked := map[string]int{} // symbol -> instrument id
		for k, e := range w.entries {
			tracked[k.symbol] = e.summary.InstrumentID
		}
		w.mu.Unlock()
		for sym, instID := range tracked {
			w.mu.Lock()
			old := w.models[sym]
			w.mu.Unlock()
			m := w.loadModel(ctx, sym, instID)
			q, err := w.svc.CurrentQuote(ctx, sym, instID)
			if err != nil || q.Price <= 0 {
				continue
			}
			switch {
			case m != nil && old != nil && m.LastClosedTS.After(old.LastClosedTS):
				// candle baru tutup: S bisa berubah tepat saat candle ditutup
				w.check(sym, q.Price, m.LastClosedTS.Add(candleDuration))
			case q.Source == "db":
				// tanpa tick: perubahan harga baru terlihat saat ingestion mengambil data
				w.check(sym, q.Price, q.TS)
			}
		}
	}
}

// delIf membuang entri hanya kalau masih berisi summary yang sama (atomik di Redis), supaya
// watcher tidak membuang ringkasan baru yang disimpan request lain sesaat sebelumnya.
var delIfScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if not v then return 0 end
local ok, s = pcall(cjson.decode, v)
if ok and tostring(s['SummaryID']) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0`)

func (p *RedisPolicy) delIf(ctx context.Context, symbol, method string, summaryID int64) (bool, error) {
	n, err := delIfScript.Run(ctx, p.rdb, []string{p.key(symbol, method)}, strconv.FormatInt(summaryID, 10)).Int()
	if err != nil {
		slog.Error("redis delIf gagal", "err", err)
	}
	return n == 1, err
}

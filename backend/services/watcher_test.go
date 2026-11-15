package services

import (
	"context"
	"testing"
	"time"

	"ta-backend/models"
)

// hourly: n candle 1h tertutup yang berakhir di last, close naik linear dari -> ke (dengan sedikit
// zig-zag supaya RSI punya gain dan loss).
func hourly(n int, last time.Time, from, to float64) []models.Candle {
	out := make([]models.Candle, n)
	for i := range out {
		c := from + (to-from)*float64(i)/float64(n-1)
		if i%2 == 1 {
			c -= 0.05
		}
		out[i] = models.Candle{TS: last.Add(-time.Duration(n-1-i) * time.Hour), Open: c, High: c + 0.2, Low: c - 0.2, Close: c, Volume: 1}
	}
	return out
}

// Regresi 15 Nov 2026 (server, hybrid θ1): 113 dari 113 invalidasi adalah state_change oleh watcher
// dengan umur median 0 menit, hampir semuanya neutral/pos -> oversold/neg. Penyebabnya: Track hanya
// memuat model indikator kalau belum ada, dan Run hanya me-refresh simbol yang punya entri hidup.
// Model simbol yang entrinya sudah habis tertinggal berjam-jam; entri baru lalu dibandingkan dengan S
// dari candle lama + harga sekarang dan langsung dibuang pada tick pertama.
func TestWatcherTrackReplacesStaleModel(t *testing.T) {
	end := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	fresh := hourly(stateCandles, end, 100, 112)                 // candle yang dipakai saat summary dibuat
	old := hourly(stateCandles, end.Add(-5*time.Hour), 100, 130) // model yang tertinggal 5 jam
	price := fresh[len(fresh)-1].Close
	sGen := newIndicatorModel(fresh).State(price)
	if sOld := newIndicatorModel(old).State(price); sGen == "" || sOld == sGen {
		t.Fatalf("fixture: S dari model lama (%q) harus berbeda dari S saat generate (%q)", sOld, sGen)
	}

	w := NewWatcher(nil, &RedisPolicy{threshold: 1, checkState: true})
	loaded := make(chan struct{}, 4)
	w.candles = func(context.Context, int) ([]models.Candle, error) {
		defer func() { loaded <- struct{}{} }()
		return fresh, nil
	}
	w.models["BTCUSDT"] = newIndicatorModel(old)

	s := models.CachedSummary{SummaryID: 1, InstrumentID: 7, Method: "debate", State: sGen,
		PriceAtGeneration: price, SourceDataTS: fresh[len(fresh)-1].TS}
	w.Track("BTCUSDT", "debate", s, time.Now().Add(time.Hour))

	// tick pertama, bisa sebelum model baru selesai dimuat: tidak boleh memakai model lama
	if inv := w.due("BTCUSDT", price, time.Now()); len(inv) > 0 {
		t.Fatalf("entri baru dibuang pada tick pertama: %s %s -> %s", inv[0].Reason, inv[0].StateFrom, inv[0].StateTo)
	}
	select {
	case <-loaded:
	case <-time.After(2 * time.Second):
		t.Fatal("Track harus memuat ulang model yang tidak sesuai candle summary")
	}
	deadline := time.Now().Add(time.Second)
	for {
		w.mu.Lock()
		m := w.models["BTCUSDT"]
		w.mu.Unlock()
		if m != nil && m.LastClosedTS.Equal(s.SourceDataTS) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("model baru tidak terpasang")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// dengan model yang benar, harga yang sama tidak mengubah S
	if inv := w.due("BTCUSDT", price, time.Now()); len(inv) > 0 {
		t.Fatalf("model segar: entri dibuang %s %s -> %s", inv[0].Reason, inv[0].StateFrom, inv[0].StateTo)
	}
	// drift harga >= θ tetap terdeteksi
	if inv := w.due("BTCUSDT", price*0.98, time.Now()); len(inv) != 1 || inv[0].Reason != "price_move" {
		t.Fatalf("drift 2%% dengan θ 1%% harus dibuang sebagai price_move, dapat %+v", inv)
	}
}

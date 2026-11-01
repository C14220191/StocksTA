package main

import (
	"fmt"
	"math"
	"time"
)

// Indikator teknikal server-side (Bab 3.3.1) untuk dua keperluan:
//  1. IndicatorSnapshot: nilai indikator dari candle 1h yang sudah tutup, dikirim ke prompt AI.
//  2. Vektor kondisi S (Bab 2.5): zona RSI(14), posisi harga terhadap EMA(20), dan tanda
//     histogram MACD(12,26,9). Dihitung dari candle tertutup ditambah harga terkini p sebagai
//     close sementara, sehingga S ikut berubah saat harga bergerak di tengah candle berjalan.
//     Entri cache dianggap basi (SFR, pemeriksaan ketiga hybrid invalidation) kalau S_serve != S_gen.

// stateCandles: jumlah candle 1h tertutup yang dibaca per analisis. MACD butuh 26 + 9 candle
// untuk nilai pertama; sisanya warm-up supaya EMA tidak lagi dipengaruhi seed SMA.
const stateCandles = 120

// IndicatorModel menyimpan keadaan akhir indikator setelah candle tertutup terakhir.
// Dengan keadaan ini, RSI/EMA/MACD untuk satu close tambahan p dihitung O(1), cukup murah
// untuk dievaluasi di setiap tick WebSocket oleh watcher.
type IndicatorModel struct {
	LastClosedTS time.Time
	lastClose    float64
	avgGain      float64 // Wilder RSI(14)
	avgLoss      float64
	ema20        float64
	ema12        float64
	ema26        float64
	macdSignal   float64 // EMA(9) dari garis MACD
	ok           bool
}

func newIndicatorModel(c []Candle) *IndicatorModel {
	m := &IndicatorModel{}
	if len(c) < 35 {
		return m
	}
	closes := make([]float64, len(c))
	for i, k := range c {
		closes[i] = k.Close
	}
	last := len(closes) - 1
	ag, al, ok := rsiWilderState(closes, 14)
	e20, e12, e26 := ema(closes, 20), ema(closes, 12), ema(closes, 26)
	_, sig, _ := macd(closes, 12, 26, 9)
	if !ok || math.IsNaN(e20[last]) || math.IsNaN(e26[last]) || math.IsNaN(sig[last]) {
		return m
	}
	*m = IndicatorModel{
		LastClosedTS: c[last].TS, lastClose: closes[last],
		avgGain: ag, avgLoss: al,
		ema20: e20[last], ema12: e12[last], ema26: e26[last], macdSignal: sig[last],
		ok: true,
	}
	return m
}

func emaStep(prev, x float64, p int) float64 {
	k := 2.0 / float64(p+1)
	return x*k + prev*(1-k)
}

// RSI zone thresholds (Murphy, 1999).
const (
	rsiOversold   = 30
	rsiOverbought = 70
)

// State mengembalikan S untuk harga p, mis. "rsi:neutral;ema20:above;macd:pos".
// String kosong kalau model belum cukup data atau harga tidak diketahui.
func (m *IndicatorModel) State(p float64) string {
	if m == nil || !m.ok || p <= 0 {
		return ""
	}
	g, l := 0.0, 0.0
	if d := p - m.lastClose; d > 0 {
		g = d
	} else {
		l = -d
	}
	r := rsiValue((m.avgGain*13+g)/14, (m.avgLoss*13+l)/14)
	zone := "neutral"
	if r >= rsiOverbought {
		zone = "overbought"
	} else if r <= rsiOversold {
		zone = "oversold"
	}
	pos := "below"
	if p > emaStep(m.ema20, p, 20) {
		pos = "above"
	}
	line := emaStep(m.ema12, p, 12) - emaStep(m.ema26, p, 26)
	hist := "neg"
	if line-emaStep(m.macdSignal, line, 9) > 0 {
		hist = "pos"
	}
	return fmt.Sprintf("rsi:%s;ema20:%s;macd:%s", zone, pos, hist)
}

// IndicatorSnapshot: nilai indikator pada candle 1h tertutup terakhir, untuk prompt AI.
type IndicatorSnapshot struct {
	RSI14      *float64 `json:"rsi14"`
	EMA20      *float64 `json:"ema20"`
	EMA50      *float64 `json:"ema50"`
	MACD       *float64 `json:"macd"`
	MACDSignal *float64 `json:"macd_signal"`
	MACDHist   *float64 `json:"macd_hist"`
	BBUpper    *float64 `json:"bb_upper"`
	BBMiddle   *float64 `json:"bb_middle"`
	BBLower    *float64 `json:"bb_lower"`
	StochK     *float64 `json:"stoch_k"`
	StochD     *float64 `json:"stoch_d"`
}

func indicatorSnapshot(c []Candle) IndicatorSnapshot {
	n := len(c)
	closes, highs, lows := make([]float64, n), make([]float64, n), make([]float64, n)
	for i, k := range c {
		closes[i], highs[i], lows[i] = k.Close, k.High, k.Low
	}
	line, sig, hist := macd(closes, 12, 26, 9)
	up, mid, lo := bollinger(closes, 20, 2)
	k, d := stochastic(highs, lows, closes, 14, 3)
	at := func(v []float64) *float64 { // nilai pada candle terakhir saja (bukan nilai valid terakhir)
		if n == 0 || math.IsNaN(v[n-1]) || math.IsInf(v[n-1], 0) {
			return nil
		}
		x := math.Round(v[n-1]*1e6) / 1e6
		return &x
	}
	return IndicatorSnapshot{
		RSI14: at(rsi(closes, 14)), EMA20: at(ema(closes, 20)), EMA50: at(ema(closes, 50)),
		MACD: at(line), MACDSignal: at(sig), MACDHist: at(hist),
		BBUpper: at(up), BBMiddle: at(mid), BBLower: at(lo),
		StochK: at(k), StochD: at(d),
	}
}

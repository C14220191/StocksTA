package main

import (
	"math"
	"strings"
	"testing"
	"time"
)

// Rumus Bab 2.5: TTL = clamp(TTL_base / (1 + k * σ / σ_ref), TTL_min, TTL_max).
func TestDynamicTTL(t *testing.T) {
	base, minTTL, maxTTL := 60*time.Minute, 5*time.Minute, 60*time.Minute
	cases := []struct {
		name     string
		vol, ref float64
		k        float64
		want     time.Duration
	}{
		{"pasar diam -> TTL_base", 0, 0.01, 1, 60 * time.Minute},
		{"σ = σ_ref, k=1 -> base/2", 0.01, 0.01, 1, 30 * time.Minute},
		{"σ = 2σ_ref, k=1 -> base/3", 0.02, 0.01, 1, 20 * time.Minute},
		{"σ = σ_ref, k=2 -> base/3", 0.01, 0.01, 2, 20 * time.Minute},
		{"sangat volatil di-clamp ke TTL_min", 0.5, 0.01, 1, minTTL},
		{"k=0 -> selalu TTL_base", 0.05, 0.01, 0, 60 * time.Minute},
		{"σ_ref tidak diketahui -> TTL_min", 0.005, 0, 1, minTTL},
	}
	for _, c := range cases {
		if got := dynamicTTL(c.vol, c.ref, base, c.k, minTTL, maxTTL); got != c.want {
			t.Errorf("%s: dynamicTTL = %v, mau %v", c.name, got, c.want)
		}
	}
	// TTL_max membatasi kalau TTL_base lebih besar
	if got := dynamicTTL(0, 0.01, 2*time.Hour, 1, minTTL, maxTTL); got != maxTTL {
		t.Errorf("clamp TTL_max: %v", got)
	}
}

func TestSigmaRefStat(t *testing.T) {
	v := []float64{1, 2, 3, 4, 10}
	if got, _ := sigmaRefStat(v, "mean"); got != 4 {
		t.Errorf("mean = %v, mau 4", got)
	}
	if got, _ := sigmaRefStat(v, "p50"); got != 3 {
		t.Errorf("p50 = %v, mau 3", got)
	}
	if _, err := sigmaRefStat(v, "median"); err == nil {
		t.Error("stat tidak dikenal harus error")
	}
}

func TestPriceDeviationPct(t *testing.T) {
	if d, ok := priceDeviationPct(80000, 74000); !ok || math.Abs(d-(-7.5)) > 1e-9 {
		t.Errorf("turun 7.5%%: dapat %v %v", d, ok)
	}
	if _, ok := priceDeviationPct(0, 100); ok {
		t.Error("harga generation 0 harus ok=false")
	}
}

func TestStale(t *testing.T) {
	p := &RedisPolicy{threshold: 2, checkState: true}
	s := &CachedSummary{SummaryID: 7, PriceAtGeneration: 100, State: "rsi:neutral;ema20:above;macd:pos"}
	if inv := p.stale(s, 101.9, s.State); inv != nil {
		t.Errorf("drift 1.9%% < θ 2%% harus valid, dapat %+v", inv)
	}
	if inv := p.stale(s, 102, s.State); inv == nil || inv.Reason != "price_move" {
		t.Errorf("drift 2%% >= θ harus price_move, dapat %+v", inv)
	}
	if inv := p.stale(s, 100.5, "rsi:neutral;ema20:below;macd:pos"); inv == nil || inv.Reason != "state_change" {
		t.Errorf("S berubah harus state_change, dapat %+v", inv)
	}
	if inv := (&RedisPolicy{threshold: 2}).stale(s, 100.5, "rsi:neutral;ema20:below;macd:pos"); inv != nil {
		t.Error("checkState=false: perubahan S diabaikan")
	}
	if inv := (&RedisPolicy{}).stale(s, 150, "lain"); inv != nil {
		t.Error("kondisi tanpa θ (TTL saja) tidak boleh invalidasi")
	}
}

func candlesFrom(closes []float64) []Candle {
	c := make([]Candle, len(closes))
	for i, x := range closes {
		c[i] = Candle{TS: time.Unix(int64(i)*3600, 0), Open: x, High: x * 1.001, Low: x * 0.999, Close: x}
	}
	return c
}

// Model O(1) harus sama dengan menghitung ulang seluruh deret dengan close tambahan p.
func TestIndicatorModelMatchesFullRecompute(t *testing.T) {
	closes := make([]float64, 120)
	for i := range closes {
		closes[i] = 100 + 5*math.Sin(float64(i)/7) + float64(i)*0.05
	}
	m := newIndicatorModel(candlesFrom(closes))
	for _, p := range []float64{90, 100, 104.3, 110, 120} {
		full := append(append([]float64{}, closes...), p)
		r := rsi(full, 14)[len(full)-1]
		e20 := ema(full, 20)[len(full)-1]
		_, _, h := macd(full, 12, 26, 9)
		zone := "neutral"
		if r >= 70 {
			zone = "overbought"
		} else if r <= 30 {
			zone = "oversold"
		}
		pos := map[bool]string{true: "above", false: "below"}[p > e20]
		hist := map[bool]string{true: "pos", false: "neg"}[h[len(full)-1] > 0]
		want := "rsi:" + zone + ";ema20:" + pos + ";macd:" + hist
		if got := m.State(p); got != want {
			t.Errorf("p=%v: State = %s, hitung ulang = %s", p, got, want)
		}
	}
	if (&IndicatorModel{}).State(100) != "" {
		t.Error("model tanpa data harus mengembalikan S kosong")
	}
	if newIndicatorModel(candlesFrom(closes[:20])).State(100) != "" {
		t.Error("candle < 35 harus mengembalikan S kosong")
	}
}

func TestStochastic(t *testing.T) {
	high := []float64{10, 11, 12, 13, 14}
	low := []float64{8, 9, 10, 11, 12}
	cl := []float64{9, 10, 11, 12, 14}
	k, d := stochastic(high, low, cl, 3, 2)
	// candle ke-4: HH 14, LL 10, close 14 -> 100
	if math.Abs(k[4]-100) > 1e-9 {
		t.Errorf("%%K = %v, mau 100", k[4])
	}
	// candle ke-3: HH 13, LL 9, close 12 -> 75; %D(2) di candle ke-4 = (75+100)/2
	if math.Abs(d[4]-87.5) > 1e-9 {
		t.Errorf("%%D = %v, mau 87.5", d[4])
	}
}

func TestIndicatorSnapshot(t *testing.T) {
	closes := make([]float64, 120)
	for i := range closes {
		closes[i] = 100 + float64(i%10)
	}
	s := indicatorSnapshot(candlesFrom(closes))
	for name, v := range map[string]*float64{"rsi": s.RSI14, "ema50": s.EMA50, "macd_hist": s.MACDHist,
		"bb_lower": s.BBLower, "stoch_d": s.StochD} {
		if v == nil {
			t.Errorf("%s kosong untuk 120 candle", name)
		}
	}
}

func TestRealizedVolatility(t *testing.T) {
	if v := realizedVolatility([]Candle{{Close: 100}, {Close: 100}, {Close: 100}}); v != 0 {
		t.Errorf("harga datar = %v, mau 0", v)
	}
	want := math.Log(1.1) * math.Sqrt(2)
	if v := realizedVolatility([]Candle{{Close: 100}, {Close: 110}, {Close: 100}}); math.Abs(v-want) > 1e-12 {
		t.Errorf("zigzag = %v, mau %v", v, want)
	}
}

func TestInstanceKey(t *testing.T) {
	cases := map[string]policyConfig{
		"no_cache":        {mode: "no_cache"},
		"hybrid-2":        {mode: "hybrid", threshold: 2},
		"hybrid-2-pg":     {mode: "hybrid", threshold: 2, pregen: true},
		"event_only-0.5":  {mode: "event_only", threshold: 0.5},
		"lab.hybrid-2-pg": {mode: "hybrid", threshold: 2, pregen: true, namespace: "lab"},
		"lab.no_cache":    {mode: "no_cache", namespace: "lab"},
	}
	for want, c := range cases {
		if got := c.instanceKey(); got != want {
			t.Errorf("instanceKey = %s, mau %s", got, want)
		}
	}
}

func TestValidSymbol(t *testing.T) {
	for _, s := range []string{"BTCUSDT", "BBCA.JK", "XAUUSDT"} {
		if !validSymbol(s) {
			t.Errorf("%s harus valid", s)
		}
	}
	for _, s := range []string{"", "btc", "BTC;DROP", strings.Repeat("A", 30), "../X"} {
		if validSymbol(s) {
			t.Errorf("%q harus ditolak", s)
		}
	}
}

func TestTickerURL(t *testing.T) {
	cases := map[string]string{
		"https://data-api.binance.vision/api/v3/klines": "https://data-api.binance.vision/api/v3/ticker/price",
		"https://fapi.binance.com/fapi/v1/klines":       "https://fapi.binance.com/fapi/v1/ticker/price",
	}
	for in, want := range cases {
		if got := tickerURL(in); got != want {
			t.Errorf("tickerURL(%s) = %s, mau %s", in, got, want)
		}
	}
}

// Simbol yang jarang bertransaksi tetap memakai harga terakhir selama koneksi market-nya sehat.
func TestLiveQuoteFreshness(t *testing.T) {
	m := &marketStream{name: "futures", connected: true, lastMsg: time.Now()}
	f := &PriceFeed{maxAge: 30 * time.Second, idleMax: 15 * time.Minute,
		bySymbol: map[string]*marketStream{"COPPERUSDT": m, "XAUUSDT": m}, prices: map[string]Quote{}}
	f.prices["XAUUSDT"] = Quote{Price: 1, ReceivedAt: time.Now()}
	f.prices["COPPERUSDT"] = Quote{Price: 2, ReceivedAt: time.Now().Add(-3 * time.Minute)}
	if _, ok := f.Live("XAUUSDT"); !ok {
		t.Error("harga baru harus berlaku")
	}
	if _, ok := f.Live("COPPERUSDT"); !ok {
		t.Error("simbol sepi (3 mnt) dengan koneksi sehat harus tetap berlaku")
	}
	f.prices["COPPERUSDT"] = Quote{Price: 2, ReceivedAt: time.Now().Add(-20 * time.Minute)}
	if _, ok := f.Live("COPPERUSDT"); ok {
		t.Error("simbol diam > idleMax tidak boleh dipakai")
	}
	f.prices["COPPERUSDT"] = Quote{Price: 2, ReceivedAt: time.Now().Add(-3 * time.Minute)}
	m.lastMsg = time.Now().Add(-time.Minute) // koneksi tidak mengirim apa pun 1 menit
	if _, ok := f.Live("COPPERUSDT"); ok {
		t.Error("koneksi tidak sehat: harga lama tidak boleh dipakai")
	}
	if _, ok := f.Live("BTCUSDT"); ok {
		t.Error("simbol tanpa harga harus ok=false")
	}
}

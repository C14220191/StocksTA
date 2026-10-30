package main

import (
	"math"
	"testing"
	"time"
)

func TestDynamicTTL(t *testing.T) {
	minTTL, maxTTL := 5*time.Minute, 60*time.Minute
	cases := []struct {
		name     string
		vol, ref float64
		want     time.Duration
	}{
		{"pasar diam -> TTL_max", 0, 0.01, maxTTL},
		{"setengah sigma_ref -> tengah", 0.005, 0.01, 32*time.Minute + 30*time.Second},
		{"sama dengan sigma_ref -> TTL_min", 0.01, 0.01, minTTL},
		{"di atas sigma_ref di-clamp", 0.05, 0.01, minTTL},
		{"sigma_ref tidak diketahui -> TTL_min", 0.005, 0, minTTL},
	}
	for _, c := range cases {
		if got := dynamicTTL(c.vol, c.ref, minTTL, maxTTL); got != c.want {
			t.Errorf("%s: dynamicTTL(%v, %v) = %v, mau %v", c.name, c.vol, c.ref, got, c.want)
		}
	}
}

func TestPriceDeviationPct(t *testing.T) {
	if d, ok := priceDeviationPct(80000, 74000); !ok || math.Abs(d-(-7.5)) > 1e-9 {
		t.Errorf("turun 7.5%%: dapat %v %v", d, ok)
	}
	if d, ok := priceDeviationPct(100, 102); !ok || math.Abs(d-2) > 1e-9 {
		t.Errorf("naik 2%%: dapat %v %v", d, ok)
	}
	if _, ok := priceDeviationPct(0, 100); ok {
		t.Error("harga generation 0 harus ok=false")
	}
	if _, ok := priceDeviationPct(100, 0); ok {
		t.Error("harga request 0 harus ok=false")
	}
}

func TestPercentile(t *testing.T) {
	v := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := percentile(v, 0.9); math.Abs(got-9.1) > 1e-9 {
		t.Errorf("p90 = %v, mau 9.1", got)
	}
	if got := percentile(v, 0); got != 1 {
		t.Errorf("p0 = %v, mau 1", got)
	}
	if got := percentile(v, 1); got != 10 {
		t.Errorf("p100 = %v, mau 10", got)
	}
	if got := percentile(nil, 0.9); got != 0 {
		t.Errorf("kosong = %v, mau 0", got)
	}
}

func TestRealizedVolatility(t *testing.T) {
	flat := []Candle{{Close: 100}, {Close: 100}, {Close: 100}}
	if v := realizedVolatility(flat); v != 0 {
		t.Errorf("harga datar = %v, mau 0", v)
	}
	// log return: ln(1.1), ln(1/1.1) -> mean 0, stddev sampel = ln(1.1) * sqrt(2)
	zigzag := []Candle{{Close: 100}, {Close: 110}, {Close: 100}}
	want := math.Log(1.1) * math.Sqrt(2)
	if v := realizedVolatility(zigzag); math.Abs(v-want) > 1e-12 {
		t.Errorf("zigzag = %v, mau %v", v, want)
	}
}

func TestTickerURL(t *testing.T) {
	got := tickerURL("https://data-api.binance.vision/api/v3/klines")
	if got != "https://data-api.binance.vision/api/v3/ticker/price" {
		t.Errorf("tickerURL = %s", got)
	}
}

package main

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestValidMethod(t *testing.T) {
	for _, m := range []string{"debate", "mtf", "grounded"} {
		if !validMethod(m) {
			t.Errorf("%s harus valid", m)
		}
	}
	for _, m := range []string{"", "summary", "DEBATE", "x"} {
		if validMethod(m) {
			t.Errorf("%q tidak boleh valid", m)
		}
	}
}

func TestCacheKeyPerMethod(t *testing.T) {
	p := &RedisPolicy{prefix: "hybrid-2-pg", version: "hermes-v5"}
	if got := p.key("BTCUSDT", "mtf"); got != "ta:summary:hybrid-2-pg:hermes-v5:BTCUSDT:mtf" {
		t.Errorf("key = %s", got)
	}
	if p.key("BTCUSDT", "debate") == p.key("BTCUSDT", "grounded") {
		t.Error("metode berbeda harus punya entry berbeda")
	}
}

func TestStaleCarriesMethod(t *testing.T) {
	p := &RedisPolicy{threshold: 2}
	inv := p.stale(&CachedSummary{PriceAtGeneration: 100, Method: "grounded"}, 103, "")
	if inv == nil || inv.Method != "grounded" || inv.Reason != "price_move" {
		t.Fatalf("inv = %+v", inv)
	}
}

func TestConsensusSignal(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"BUY", "BUY", "SELL"}, "BUY"},
		{[]string{"HOLD"}, "HOLD"},
		{[]string{"BUY", "SELL"}, "MIXED"},
		{[]string{"BUY", "SELL", "HOLD"}, "MIXED"},
		{[]string{"SELL", "SELL"}, "SELL"},
	}
	for _, c := range cases {
		if got := consensusSignal(c.in); got == nil || *got != c.want {
			t.Errorf("consensus(%v) = %v, mau %s", c.in, got, c.want)
		}
	}
	if consensusSignal(nil) != nil {
		t.Error("tanpa analisis harus nil")
	}
}

func dailyCandles(n int, start time.Time, close func(i int) float64) []Candle {
	out := make([]Candle, n)
	for i := range out {
		c := close(i)
		out[i] = Candle{TS: start.AddDate(0, 0, i), Open: c, High: c + 1, Low: c - 1, Close: c, Volume: float64(i + 1)}
	}
	return out
}

func TestPriceStats(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	daily := dailyCandles(400, start, func(i int) float64 { return 100 + float64(i) })
	s := priceStats(daily, 0.004)
	if s.Days != 400 || *s.LastClose != 499 {
		t.Fatalf("days/last = %d/%v", s.Days, *s.LastClose)
	}
	// 52 minggu: candle dengan ts >= last - 365 hari -> close 134..499
	if *s.High52W != 500 || *s.Low52W != 133 {
		t.Errorf("52w = %v/%v", *s.High52W, *s.Low52W)
	}
	if *s.High20D != 500 || *s.Low20D != 479 {
		t.Errorf("20d = %v/%v", *s.High20D, *s.Low20D)
	}
	if want := (499.0/469 - 1) * 100; math.Abs(*s.Return30DPct-want) > 1e-9 {
		t.Errorf("30d = %v, mau %v", *s.Return30DPct, want)
	}
	if want := (499.0/134 - 1) * 100; math.Abs(*s.Return1YPct-want) > 1e-9 {
		t.Errorf("1y = %v, mau %v", *s.Return1YPct, want)
	}
	if *s.Volatility1H != 0.004 {
		t.Error("volatilitas 1H harus diteruskan")
	}

	short := priceStats(dailyCandles(40, start, func(i int) float64 { return 10 }), 0)
	if short.Return1YPct != nil || short.Return30DPct == nil || short.Volatility1H != nil {
		t.Errorf("riwayat 40 hari: 1y harus nil, 30d terisi, σ nil: %+v", short)
	}
	if empty := priceStats(nil, 0); empty.Days != 0 || empty.LastClose != nil {
		t.Error("tanpa candle harian harus kosong")
	}
}

func TestTFDataShortHistory(t *testing.T) {
	c := dailyCandles(150, time.Now(), func(i int) float64 { return 50 + math.Sin(float64(i)) })
	d := tfData("1D", c)
	if d.N != 150 || len(d.Candles) != ctxPromptCandles {
		t.Errorf("n=%d candles=%d", d.N, len(d.Candles))
	}
	if d.EMA200 != nil {
		t.Error("EMA200 harus nil kalau candle < 200")
	}
	if d.EMA20 == nil || d.EMA50 == nil || d.RSI14 == nil || d.VolumeAvg20 == nil {
		t.Error("EMA20/50, RSI14, volume rata-rata harus terisi")
	}
	if *d.VolumeAvg20 != (131.0+150)/2 { // rata-rata volume 20 candle terakhir: 131..150
		t.Errorf("volume avg = %v", *d.VolumeAvg20)
	}
	if empty := tfData("4H", nil); empty.Candles == nil || empty.VolumeAvg20 != nil {
		t.Error("tanpa candle: daftar kosong (bukan null) dan tanpa rata-rata volume")
	}
}

func TestClosedCandles(t *testing.T) {
	in := []ChartCandle{{Time: 0, Close: 1, Closed: true}, {Time: 14400, Close: 2, Closed: false}}
	out := closedCandles(in)
	if len(out) != 1 || out[0].Close != 1 {
		t.Errorf("hanya bucket tertutup: %+v", out)
	}
}

// Respons /analyze asli dari AI service (4 Nov, metode debate, dipersingkat).
const aiServiceResponse = `{"content":"ringkas","signal":"BUY","confidence":60,"support":77000.0,"resistance":84900.0,
"detail":{"analysts":[{"role":"technical","signal":"bullish","confidence":62,"reason":"r"}],"bias":"bullish"},
"format_ok":true,"method":"debate","skill":"ta-bull-bear-debate","tool_events":0,"model":"upstage/solar-mini4:free",
"tokens_in":2891,"tokens_out":858,"cache_read_tokens":0,"cost_usd":0.0,"prompt_version":"hermes-v5","exec_ms":16437,"queue_ms":0}`

func TestDecodeAIServiceResponse(t *testing.T) {
	var r AIResult
	if err := json.Unmarshal([]byte(aiServiceResponse), &r); err != nil {
		t.Fatal(err)
	}
	if r.Method != "debate" || *r.Confidence != 60 || !r.valid() || *r.ExecMS != 16437 || r.TokensIn != 2891 {
		t.Errorf("hasil decode: %+v", r)
	}
	var d map[string]any
	if err := json.Unmarshal(r.Detail, &d); err != nil || d["bias"] != "bullish" {
		t.Errorf("detail: %s (%v)", r.Detail, err)
	}
	bad := false
	if (&AIResult{FormatOK: &bad}).valid() {
		t.Error("format_ok=false tidak boleh di-cache")
	}
}

// Nama field payload harus sama dengan model pydantic di ai-service/app.py (AnalyzeRequest, ContextIn,
// TimeframeIn, StatsIn); field yang salah nama diam-diam diabaikan pydantic.
func TestAIRequestFieldNames(t *testing.T) {
	daily := dailyCandles(30, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), func(i int) float64 { return 10 })
	mc := &MarketContext{Name: "Bitcoin", AssetType: "crypto", Timeframes: []TFData{tfData("1D", daily)},
		Stats: priceStats(daily, 0.004)}
	b, _ := json.Marshal(newAIRequest(AIInput{Symbol: "BTCUSDT", Method: "mtf", Candles: daily[:3],
		Quote: Quote{Price: 10, TS: time.Now()}, Context: mc}))
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	ctx := got["context"].(map[string]any)
	tf := ctx["timeframes"].([]any)[0].(map[string]any)
	stats := ctx["stats"].(map[string]any)
	want := map[string][]string{
		"request":   {"symbol", "method", "timeframe", "candles", "last_price", "last_price_ts", "context"},
		"context":   {"name", "asset_type", "timeframes", "stats"},
		"timeframe": {"tf", "candles", "n", "ema20", "ema50", "ema200", "rsi14", "volume_avg20"},
		"stats": {"days", "last_close", "last_close_ts", "high_52w", "low_52w", "high_20d", "low_20d",
			"return_30d_pct", "return_1y_pct", "volatility_1h"},
	}
	for name, obj := range map[string]map[string]any{"request": got, "context": ctx, "timeframe": tf, "stats": stats} {
		for _, k := range want[name] {
			if _, ok := obj[k]; !ok {
				t.Errorf("%s tidak punya field %q", name, k)
			}
		}
	}
}

func TestStubDetailShape(t *testing.T) {
	for _, m := range analysisMethods {
		var d map[string]any
		if err := json.Unmarshal(stubDetail(m, "bullish"), &d); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		if d["bias"] != "bullish" {
			t.Errorf("%s: bias = %v", m, d["bias"])
		}
	}
}

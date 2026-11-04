package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Harga mode stub (USD per juta token). Hanya supaya kolom cost_usd terisi saat tes tanpa model.
const (
	stubPriceInPerM  = 0.15
	stubPriceOutPerM = 0.60
)

// stubTokens: token rata-rata per metode dari uji Hermes sungguhan (4 Nov 2026,
// upstage/solar-mini4:free, toolset context_engine, BTC/ETH/BBCA), supaya biaya relatif antar
// metode di lab stub mendekati kenyataan. Kalibrasi final tetap dari pilot-ai.
var stubTokens = map[string][2]int{ // {input, output}
	"debate":   {3000, 1050},
	"mtf":      {4850, 450},
	"grounded": {3000, 1100},
}

// AIResult adalah hasil satu panggilan AI inference (respons /analyze AI service).
type AIResult struct {
	Content         string          `json:"content"` // ringkasan (summary) dari metode
	Signal          string          `json:"signal"`  // BUY | SELL | HOLD = bias bullish | bearish | neutral
	Method          string          `json:"method"`
	Confidence      *int            `json:"confidence"`
	Detail          json.RawMessage `json:"detail"` // isi terstruktur per metode; null kalau output bukan JSON
	Model           string          `json:"model"`
	TokensIn        int             `json:"tokens_in"`
	TokensOut       int             `json:"tokens_out"`
	CacheReadTokens int             `json:"cache_read_tokens"`
	CostUSD         float64         `json:"cost_usd"`
	PromptVersion   string          `json:"prompt_version"`
	Support         *float64        `json:"support"`     // level dari AI, nil kalau tidak valid
	Resistance      *float64        `json:"resistance"`  // level dari AI, nil kalau tidak valid
	FormatOK        *bool           `json:"format_ok"`   // output AI berupa JSON valid
	ToolEvents      *int            `json:"tool_events"` // > 0 = model memanggil tool walau dilarang
	QueueMS         *int            `json:"queue_ms"`    // menunggu giliran proses Hermes
	ExecMS          *int            `json:"exec_ms"`     // proses Hermes berjalan
}

// valid: hasil boleh disimpan di cache (output terbaca sebagai JSON sesuai format skill).
func (r *AIResult) valid() bool { return r.FormatOK == nil || *r.FormatOK }

// AIInput: semua yang dikirim ke AI untuk satu analisis.
type AIInput struct {
	Symbol     string
	Method     string
	Candles    []Candle // 14 candle 1h tertutup terakhir
	Quote      Quote    // harga terkini, supaya analisis mencerminkan pergerakan sejak candle terakhir tutup
	Indicators *IndicatorSnapshot
	Context    *MarketContext // multi-timeframe, statistik harian, fundamental
}

type AIClient interface {
	Analyze(ctx context.Context, in AIInput) (*AIResult, error)
}

// NewAIClient: mode "http" memanggil layanan FastAPI, selain itu memakai stub (tanpa token).
func NewAIClient(mode, url string) AIClient {
	if mode == "http" {
		// Harus lebih besar dari HERMES_QUEUE_TIMEOUT + HERMES_TIMEOUT di AI service.
		sec, err := strconv.Atoi(env("AI_TIMEOUT_SECONDS", "330"))
		if err != nil || sec <= 0 {
			sec = 330
		}
		return &httpAI{url: url, client: &http.Client{Timeout: time.Duration(sec) * time.Second}}
	}
	delay := 300
	if v, err := strconv.Atoi(env("STUB_DELAY_MS", "300")); err == nil && v >= 0 {
		delay = v
	}
	return &stubAI{delay: time.Duration(delay) * time.Millisecond}
}

// ---- stub ----

type stubAI struct{ delay time.Duration }

var biasOfSignal = map[string]string{"BUY": "bullish", "SELL": "bearish", "HOLD": "neutral"}

func (s *stubAI) Analyze(ctx context.Context, in AIInput) (*AIResult, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	candles := in.Candles
	signal := "HOLD"
	if len(candles) >= 2 && candles[0].Close > 0 {
		change := (candles[len(candles)-1].Close - candles[0].Close) / candles[0].Close
		if change > 0.005 {
			signal = "BUY"
		} else if change < -0.005 {
			signal = "SELL"
		}
	}
	lo, hi := candles[0].Low, candles[0].High
	for _, c := range candles {
		lo, hi = min(lo, c.Low), max(hi, c.High)
	}
	tok := stubTokens[in.Method]
	cost := float64(tok[0])*stubPriceInPerM/1e6 + float64(tok[1])*stubPriceOutPerM/1e6
	ok, execMS, queueMS, tools, conf := true, int(s.delay.Milliseconds()), 0, 0, 50
	return &AIResult{
		Content:       fmt.Sprintf("Ringkasan stub %s untuk %s dari %d candle.", in.Method, in.Symbol, len(candles)),
		Signal:        signal,
		Method:        in.Method,
		Confidence:    &conf,
		Detail:        stubDetail(in.Method, biasOfSignal[signal]),
		Model:         "stub",
		TokensIn:      tok[0],
		TokensOut:     tok[1],
		CostUSD:       cost,
		PromptVersion: "stub",
		Support:       &lo,
		Resistance:    &hi,
		FormatOK:      &ok,
		ToolEvents:    &tools,
		QueueMS:       &queueMS,
		ExecMS:        &execMS,
	}, nil
}

// stubDetail: bentuk detail yang sama dengan normalisasi AI service, supaya UI bisa diuji tanpa model.
func stubDetail(method, bias string) json.RawMessage {
	var d any
	switch method {
	case "debate":
		d = map[string]any{
			"analysts": []map[string]any{
				{"role": "fundamental", "signal": "neutral", "confidence": 0, "reason": "TIDAK ADA DATA (stub)"},
				{"role": "technical", "signal": bias, "confidence": 50, "reason": "stub"},
				{"role": "sentiment", "signal": "neutral", "confidence": 0, "reason": "TIDAK ADA DATA (stub)"},
			},
			"debate":            []map[string]any{{"round": 1, "bull": "stub", "bear": "stub"}},
			"risk":              map[string]any{"worst_case": "stub", "volatility": "stub"},
			"change_conditions": []string{"stub"}, "missing_data": []string{"berita"}, "bias": bias,
		}
	case "mtf":
		d = map[string]any{
			"timeframes": []map[string]any{
				{"tf": "1D", "trend": "sideways", "structure": "campuran", "aligned": true, "note": "stub"},
				{"tf": "4H", "trend": "sideways", "structure": "campuran", "aligned": false, "note": "stub"},
				{"tf": "1H", "trend": "sideways", "structure": "campuran", "aligned": false, "note": "stub"},
			},
			"confluence": map[string]any{"score": 1, "trend": true, "structure": false, "level": false, "rsi": false, "volume": false},
			"setup":      "tidak_jelas",
			"scenarios":  map[string]any{"bullish": "stub", "bearish": "stub", "invalidation": "stub"},
			"bias":       bias,
		}
	default:
		d = map[string]any{
			"thesis": "stub", "brief": "stub", "metrics": []any{}, "red_flags": []string{},
			"weaknesses": []string{"stub"}, "falsifiers": []string{"stub"}, "thesis_holds": false, "bias": bias,
		}
	}
	b, _ := json.Marshal(d)
	return b
}

// ---- http ----

type httpAI struct {
	url    string
	client *http.Client
}

type aiCandle struct {
	TS     string  `json:"ts"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
}

func toAICandle(c Candle) aiCandle {
	return aiCandle{TS: c.TS.UTC().Format(time.RFC3339), Open: c.Open, High: c.High, Low: c.Low,
		Close: c.Close, Volume: c.Volume}
}

type aiRequest struct {
	Symbol      string             `json:"symbol"`
	Method      string             `json:"method"`
	Timeframe   string             `json:"timeframe"`
	Candles     []aiCandle         `json:"candles"`
	LastPrice   float64            `json:"last_price,omitempty"`
	LastPriceTS string             `json:"last_price_ts,omitempty"`
	Indicators  *IndicatorSnapshot `json:"indicators,omitempty"`
	Context     *MarketContext     `json:"context,omitempty"`
}

// newAIRequest: payload ke AI service, sama untuk /analyze dan /prompt/preview.
func newAIRequest(in AIInput) aiRequest {
	req := aiRequest{Symbol: in.Symbol, Method: in.Method, Timeframe: analysisTimeframe,
		Indicators: in.Indicators, Context: in.Context}
	if in.Quote.Price > 0 {
		req.LastPrice = in.Quote.Price
		req.LastPriceTS = in.Quote.TS.UTC().Format(time.RFC3339)
	}
	for _, c := range in.Candles {
		req.Candles = append(req.Candles, toAICandle(c))
	}
	return req
}

func (h *httpAI) Analyze(ctx context.Context, in AIInput) (*AIResult, error) {
	body, err := json.Marshal(newAIRequest(in))
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("panggil AI service: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		msg := string(raw)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("AI service status %d: %s", resp.StatusCode, msg)
	}
	var res AIResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("respons AI tidak valid: %w", err)
	}
	if string(res.Detail) == "null" {
		res.Detail = nil
	}
	return &res, nil
}

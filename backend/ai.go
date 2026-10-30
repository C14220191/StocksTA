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

// AIResult adalah hasil satu panggilan AI inference.
// Layanan FastAPI mengembalikan JSON: content, signal, model, tokens_in, tokens_out,
// cache_read_tokens, cost_usd, prompt_version.
type AIResult struct {
	Content         string   `json:"content"`
	Signal          string   `json:"signal"`
	Model           string   `json:"model"`
	TokensIn        int      `json:"tokens_in"`
	TokensOut       int      `json:"tokens_out"`
	CacheReadTokens int      `json:"cache_read_tokens"`
	CostUSD         float64  `json:"cost_usd"`
	PromptVersion   string   `json:"prompt_version"`
	Support         *float64 `json:"support"`    // level dari AI, nil kalau tidak valid
	Resistance      *float64 `json:"resistance"` // level dari AI, nil kalau tidak valid
}

type AIClient interface {
	Analyze(ctx context.Context, symbol string, candles []Candle) (*AIResult, error)
}

// NewAIClient: mode "http" memanggil layanan FastAPI, selain itu memakai stub (tanpa token).
func NewAIClient(mode, url string) AIClient {
	if mode == "http" {
		return &httpAI{url: url, client: &http.Client{Timeout: 5 * time.Minute}}
	}
	delay := 300
	if v, err := strconv.Atoi(env("STUB_DELAY_MS", "300")); err == nil && v >= 0 {
		delay = v
	}
	return &stubAI{delay: time.Duration(delay) * time.Millisecond}
}

// ---- stub ----

type stubAI struct{ delay time.Duration }

func (s *stubAI) Analyze(ctx context.Context, symbol string, candles []Candle) (*AIResult, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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
	tokensIn := 300 + 25*len(candles)
	tokensOut := 120
	cost := float64(tokensIn)*stubPriceInPerM/1e6 + float64(tokensOut)*stubPriceOutPerM/1e6
	return &AIResult{
		Content:       fmt.Sprintf("Ringkasan stub untuk %s dari %d candle.", symbol, len(candles)),
		Signal:        signal,
		Model:         "stub",
		TokensIn:      tokensIn,
		TokensOut:     tokensOut,
		CostUSD:       cost,
		PromptVersion: "stub",
		Support:       &lo,
		Resistance:    &hi,
	}, nil
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

type aiRequest struct {
	Symbol    string     `json:"symbol"`
	Timeframe string     `json:"timeframe"`
	Candles   []aiCandle `json:"candles"`
}

func (h *httpAI) Analyze(ctx context.Context, symbol string, candles []Candle) (*AIResult, error) {
	req := aiRequest{Symbol: symbol, Timeframe: analysisTimeframe}
	for _, c := range candles {
		req.Candles = append(req.Candles, aiCandle{
			TS: c.TS.UTC().Format(time.RFC3339), Open: c.Open, High: c.High,
			Low: c.Low, Close: c.Close, Volume: c.Volume,
		})
	}
	body, err := json.Marshal(req)
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
	return &res, nil
}

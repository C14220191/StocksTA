package models

import (
	"encoding/json"
	"time"
)

// InferenceLog adalah satu baris pengukuran per request (tabel inference_log).
type InferenceLog struct {
	InstrumentID    int
	Condition       string
	CacheStatus     string
	LatencyMS       int
	Model           string
	TokensIn        int
	TokensOut       int
	CacheReadTokens int
	CostUSD         float64
	DataAgeSeconds  *int
	TTLSeconds      *int
	Volatility      float64

	Trigger           string
	PromptVersion     string
	Threshold         *float64
	SummaryID         *int64
	PriceAtGeneration *float64
	PriceAtRequest    *float64
	PriceSource       string
	PriceDeviationPct *float64
	CacheAgeSeconds   *int
	AIQueueMS         *int
	AIExecMS          *int
	AIFormatOK        *bool
	Error             string

	Pregen            bool
	StateAtGeneration string
	StateAtRequest    string
	StateChanged      *bool
	Method            string // debate | mtf | grounded
}

// CacheEvent: satu invalidation (tabel cache_events). L_inv = waktu dicatat - CrossedAt.
type CacheEvent struct {
	InstrumentID int
	Reason       string // price_move | state_change | manual
	ChangePct    *float64
	Condition    string
	Threshold    *float64
	Pregen       bool
	SummaryID    *int64
	DetectedBy   string // watcher | request | scheduler | admin
	CrossedAt    *time.Time
	LatencyMS    *float64
	StateFrom    string
	StateTo      string
	Method       string
}

// AnalyzeResponse: respons GET /api/analyze/:symbol, disusun dari baris inference_log dan summary
// yang disajikan.
type AnalyzeResponse struct {
	Symbol            string          `json:"symbol"`
	Method            string          `json:"method"` // debate | mtf | grounded
	Condition         string          `json:"condition"`
	CacheStatus       string          `json:"cache_status"`
	Origin            string          `json:"origin"` // ondemand | pregen
	Signal            string          `json:"signal"` // BUY | SELL | HOLD = bias bullish | bearish | neutral
	Confidence        *int            `json:"confidence"`
	Summary           string          `json:"summary"`
	Detail            json.RawMessage `json:"detail"` // isi terstruktur metode; null kalau output AI bukan JSON
	Model             string          `json:"model"`
	FormatOK          *bool           `json:"format_ok,omitempty"` // hanya saat AI dipanggil request ini
	LatencyMS         int64           `json:"latency_ms"`
	TokensIn          int             `json:"tokens_in"`
	TokensOut         int             `json:"tokens_out"`
	CacheReadTokens   int             `json:"cache_read_tokens"`
	CostUSD           float64         `json:"cost_usd"`
	DataAgeSeconds    int             `json:"data_age_seconds"`
	CacheAgeSeconds   int             `json:"cache_age_seconds"`
	SourceDataTS      time.Time       `json:"source_data_ts"`
	GeneratedAt       time.Time       `json:"generated_at"`
	PriceAtGeneration float64         `json:"price_at_generation"`
	PriceAtRequest    float64         `json:"price_at_request"`
	PriceDeviationPct *float64        `json:"price_deviation_pct"`
	StateAtGeneration string          `json:"state_at_generation"`
	StateAtRequest    string          `json:"state_at_request"`
	Support           *float64        `json:"support"`    // dari AI
	Resistance        *float64        `json:"resistance"` // dari AI
}

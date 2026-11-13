package models

import (
	"encoding/json"
	"time"
)

// NewSummary: satu hasil AI yang akan disimpan ke tabel ai_summaries.
type NewSummary struct {
	InstrumentID   int
	SourceTS       time.Time
	PromptVersion  string
	Model, Content string
	Signal         string
	PriceAtGen     float64
	Support        *float64
	Resistance     *float64
	Origin         string // ondemand | pregen
	State          string
	Indicators     *IndicatorSnapshot
	Method         string
	Confidence     *int
	Detail         json.RawMessage // nil -> NULL (output AI bukan JSON)
}

// StoredSummary: analisis AI terakhir yang tersimpan di ai_summaries untuk satu metode (tanpa memanggil AI).
type StoredSummary struct {
	Method            string          `json:"method"`
	Signal            *string         `json:"signal"`
	Confidence        *int            `json:"confidence"`
	Summary           string          `json:"summary"`
	Detail            json.RawMessage `json:"detail"`
	Model             string          `json:"model"`
	GeneratedAt       time.Time       `json:"generated_at"`
	SourceDataTS      time.Time       `json:"source_data_ts"`
	PriceAtGeneration *float64        `json:"price_at_generation"`
	Support           *float64        `json:"support"`    // dari AI
	Resistance        *float64        `json:"resistance"` // dari AI
}

// CachedSummary adalah baris ai_summaries yang disimpan di cache Redis. Seperti Cortex (Ruan et al., 2026),
// setiap entri membawa metadata performanya sendiri: token, biaya, harga, dan waktu generate.
// Nama field jangan diubah: entri Redis disimpan sebagai JSON dengan nama field ini (skrip delIf membaca
// SummaryID).
type CachedSummary struct {
	SummaryID         int64
	InstrumentID      int
	Content           string
	Signal            string
	Model             string
	PromptVersion     string
	Origin            string    // ondemand | pregen (Lapis 2 / Lapis 1)
	SourceDataTS      time.Time // ts candle terakhir yang dipakai saat summary dibuat
	GeneratedAt       time.Time
	PriceAtGeneration float64  // P_gen: harga terkini yang dilihat AI (dasar event invalidation)
	State             string   // S_gen: vektor kondisi indikator saat generate
	Support           *float64 // level dari AI
	Resistance        *float64 // level dari AI
	TokensIn          int
	TokensOut         int
	CostUSD           float64
	Method            string          // debate | mtf | grounded
	Confidence        *int            // 0-100 dari AI
	Detail            json.RawMessage `json:",omitempty"` // isi terstruktur per metode
}

// IndicatorSnapshot: nilai indikator pada candle 1h tertutup terakhir, untuk prompt AI (kolom
// ai_summaries.indicators).
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

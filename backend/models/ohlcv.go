// Package models berisi struct yang mewakili baris tabel database (dan bentuk tampilannya untuk API).
// Tidak ada logika atau query di sini: query SQL ada di package services, handler HTTP di controllers.
package models

import "time"

// Candle: satu baris tabel ohlcv (harga open/high/low/close/volume satu periode).
type Candle struct {
	TS     time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

// ChartCandle: format yang langsung bisa dipakai TradingView lightweight-charts (time = detik UTC).
type ChartCandle struct {
	Time   int64   `json:"time"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
	Closed bool    `json:"closed"` // false = candle masih berjalan
}

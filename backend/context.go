package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
)

// MarketContext: data tambahan untuk prompt tiga metode analisis, di luar 14 candle 1h dan
// indikator 1h yang sudah ada sejak hermes-v4:
//   - timeframes 1D, 4H, 1H (metode multi-timeframe): 12 candle tertutup + EMA20/50/200, RSI14,
//     volume rata-rata 20. 4H dirangkum dari candle 1h (tidak disimpan terpisah);
//   - statistik harian (52 minggu, 20 hari, perubahan 30 hari / 1 tahun) untuk debate dan grounded;
//   - fundamental saham dari tabel fundamentals (ingestion/fundamentals.py).
//
// Hanya dibangun saat generate (MISS / pre-generation), tidak di jalur HIT, supaya latency HIT
// tidak bertambah.
type MarketContext struct {
	Name           string          `json:"name"`
	AssetType      string          `json:"asset_type"`
	Timeframes     []TFData        `json:"timeframes"`
	Stats          PriceStats      `json:"stats"`
	Fundamentals   json.RawMessage `json:"fundamentals,omitempty"`
	FundamentalsAt *time.Time      `json:"fundamentals_at,omitempty"`
}

type TFData struct {
	TF          string     `json:"tf"` // 1D | 4H | 1H
	Candles     []aiCandle `json:"candles"`
	N           int        `json:"n"` // candle tertutup yang dipakai menghitung indikator
	EMA20       *float64   `json:"ema20"`
	EMA50       *float64   `json:"ema50"`
	EMA200      *float64   `json:"ema200"`
	RSI14       *float64   `json:"rsi14"`
	VolumeAvg20 *float64   `json:"volume_avg20"`
}

type PriceStats struct {
	Days         int      `json:"days"`
	LastClose    *float64 `json:"last_close"`
	LastCloseTS  string   `json:"last_close_ts,omitempty"`
	High52W      *float64 `json:"high_52w"`
	Low52W       *float64 `json:"low_52w"`
	High20D      *float64 `json:"high_20d"`
	Low20D       *float64 `json:"low_20d"`
	Return30DPct *float64 `json:"return_30d_pct"`
	Return1YPct  *float64 `json:"return_1y_pct"`
	Volatility1H *float64 `json:"volatility_1h"`
}

const (
	ctxPromptCandles = 12  // candle per timeframe yang masuk prompt
	ctxIndicatorBars = 260 // candle per timeframe untuk EMA200 (+ warm-up)
	ctxDailyBars     = 400 // candle 1D: 52 minggu + EMA200
)

// aggOrigin: titik awal bucket candle gabungan. Kripto/komoditas 00:00 UTC (sama dengan Binance),
// saham 00:00 WIB supaya sesi bursa tidak terbelah.
func aggOrigin(assetType string) time.Time {
	origin := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if assetType == "stock" {
		origin = origin.Add(-7 * time.Hour)
	}
	return origin
}

func (a *App) marketContext(ctx context.Context, instID int, vol float64) (*MarketContext, error) {
	name, assetType, err := a.store.InstrumentMeta(ctx, instID)
	if err != nil {
		return nil, err
	}
	daily, err := a.store.LatestCandles(ctx, instID, "1d", ctxDailyBars)
	if err != nil {
		return nil, err
	}
	hourly, err := a.store.LatestCandles(ctx, instID, analysisTimeframe, ctxIndicatorBars)
	if err != nil {
		return nil, err
	}
	agg, err := a.store.AggCandles(ctx, instID, 4, ctxIndicatorBars+1, aggOrigin(assetType))
	if err != nil {
		return nil, err
	}
	mc := &MarketContext{
		Name: name, AssetType: assetType,
		Timeframes: []TFData{tfData("1D", daily), tfData("4H", closedCandles(agg)), tfData("1H", hourly)},
		Stats:      priceStats(daily, vol),
	}
	if assetType == "stock" {
		// Fundamental hanya pelengkap: kalau belum ada (atau migrasi 009 belum jalan), prompt
		// menulis TIDAK ADA DATA dan analisis tetap dibuat.
		data, at, err := a.store.Fundamentals(ctx, instID)
		switch {
		case err == nil:
			mc.Fundamentals, mc.FundamentalsAt = data, &at
		case !errors.Is(err, ErrNotFound):
			slog.Warn("gagal membaca fundamentals", "instrument_id", instID, "err", err)
		}
	}
	return mc, nil
}

// closedCandles: bucket gabungan yang sudah tutup saja (candle berjalan tidak dianalisis).
func closedCandles(c []ChartCandle) []Candle {
	out := make([]Candle, 0, len(c))
	for _, k := range c {
		if k.Closed {
			out = append(out, Candle{TS: time.Unix(k.Time, 0).UTC(), Open: k.Open, High: k.High,
				Low: k.Low, Close: k.Close, Volume: k.Volume})
		}
	}
	return out
}

func tfData(tf string, c []Candle) TFData {
	closes := make([]float64, len(c))
	for i, k := range c {
		closes[i] = k.Close
	}
	d := TFData{TF: tf, N: len(c), Candles: []aiCandle{},
		EMA20: lastValid(ema(closes, 20)), EMA50: lastValid(ema(closes, 50)), EMA200: lastValid(ema(closes, 200)),
		RSI14: lastValid(rsi(closes, 14))}
	if len(c) > 0 {
		recent := tail(c, 20)
		var v float64
		for _, k := range recent {
			v += k.Volume
		}
		avg := v / float64(len(recent))
		d.VolumeAvg20 = &avg
	}
	for _, k := range tail(c, ctxPromptCandles) {
		d.Candles = append(d.Candles, toAICandle(k))
	}
	return d
}

// priceStats dari candle 1D tertutup (urut lama -> baru).
func priceStats(daily []Candle, vol float64) PriceStats {
	s := PriceStats{Days: len(daily)}
	if vol > 0 {
		s.Volatility1H = &vol
	}
	if len(daily) == 0 {
		return s
	}
	last := daily[len(daily)-1]
	s.LastClose, s.LastCloseTS = &last.Close, last.TS.UTC().Format(time.RFC3339)
	hiLo := func(c []Candle) (*float64, *float64) {
		hi, lo := math.Inf(-1), math.Inf(1)
		for _, k := range c {
			hi, lo = math.Max(hi, k.High), math.Min(lo, k.Low)
		}
		return &hi, &lo
	}
	var year []Candle
	for _, k := range daily {
		if !k.TS.Before(last.TS.AddDate(0, 0, -365)) {
			year = append(year, k)
		}
	}
	s.High52W, s.Low52W = hiLo(year)
	s.High20D, s.Low20D = hiLo(tail(daily, 20))
	s.Return30DPct = changeSince(daily, last, 30)
	s.Return1YPct = changeSince(daily, last, 365)
	return s
}

// changeSince: perubahan (%) close terakhir terhadap close terakhir yang berumur >= days hari.
// nil kalau riwayat lebih pendek dari itu.
func changeSince(daily []Candle, last Candle, days int) *float64 {
	cut := last.TS.AddDate(0, 0, -days)
	for i := len(daily) - 1; i >= 0; i-- {
		if !daily[i].TS.After(cut) {
			if daily[i].Close <= 0 {
				return nil
			}
			v := (last.Close/daily[i].Close - 1) * 100
			return &v
		}
	}
	return nil
}

func (s *Store) InstrumentMeta(ctx context.Context, instID int) (name, assetType string, err error) {
	err = s.pool.QueryRow(ctx, `SELECT coalesce(name, symbol), asset_type FROM instruments WHERE id = $1`, instID).
		Scan(&name, &assetType)
	return
}

// Fundamentals: snapshot fundamental saham (jsonb) dan kapan diambil. ErrNotFound kalau belum ada.
func (s *Store) Fundamentals(ctx context.Context, instID int) (json.RawMessage, time.Time, error) {
	var data json.RawMessage
	var at time.Time
	err := s.pool.QueryRow(ctx, `SELECT data, fetched_at FROM fundamentals WHERE instrument_id = $1`, instID).
		Scan(&data, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, at, ErrNotFound
	}
	return data, at, err
}

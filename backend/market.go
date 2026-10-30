package main

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"
)

// Endpoint untuk UI (Home, Search, Detail). Semua endpoint di file ini HANYA membaca database:
// tidak memanggil AI, tidak menulis inference_log, jadi tidak menambah token cost dan
// tidak mencemari data eksperimen. Ringkasan AI baru dibuat lewat /api/analyze/:symbol.

// MarketRow: satu baris instrument untuk tabel Home dan Search.
type MarketRow struct {
	Symbol        string     `json:"symbol"`         // dipakai untuk URL, mis. "BTCUSDT", "BBCA.JK"
	DisplaySymbol string     `json:"display_symbol"` // untuk tampilan, mis. "BTC/USDT", "BBCA"
	Name          string     `json:"name"`
	AssetType     string     `json:"asset_type"` // stock | crypto
	Currency      string     `json:"currency"`   // IDR | USD
	Price         float64    `json:"price"`
	PriceTS       time.Time  `json:"price_ts"`         // waktu buka candle 1h terakhir yang dipakai sebagai harga
	FetchedAt     time.Time  `json:"fetched_at"`       // kapan data harga itu diambil (untuk "diperbarui X menit lalu")
	ChangePct     *float64   `json:"change_pct"`       // terhadap penutupan harian sebelumnya
	Volume24h     *float64   `json:"volume_24h"`       // satuan asli: lembar saham / koin
	Turnover24h   *float64   `json:"turnover_24h"`     // nilai transaksi = sum(volume x close), dalam Currency
	TurnoverUSD   *float64   `json:"turnover_24h_usd"` // dikonversi ke USD (kurs USDIDR_RATE), untuk mengurutkan saham dan kripto bersama
	Signal        *string    `json:"signal"`           // BUY | SELL | HOLD dari ringkasan AI terakhir (bisa null)
	SignalAt      *time.Time `json:"signal_at"`        // kapan ringkasan AI terakhir dibuat
}

const marketSQL = `
WITH last1h AS (
    SELECT DISTINCT ON (instrument_id) instrument_id, ts, close, fetched_at
    FROM ohlcv WHERE timeframe = '1h'
    ORDER BY instrument_id, ts DESC
),
vol AS (
    SELECT o.instrument_id,
           sum(o.volume)::float8           AS volume_24h,
           sum(o.volume * o.close)::float8 AS turnover_24h
    FROM ohlcv o JOIN last1h l USING (instrument_id)
    WHERE o.timeframe = '1h' AND o.ts > l.ts - interval '24 hours'
    GROUP BY o.instrument_id
),
ref AS (
    -- penutupan harian terakhir yang SUDAH selesai sebelum candle 1h terakhir dibuka
    SELECT DISTINCT ON (d.instrument_id) d.instrument_id, d.close AS ref_close
    FROM ohlcv d JOIN last1h l USING (instrument_id)
    WHERE d.timeframe = '1d' AND d.ts + interval '1 day' <= l.ts
    ORDER BY d.instrument_id, d.ts DESC
),
sig AS (
    SELECT DISTINCT ON (instrument_id) instrument_id, signal, generated_at
    FROM ai_summaries
    ORDER BY instrument_id, generated_at DESC
)
SELECT i.symbol, coalesce(i.name, ''), i.asset_type,
       l.close::float8, l.ts, l.fetched_at,
       r.ref_close::float8, v.volume_24h, v.turnover_24h,
       s.signal, s.generated_at
FROM instruments i
JOIN last1h l ON l.instrument_id = i.id
LEFT JOIN ref r ON r.instrument_id = i.id
LEFT JOIN vol v ON v.instrument_id = i.id
LEFT JOIN sig s ON s.instrument_id = i.id
WHERE ($1::text = '' OR i.asset_type = $1::text)
  AND ($2::text = '' OR i.symbol ILIKE '%' || $2::text || '%' OR i.name ILIKE '%' || $2::text || '%')
  AND ($3::text = '' OR i.symbol = $3::text)
ORDER BY i.symbol`

// MarketRows: assetType ("" | stock | crypto), query (pencarian bebas), exact (satu symbol persis).
func (s *Store) MarketRows(ctx context.Context, assetType, query, exact string) ([]MarketRow, error) {
	rows, err := s.pool.Query(ctx, marketSQL, assetType, query, exact)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MarketRow
	for rows.Next() {
		var r MarketRow
		var ref *float64
		if err := rows.Scan(&r.Symbol, &r.Name, &r.AssetType,
			&r.Price, &r.PriceTS, &r.FetchedAt,
			&ref, &r.Volume24h, &r.Turnover24h,
			&r.Signal, &r.SignalAt); err != nil {
			return nil, err
		}
		if ref != nil && *ref > 0 {
			chg := (r.Price - *ref) / *ref * 100
			r.ChangePct = &chg
		}
		r.DisplaySymbol, r.Currency = displayOf(r.Symbol, r.AssetType)
		if r.Turnover24h != nil {
			usd := *r.Turnover24h
			if r.Currency == "IDR" {
				usd /= usdIDRRate()
			}
			r.TurnoverUSD = &usd
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func displayOf(symbol, assetType string) (display, currency string) {
	if assetType == "crypto" {
		for _, q := range []string{"USDT", "USDC", "BUSD"} {
			if strings.HasSuffix(symbol, q) && len(symbol) > len(q) {
				return strings.TrimSuffix(symbol, q) + "/" + q, "USD"
			}
		}
		return symbol, "USD"
	}
	return strings.TrimSuffix(symbol, ".JK"), "IDR"
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

// ChartCandles: n candle terbaru (termasuk yang masih berjalan), urut lama -> baru.
func (s *Store) ChartCandles(ctx context.Context, instID int, timeframe string, n int) ([]ChartCandle, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ts, open::float8, high::float8, low::float8, close::float8, volume::float8,
		       fetched_at >= ts + make_interval(secs => $4) AS closed
		FROM ohlcv
		WHERE instrument_id = $1 AND timeframe = $2
		ORDER BY ts DESC
		LIMIT $3`, instID, timeframe, n, timeframeDuration(timeframe).Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChartCandle
	for rows.Next() {
		var c ChartCandle
		var ts time.Time
		if err := rows.Scan(&ts, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume, &c.Closed); err != nil {
			return nil, err
		}
		c.Time = ts.Unix()
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// StoredSummary: ringkasan AI terakhir yang tersimpan (tanpa memanggil AI).
type StoredSummary struct {
	Signal       *string   `json:"signal"`
	Summary      string    `json:"summary"`
	Model        string    `json:"model"`
	GeneratedAt  time.Time `json:"generated_at"`
	SourceDataTS time.Time `json:"source_data_ts"`
	Support      *float64  `json:"support"`    // dari AI
	Resistance   *float64  `json:"resistance"` // dari AI
}

func (s *Store) LatestSummary(ctx context.Context, instID int) (*StoredSummary, error) {
	var out StoredSummary
	err := s.pool.QueryRow(ctx, `
		SELECT signal, content, model, generated_at, source_data_ts, support::float8, resistance::float8
		FROM ai_summaries WHERE instrument_id = $1
		ORDER BY generated_at DESC LIMIT 1`, instID).
		Scan(&out.Signal, &out.Summary, &out.Model, &out.GeneratedAt, &out.SourceDataTS, &out.Support, &out.Resistance)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- handlers ----

// GET /api/market/overview?type=all|stock|crypto&limit=5&movers=3
func (a *App) MarketOverview(c echo.Context) error {
	assetType, ok := parseAssetType(c.QueryParam("type"))
	if !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "type harus all, stock, atau crypto"})
	}
	limit := queryInt(c, "limit", 10, 1, 50)
	movers := queryInt(c, "movers", 3, 1, 20)

	rows, err := a.store.MarketRows(c.Request().Context(), assetType, "", "")
	if err != nil {
		return a.fail(c, err)
	}

	byTurnover := append([]MarketRow(nil), rows...)
	// diurutkan dalam USD supaya saham (IDR) dan kripto (USD) bisa dibandingkan di tab "Semua"
	sort.SliceStable(byTurnover, func(i, j int) bool { return val(byTurnover[i].TurnoverUSD) > val(byTurnover[j].TurnoverUSD) })

	var gainers, losers []MarketRow
	for _, r := range rows {
		if r.ChangePct == nil {
			continue
		}
		if *r.ChangePct > 0 {
			gainers = append(gainers, r)
		} else if *r.ChangePct < 0 {
			losers = append(losers, r)
		}
	}
	sort.SliceStable(gainers, func(i, j int) bool { return *gainers[i].ChangePct > *gainers[j].ChangePct })
	sort.SliceStable(losers, func(i, j int) bool { return *losers[i].ChangePct < *losers[j].ChangePct })

	return c.JSON(http.StatusOK, map[string]any{
		"type":         orAll(assetType),
		"generated_at": time.Now().UTC(),
		"top_volume":   head(byTurnover, limit),
		"top_gainers":  head(gainers, movers),
		"top_losers":   head(losers, movers),
	})
}

// GET /api/search?q=antm&type=all|stock|crypto
func (a *App) Search(c echo.Context) error {
	q := strings.TrimSpace(c.QueryParam("q"))
	assetType, ok := parseAssetType(c.QueryParam("type"))
	if !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "type harus all, stock, atau crypto"})
	}
	if len(q) > 40 {
		q = q[:40]
	}
	// hapus karakter wildcard dan pemisah tampilan, supaya "BTC/USDT" tetap cocok dengan "BTCUSDT"
	q = strings.NewReplacer("%", "", "_", "", "/", "", " ", "").Replace(q)
	if q == "" {
		return c.JSON(http.StatusOK, map[string]any{"query": "", "count": 0, "results": []MarketRow{}})
	}
	rows, err := a.store.MarketRows(c.Request().Context(), assetType, q, "")
	if err != nil {
		return a.fail(c, err)
	}
	if rows == nil {
		rows = []MarketRow{}
	}
	return c.JSON(http.StatusOK, map[string]any{"query": q, "count": len(rows), "results": rows})
}

// DetailStats: kartu statistik di Detail Page. Dihitung dari candle HARIAN yang sudah tutup.
type DetailStats struct {
	RSI14      *float64 `json:"rsi14"`
	Support    *float64 `json:"support"`    // low terendah 20 hari terakhir
	Resistance *float64 `json:"resistance"` // high tertinggi 20 hari terakhir
	Volume24h  *float64 `json:"volume_24h"`
	Basis      string   `json:"basis"`
}

// GET /api/instruments/:symbol
func (a *App) InstrumentDetail(c echo.Context) error {
	ctx := c.Request().Context()
	symbol := strings.ToUpper(c.Param("symbol"))
	instID, err := a.store.InstrumentID(ctx, symbol)
	if errors.Is(err, ErrNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "instrument tidak ditemukan"})
	}
	if err != nil {
		return a.fail(c, err)
	}

	rows, err := a.store.MarketRows(ctx, "", "", symbol)
	if err != nil {
		return a.fail(c, err)
	}
	if len(rows) == 0 {
		return c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "data harga belum ada, jalankan ingest.py"})
	}
	row := rows[0]

	daily, err := a.store.LatestCandles(ctx, instID, "1d", 60)
	if err != nil {
		return a.fail(c, err)
	}
	stats := DetailStats{Volume24h: row.Volume24h, Basis: "candle harian yang sudah tutup; support/resistance = low/high 20 hari"}
	if len(daily) > 0 {
		closes := make([]float64, len(daily))
		for i, d := range daily {
			closes[i] = d.Close
		}
		stats.RSI14 = lastValid(rsi(closes, 14))
		win := daily
		if len(win) > 20 {
			win = win[len(win)-20:]
		}
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, d := range win {
			lo, hi = math.Min(lo, d.Low), math.Max(hi, d.High)
		}
		stats.Support, stats.Resistance = &lo, &hi
	}

	ai, err := a.store.LatestSummary(ctx, instID)
	if err != nil {
		return a.fail(c, err)
	}

	return c.JSON(http.StatusOK, map[string]any{
		"instrument": row,
		"stats":      stats,
		"ai_latest":  ai, // null kalau belum pernah dianalisis; ringkasan baru lewat /api/analyze/:symbol
	})
}

// Timeframe yang tersedia untuk grafik. 2h/4h/6h digabung dari candle 1h (tidak disimpan),
// jadi tidak perlu ingestion tambahan. Tidak dipakai di prompt AI (prompt_version tetap).
var chartTimeframes = map[string]time.Duration{
	"1h": time.Hour,
	"2h": 2 * time.Hour,
	"4h": 4 * time.Hour,
	"6h": 6 * time.Hour,
	"1d": 24 * time.Hour,
}

// Rentang waktu tiap range, dan timeframe bawaan kalau tf tidak diisi.
// span 0 = ALL (seluruh data yang ada di database, dibatasi maxChartCandles).
var chartRanges = map[string]struct {
	span      time.Duration
	defaultTF string
}{
	"1D":  {24 * time.Hour, "1h"},
	"1W":  {7 * 24 * time.Hour, "1h"},
	"1M":  {30 * 24 * time.Hour, "4h"},
	"3M":  {90 * 24 * time.Hour, "1d"},
	"6M":  {182 * 24 * time.Hour, "1d"},
	"1Y":  {365 * 24 * time.Hour, "1d"},
	"ALL": {0, "1d"},
}

// maxChartCandles: batas jumlah candle per respons (cukup untuk 1 tahun data 1h = 8.760 candle).
const maxChartCandles = 10000

// AggCandles: candle 1h digabung per bucket `hours` jam yang dihitung dari `origin`, urut lama -> baru.
// Kripto memakai origin 00:00 UTC (sama dengan Binance); saham 00:00 WIB supaya sesi bursa tidak terbelah aneh.
// Bucket dianggap tutup kalau waktunya sudah lewat dan semua candle 1h di dalamnya sudah tutup.
func (s *Store) AggCandles(ctx context.Context, instID int, hours, n int, origin time.Time) ([]ChartCandle, error) {
	rows, err := s.pool.Query(ctx, `
		WITH raw AS (
		    SELECT ts, open, high, low, close, volume, fetched_at
		    FROM ohlcv
		    WHERE instrument_id = $1 AND timeframe = '1h'
		    ORDER BY ts DESC
		    LIMIT $3
		)
		SELECT b,
		       (array_agg(open ORDER BY ts))[1]::float8,
		       max(high)::float8, min(low)::float8,
		       (array_agg(close ORDER BY ts DESC))[1]::float8,
		       sum(volume)::float8,
		       bool_and(fetched_at >= ts + interval '1 hour') AND b + make_interval(hours => $2) <= now()
		FROM (SELECT date_bin(make_interval(hours => $2), ts, $5::timestamptz) AS b, * FROM raw) x
		GROUP BY b
		ORDER BY b DESC
		LIMIT $4`, instID, hours, (n+1)*hours, n, origin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChartCandle
	for rows.Next() {
		var c ChartCandle
		var b time.Time
		if err := rows.Scan(&b, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume, &c.Closed); err != nil {
			return nil, err
		}
		c.Time = b.Unix()
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (s *Store) AssetType(ctx context.Context, instID int) (string, error) {
	var t string
	err := s.pool.QueryRow(ctx, `SELECT asset_type FROM instruments WHERE id = $1`, instID).Scan(&t)
	return t, err
}

// GET /api/instruments/:symbol/candles?range=1D|1W|1M|3M|6M|1Y|ALL&tf=1h|2h|4h|6h|1d
func (a *App) InstrumentCandles(c echo.Context) error {
	ctx := c.Request().Context()
	symbol := strings.ToUpper(c.Param("symbol"))
	rangeKey := strings.ToUpper(c.QueryParam("range"))
	if rangeKey == "" {
		rangeKey = "1W"
	}
	cr, ok := chartRanges[rangeKey]
	if !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "range harus 1D, 1W, 1M, 3M, 6M, 1Y, atau ALL"})
	}
	tf := strings.ToLower(c.QueryParam("tf"))
	if tf == "" {
		tf = cr.defaultTF
	}
	tfDur, ok := chartTimeframes[tf]
	if !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "tf harus 1h, 2h, 4h, 6h, atau 1d"})
	}
	n := maxChartCandles
	if cr.span > 0 {
		n = int(cr.span / tfDur)
	}
	if n < 6 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "kombinasi range dan tf menghasilkan terlalu sedikit candle"})
	}
	if n > maxChartCandles {
		n = maxChartCandles
	}

	instID, err := a.store.InstrumentID(ctx, symbol)
	if errors.Is(err, ErrNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "instrument tidak ditemukan"})
	}
	if err != nil {
		return a.fail(c, err)
	}

	const warmup = 60 // candle tambahan supaya indikator sudah terisi di awal grafik
	var all []ChartCandle
	switch tf {
	case "1h", "1d":
		all, err = a.store.ChartCandles(ctx, instID, tf, n+warmup)
	default:
		origin := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		if at, err := a.store.AssetType(ctx, instID); err != nil {
			return a.fail(c, err)
		} else if at == "stock" {
			origin = origin.Add(-7 * time.Hour) // 00:00 WIB
		}
		all, err = a.store.AggCandles(ctx, instID, int(tfDur/time.Hour), n+warmup, origin)
	}
	if err != nil {
		return a.fail(c, err)
	}
	closes := make([]float64, len(all))
	for i, k := range all {
		closes[i] = k.Close
	}
	if n > len(all) {
		n = len(all)
	}
	macdLine, macdSig, macdHist := macd(closes, 12, 26, 9)
	bbUp, bbMid, bbLow := bollinger(closes, 20, 2)

	return c.JSON(http.StatusOK, map[string]any{
		"symbol":    symbol,
		"range":     rangeKey,
		"timeframe": tf,
		"candles":   all[len(all)-n:],
		// setiap deret sejajar dengan "candles"; null = data belum cukup
		"indicators": map[string]any{
			"rsi14": toJSON(rsi(closes, 14), n),
			"sma20": toJSON(sma(closes, 20), n),
			"sma50": toJSON(sma(closes, 50), n),
			"ema20": toJSON(ema(closes, 20), n),
			"ema50": toJSON(ema(closes, 50), n),
			"macd": map[string]any{
				"line": toJSON(macdLine, n), "signal": toJSON(macdSig, n), "hist": toJSON(macdHist, n),
			},
			"bollinger": map[string]any{
				"upper": toJSON(bbUp, n), "middle": toJSON(bbMid, n), "lower": toJSON(bbLow, n),
			},
		},
	})
}

// ---- helper ----

func parseAssetType(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "all", "semua":
		return "", true
	case "stock", "saham":
		return "stock", true
	case "crypto", "kripto":
		return "crypto", true
	}
	return "", false
}

// usdIDRRate: kurs tetap untuk mengurutkan nilai transaksi saja (bukan untuk ditampilkan sebagai harga).
func usdIDRRate() float64 {
	v, err := strconv.ParseFloat(env("USDIDR_RATE", "16000"), 64)
	if err != nil || v <= 0 {
		return 16000
	}
	return v
}

func orAll(t string) string {
	if t == "" {
		return "all"
	}
	return t
}

func queryInt(c echo.Context, key string, def, min, max int) int {
	v, err := strconv.Atoi(c.QueryParam(key))
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func val(p *float64) float64 {
	if p == nil {
		return math.Inf(-1)
	}
	return *p
}

func head(rows []MarketRow, n int) []MarketRow {
	if rows == nil {
		return []MarketRow{}
	}
	if len(rows) > n {
		return rows[:n]
	}
	return rows
}

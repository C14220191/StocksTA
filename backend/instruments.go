package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v4"
)

// Tambah instrumen dari halaman admin (Admin -> Instrumen). Simbol selalu dicek ke sumber datanya
// sebelum disimpan, karena ingestion, price feed, dan tampilan bergantung pada sumber yang benar:
//
//	stock     -> Yahoo Finance, wajib saham Bursa Efek Indonesia (.JK, IDR)
//	crypto    -> Binance spot, pasangan USDT berstatus TRADING
//	commodity -> Binance USDⓈ-M futures, underlyingType COMMODITY (TradFi perpetual, mis. XAUUSDT)
//
// Setelah disimpan, backfill candle 1h + 1d (dan fundamental untuk saham) dijalankan di background
// lewat skrip ingestion Python yang sama dengan cron (lihat ingest_runner.go).

var (
	reStockCode = regexp.MustCompile(`^[A-Z0-9]{2,8}$`)
	reUSDTPair  = regexp.MustCompile(`^[A-Z0-9]{1,20}USDT$`)
	assetSource = map[string]string{"stock": "yahoo", "crypto": "binance", "commodity": "binance_futures"}
)

// lookupError membawa status HTTP untuk respons admin: 400 input salah, 404 tidak terdaftar di
// sumber, 502 sumber tidak bisa dihubungi.
type lookupError struct {
	status int
	msg    string
}

func (e *lookupError) Error() string { return e.msg }

func badInput(format string, a ...any) error {
	return &lookupError{http.StatusBadRequest, fmt.Sprintf(format, a...)}
}

func notListed(format string, a ...any) error {
	return &lookupError{http.StatusNotFound, fmt.Sprintf(format, a...)}
}

func unreachable(source string, err error) error {
	return &lookupError{http.StatusBadGateway, fmt.Sprintf("%s tidak bisa dihubungi: %v", source, err)}
}

// normalizeSymbol: "bbca" -> BBCA.JK, "sol" / "SOL/USDT" -> SOLUSDT.
func normalizeSymbol(assetType, raw string) (string, error) {
	s := strings.NewReplacer(" ", "", "/", "", "-", "", "_", "").Replace(strings.ToUpper(strings.TrimSpace(raw)))
	switch assetType {
	case "stock":
		code := strings.TrimSuffix(s, ".JK")
		if !reStockCode.MatchString(code) {
			return "", badInput("Kode saham IDX tidak valid: %q. Contoh: BBCA atau BBCA.JK.", raw)
		}
		return code + ".JK", nil
	case "crypto", "commodity":
		if !strings.HasSuffix(s, "USDT") {
			s += "USDT"
		}
		if !reUSDTPair.MatchString(s) {
			return "", badInput("Simbol tidak valid: %q. Contoh: SOL, SOLUSDT, atau SOL/USDT.", raw)
		}
		return s, nil
	}
	return "", badInput("asset_type harus stock, crypto, atau commodity")
}

// InstrumentCandidate: hasil pengecekan simbol ke sumber datanya.
type InstrumentCandidate struct {
	ID        int      `json:"id,omitempty"`
	Symbol    string   `json:"symbol"`
	Name      string   `json:"name"`
	AssetType string   `json:"asset_type"`
	Source    string   `json:"source"`
	Currency  string   `json:"currency"`
	Exchange  string   `json:"exchange"`
	Price     *float64 `json:"price"`  // harga terakhir di sumber, sebagai bukti data mengalir
	Exists    bool     `json:"exists"` // sudah ada di tabel instruments
}

var lookupClient = &http.Client{Timeout: 25 * time.Second}

// getJSON: GET dan decode JSON. Status HTTP dikembalikan supaya pemanggil bisa membedakan
// "simbol tidak ada" dari "sumber bermasalah".
func getJSON(ctx context.Context, u string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; StocksTA)") // Yahoo menolak klien tanpa User-Agent
	resp, err := lookupClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20)) // exchangeInfo futures ~ beberapa MB
	if err != nil {
		return resp.StatusCode, err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return resp.StatusCode, fmt.Errorf("respons bukan JSON (HTTP %d)", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// binanceBase: ".../api/v3/" dari URL klines yang sama dengan ingestion dan price feed.
func binanceBase(envKey, def string) string {
	return strings.TrimSuffix(strings.TrimRight(env(envKey, def), "/"), "klines")
}

func binancePrice(ctx context.Context, base, symbol string) *float64 {
	var t struct {
		Price string `json:"price"`
	}
	if _, err := getJSON(ctx, base+"ticker/price?symbol="+url.QueryEscape(symbol), &t); err != nil {
		return nil
	}
	p, err := strconv.ParseFloat(t.Price, 64)
	if err != nil || p <= 0 {
		return nil
	}
	return &p
}

// cleanStockName: "PT Bank Central Asia Tbk" -> "Bank Central Asia".
func cleanStockName(n string) string {
	n = strings.TrimSpace(strings.ReplaceAll(n, " (Persero)", ""))
	n = strings.TrimPrefix(n, "PT ")
	for _, suf := range []string{", Tbk.", ", Tbk", " Tbk.", " Tbk"} {
		n = strings.TrimSuffix(n, suf)
	}
	return strings.TrimSpace(n)
}

func lookupYahoo(ctx context.Context, symbol string) (*InstrumentCandidate, error) {
	base := strings.TrimRight(env("YAHOO_CHART_URL", "https://query1.finance.yahoo.com/v8/finance/chart"), "/")
	var r struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Currency       string   `json:"currency"`
					ExchangeName   string   `json:"exchangeName"`
					InstrumentType string   `json:"instrumentType"`
					LongName       string   `json:"longName"`
					ShortName      string   `json:"shortName"`
					Price          *float64 `json:"regularMarketPrice"`
				} `json:"meta"`
			} `json:"result"`
			Error *struct {
				Code        string `json:"code"`
				Description string `json:"description"`
			} `json:"error"`
		} `json:"chart"`
	}
	code, err := getJSON(ctx, base+"/"+url.PathEscape(symbol)+"?range=5d&interval=1d", &r)
	if err != nil {
		return nil, unreachable("Yahoo Finance", err)
	}
	if r.Chart.Error != nil || len(r.Chart.Result) == 0 {
		return nil, notListed("%s tidak ditemukan di Yahoo Finance.", symbol)
	}
	if code/100 != 2 {
		return nil, unreachable("Yahoo Finance", fmt.Errorf("HTTP %d", code))
	}
	m := r.Chart.Result[0].Meta
	if m.Currency != "IDR" || m.ExchangeName != "JKT" {
		return nil, notListed("%s bukan saham Bursa Efek Indonesia (bursa %s, mata uang %s).", symbol, m.ExchangeName, m.Currency)
	}
	if m.InstrumentType != "" && m.InstrumentType != "EQUITY" {
		return nil, notListed("%s bukan saham (jenis %s).", symbol, m.InstrumentType)
	}
	name := m.LongName
	if name == "" {
		name = m.ShortName
	}
	return &InstrumentCandidate{Symbol: symbol, Name: cleanStockName(name), AssetType: "stock", Source: "yahoo",
		Currency: "IDR", Exchange: "Bursa Efek Indonesia (Yahoo Finance)", Price: m.Price}, nil
}

func lookupBinanceSpot(ctx context.Context, symbol string) (*InstrumentCandidate, error) {
	base := binanceBase("BINANCE_URL", "https://data-api.binance.vision/api/v3/klines")
	var r struct {
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		Symbols []struct {
			Symbol     string `json:"symbol"`
			Status     string `json:"status"`
			BaseAsset  string `json:"baseAsset"`
			QuoteAsset string `json:"quoteAsset"`
		} `json:"symbols"`
	}
	code, err := getJSON(ctx, base+"exchangeInfo?symbol="+url.QueryEscape(symbol), &r)
	if err != nil {
		return nil, unreachable("Binance", err)
	}
	if r.Code == -1121 || (code/100 == 2 && len(r.Symbols) == 0) {
		return nil, notListed("%s tidak ada di Binance spot. Kontrak komoditas (mis. XAUUSDT) ditambahkan sebagai Komoditas.", symbol)
	}
	if code/100 != 2 {
		return nil, unreachable("Binance", fmt.Errorf("HTTP %d %s", code, r.Msg))
	}
	s := r.Symbols[0]
	if s.Status != "TRADING" {
		return nil, notListed("%s di Binance spot berstatus %s, bukan TRADING.", symbol, s.Status)
	}
	return &InstrumentCandidate{Symbol: symbol, Name: s.BaseAsset, AssetType: "crypto", Source: "binance",
		Currency: "USD", Exchange: "Binance spot", Price: binancePrice(ctx, base, symbol)}, nil
}

func lookupBinanceFutures(ctx context.Context, symbol string) (*InstrumentCandidate, error) {
	base := binanceBase("BINANCE_FUTURES_URL", "https://fapi.binance.com/fapi/v1/klines")
	var r struct {
		Symbols []struct {
			Symbol         string `json:"symbol"`
			Status         string `json:"status"`
			BaseAsset      string `json:"baseAsset"`
			QuoteAsset     string `json:"quoteAsset"`
			UnderlyingType string `json:"underlyingType"` // COMMODITY untuk TradFi perpetual
		} `json:"symbols"`
	}
	code, err := getJSON(ctx, base+"exchangeInfo", &r)
	if err != nil {
		return nil, unreachable("Binance Futures", err)
	}
	if code/100 != 2 {
		return nil, unreachable("Binance Futures", fmt.Errorf("HTTP %d", code))
	}
	for _, s := range r.Symbols {
		if s.Symbol != symbol {
			continue
		}
		if s.UnderlyingType != "COMMODITY" {
			hint := ""
			if s.UnderlyingType == "COIN" {
				hint = " Untuk kripto, tambahkan sebagai Kripto (Binance spot)."
			}
			return nil, notListed("%s di Binance Futures bertipe %s, bukan komoditas.%s", symbol, s.UnderlyingType, hint)
		}
		if s.Status != "TRADING" {
			return nil, notListed("%s di Binance Futures berstatus %s, bukan TRADING.", symbol, s.Status)
		}
		return &InstrumentCandidate{Symbol: symbol, Name: s.BaseAsset, AssetType: "commodity", Source: "binance_futures",
			Currency: "USD", Exchange: "Binance USDⓈ-M futures (TradFi perpetual)", Price: binancePrice(ctx, base, symbol)}, nil
	}
	return nil, notListed("%s tidak ada di Binance USDⓈ-M futures.", symbol)
}

// lookupSource: normalisasi + cek ke sumber, tanpa database.
func lookupSource(ctx context.Context, assetType, raw string) (*InstrumentCandidate, error) {
	symbol, err := normalizeSymbol(assetType, raw)
	if err != nil {
		return nil, err
	}
	switch assetType {
	case "stock":
		return lookupYahoo(ctx, symbol)
	case "crypto":
		return lookupBinanceSpot(ctx, symbol)
	default:
		return lookupBinanceFutures(ctx, symbol)
	}
}

func (a *App) lookupInstrument(ctx context.Context, assetType, raw string) (*InstrumentCandidate, error) {
	cand, err := lookupSource(ctx, assetType, raw)
	if err != nil {
		return nil, err
	}
	id, err := a.store.InstrumentID(ctx, cand.Symbol)
	switch {
	case err == nil:
		cand.ID, cand.Exists = id, true
	case !errors.Is(err, ErrNotFound):
		return nil, err
	}
	return cand, nil
}

func (a *App) lookupFail(c echo.Context, err error) error {
	var le *lookupError
	if errors.As(err, &le) {
		return c.JSON(le.status, map[string]string{"error": le.msg})
	}
	return a.fail(c, err)
}

// AdminInstrumentLookup: GET /api/admin/instruments/lookup?asset_type=&symbol= (pratinjau, tanpa menyimpan).
func (a *App) AdminInstrumentLookup(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
	defer cancel()
	cand, err := a.lookupInstrument(ctx, c.QueryParam("asset_type"), c.QueryParam("symbol"))
	if err != nil {
		return a.lookupFail(c, err)
	}
	return c.JSON(http.StatusOK, cand)
}

// AdminAddInstrument: POST /api/admin/instruments {"asset_type", "symbol", "name"?}.
func (a *App) AdminAddInstrument(c echo.Context) error {
	var body struct {
		AssetType string `json:"asset_type"`
		Symbol    string `json:"symbol"`
		Name      string `json:"name"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "body harus JSON {asset_type, symbol, name}"})
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
	defer cancel()
	cand, err := a.lookupInstrument(ctx, body.AssetType, body.Symbol) // selalu dicek ulang di server
	if err != nil {
		return a.lookupFail(c, err)
	}
	if cand.Exists {
		return c.JSON(http.StatusConflict, map[string]string{"error": cand.Symbol + " sudah terdaftar."})
	}
	if name := strings.TrimSpace(body.Name); name != "" {
		cand.Name = name
	}
	if utf8.RuneCountInString(cand.Name) > 80 {
		cand.Name = string([]rune(cand.Name)[:80])
	}
	id, err := a.store.AddInstrument(ctx, cand.Symbol, cand.Name, cand.AssetType, cand.Source)
	if errors.Is(err, ErrExists) {
		return c.JSON(http.StatusConflict, map[string]string{"error": cand.Symbol + " sudah terdaftar."})
	}
	if err != nil {
		return a.fail(c, err)
	}
	cand.ID, cand.Exists = id, true
	slog.Info("instrumen ditambahkan", "symbol", cand.Symbol, "asset_type", cand.AssetType, "source", cand.Source, "ip", c.RealIP())
	started := a.ingest.start(cand.Symbol, cand.AssetType)

	notes := []string{"Backfill candle 1 jam dan harian (±1 tahun) berjalan di background, biasanya 1–3 menit. " +
		"Statusnya tampil di tabel; analisis AI baru bisa dibuat setelah candle tersedia."}
	if cand.Source != "yahoo" {
		notes = append(notes, "Harga live WebSocket untuk instrumen ini aktif setelah backend di-restart. "+
			"Sebelum itu harga diambil dari candle 1 jam terakhir.")
	}
	if a.dynamicTTL() != nil {
		notes = append(notes, "σ_ref untuk TTL dinamis dihitung saat backend start; sampai restart, TTL instrumen ini memakai TTL_min.")
	}
	notes = append(notes, "Instance lab (port 8101–8115) yang sedang berjalan tidak ikut sampai di-restart. "+
		"Jangan menambah instrumen di tengah eksperimen.")
	return c.JSON(http.StatusCreated, map[string]any{"instrument": cand, "ingest_started": started, "notes": notes})
}

// AdminIngestInstrument: POST /api/admin/instruments/:symbol/ingest, jalankan ulang backfill.
func (a *App) AdminIngestInstrument(c echo.Context) error {
	symbol := strings.ToUpper(c.Param("symbol"))
	assetType, err := a.store.InstrumentAssetType(c.Request().Context(), symbol)
	if errors.Is(err, ErrNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "instrument tidak ditemukan"})
	}
	if err != nil {
		return a.fail(c, err)
	}
	if !a.ingest.start(symbol, assetType) {
		return c.JSON(http.StatusConflict, map[string]string{"error": "Backfill " + symbol + " sedang berjalan."})
	}
	return c.JSON(http.StatusAccepted, map[string]any{"symbol": symbol, "ingest_started": true})
}

type instrumentJob struct {
	Status  string    `json:"status"` // ok | fail
	At      time.Time `json:"at"`
	Rows    int       `json:"rows"`
	Message *string   `json:"message"`
}

type adminInstrument struct {
	ID            int                       `json:"id"`
	Symbol        string                    `json:"symbol"`
	DisplaySymbol string                    `json:"display_symbol"`
	Name          string                    `json:"name"`
	AssetType     string                    `json:"asset_type"`
	Source        string                    `json:"source"`
	Candles1h     int                       `json:"candles_1h"`
	Last1h        *time.Time                `json:"last_1h"`
	Candles1d     int                       `json:"candles_1d"`
	Last1d        *time.Time                `json:"last_1d"`
	Jobs          map[string]*instrumentJob `json:"jobs"` // ingest_1h | ingest_1d | fundamentals: eksekusi terakhir
	Ingesting     bool                      `json:"ingesting"`
	LivePrice     *bool                     `json:"live_price"` // nil = tidak relevan (saham tanpa feed live)
	SigmaRef      *bool                     `json:"sigma_ref"`  // nil = kondisi ini tidak memakai TTL dinamis
}

// AdminInstruments: GET /api/admin/instruments, daftar instrumen + status data dan backfill.
func (a *App) AdminInstruments(c echo.Context) error {
	ctx := c.Request().Context()
	list, err := a.store.AdminInstruments(ctx)
	if err != nil {
		return a.fail(c, err)
	}
	dyn := a.dynamicTTL()
	counts := map[string]int{}
	for _, in := range list {
		counts[in.AssetType]++
		in.DisplaySymbol, _ = displayOf(in.Symbol, in.AssetType)
		in.Ingesting = a.ingest.running(in.Symbol)
		if in.Source != "yahoo" && a.prices != nil {
			live := a.prices.Tracks(in.Symbol)
			in.LivePrice = &live
		}
		if dyn != nil {
			known := dyn.ref[in.Symbol] > 0
			in.SigmaRef = &known
		}
	}
	return c.JSON(http.StatusOK, map[string]any{
		"instruments": list, "counts": counts, "price_feed": a.prices != nil, "cache_mode": a.policy.Name(),
	})
}

// dynamicTTL: model TTL dinamis kondisi ini, nil kalau kondisi tidak memakainya.
func (a *App) dynamicTTL() *DynamicTTL {
	if rp, ok := a.policy.(*RedisPolicy); ok {
		return rp.dyn
	}
	return nil
}

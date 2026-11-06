package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestNormalizeSymbol(t *testing.T) {
	ok := []struct{ assetType, raw, want string }{
		{"stock", "bbca", "BBCA.JK"},
		{"stock", " BBRI.JK ", "BBRI.JK"},
		{"crypto", "sol", "SOLUSDT"},
		{"crypto", "SOL/USDT", "SOLUSDT"},
		{"crypto", "1000pepe-usdt", "1000PEPEUSDT"},
		{"commodity", "xau", "XAUUSDT"},
	}
	for _, c := range ok {
		if got, err := normalizeSymbol(c.assetType, c.raw); err != nil || got != c.want {
			t.Errorf("normalizeSymbol(%q, %q) = %q, %v; mau %q", c.assetType, c.raw, got, err, c.want)
		}
	}
	bad := [][2]string{{"stock", "B"}, {"stock", "BBCA.JK;DROP"}, {"crypto", "USDT"}, {"crypto", "SO$L"}, {"bond", "X"}}
	for _, c := range bad {
		var le *lookupError
		if _, err := normalizeSymbol(c[0], c[1]); !errors.As(err, &le) || le.status != http.StatusBadRequest {
			t.Errorf("normalizeSymbol(%q, %q) harus 400, dapat %v", c[0], c[1], err)
		}
	}
}

func TestCleanStockName(t *testing.T) {
	for in, want := range map[string]string{
		"PT Bank Central Asia Tbk":          "Bank Central Asia",
		"PT Aneka Tambang Tbk.":             "Aneka Tambang",
		"PT Telkom Indonesia (Persero) Tbk": "Telkom Indonesia",
		"Merdeka Copper Gold":               "Merdeka Copper Gold",
	} {
		if got := cleanStockName(in); got != want {
			t.Errorf("cleanStockName(%q) = %q, mau %q", in, got, want)
		}
	}
}

// fakeSources meniru Yahoo chart, Binance spot, dan Binance futures dengan bentuk respons asli
// (dicek 6 Nov 2026: komoditas = underlyingType COMMODITY, contractType TRADIFI_PERPETUAL).
func fakeSources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, q := r.URL.Path, r.URL.Query().Get("symbol")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(p, "/chart/"):
			switch strings.TrimPrefix(p, "/chart/") {
			case "BBCA.JK":
				w.Write([]byte(`{"chart":{"result":[{"meta":{"currency":"IDR","exchangeName":"JKT","instrumentType":"EQUITY","longName":"PT Bank Central Asia Tbk","regularMarketPrice":5200}}],"error":null}}`))
			case "AAPL.JK": // anggap terdaftar tetapi bukan IDX
				w.Write([]byte(`{"chart":{"result":[{"meta":{"currency":"USD","exchangeName":"NMS","instrumentType":"EQUITY"}}],"error":null}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"chart":{"result":null,"error":{"code":"Not Found","description":"No data found"}}}`))
			}
		case p == "/spot/exchangeInfo":
			if q == "PEPEUSDT" {
				w.Write([]byte(`{"symbols":[{"symbol":"PEPEUSDT","status":"TRADING","baseAsset":"PEPE","quoteAsset":"USDT"}]}`))
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"code":-1121,"msg":"Invalid symbol."}`))
		case strings.HasSuffix(p, "/ticker/price"):
			w.Write([]byte(`{"symbol":"` + q + `","price":"1.2300"}`))
		case p == "/fapi/exchangeInfo":
			w.Write([]byte(`{"symbols":[
				{"symbol":"XAUUSDT","status":"TRADING","contractType":"TRADIFI_PERPETUAL","baseAsset":"XAU","quoteAsset":"USDT","underlyingType":"COMMODITY"},
				{"symbol":"BTCUSDT","status":"TRADING","contractType":"PERPETUAL","baseAsset":"BTC","quoteAsset":"USDT","underlyingType":"COIN"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("YAHOO_CHART_URL", srv.URL+"/chart")
	t.Setenv("BINANCE_URL", srv.URL+"/spot/klines")
	t.Setenv("BINANCE_FUTURES_URL", srv.URL+"/fapi/klines")
}

func TestLookupSource(t *testing.T) {
	fakeSources(t)
	ctx := context.Background()

	c, err := lookupSource(ctx, "stock", "bbca")
	if err != nil || c.Symbol != "BBCA.JK" || c.Name != "Bank Central Asia" || c.Source != "yahoo" || c.Price == nil {
		t.Fatalf("saham IDX: %+v %v", c, err)
	}
	c, err = lookupSource(ctx, "crypto", "pepe")
	if err != nil || c.Symbol != "PEPEUSDT" || c.Source != "binance" || c.Price == nil || *c.Price != 1.23 {
		t.Fatalf("kripto: %+v %v", c, err)
	}
	c, err = lookupSource(ctx, "commodity", "XAU")
	if err != nil || c.Source != "binance_futures" || c.AssetType != "commodity" {
		t.Fatalf("komoditas: %+v %v", c, err)
	}

	notFound := []struct{ assetType, raw, contains string }{
		{"stock", "ZZZZ", "tidak ditemukan"},
		{"stock", "AAPL", "bukan saham Bursa Efek Indonesia"},
		{"crypto", "ZZZZ", "tidak ada di Binance spot"},
		{"commodity", "BTC", "Kripto"}, // kontrak kripto di futures bukan komoditas
		{"commodity", "OIL", "tidak ada di Binance"},
	}
	for _, n := range notFound {
		_, err := lookupSource(ctx, n.assetType, n.raw)
		var le *lookupError
		if !errors.As(err, &le) || le.status != http.StatusNotFound || !strings.Contains(le.msg, n.contains) {
			t.Errorf("%s %s: mau 404 berisi %q, dapat %v", n.assetType, n.raw, n.contains, err)
		}
	}
}

func TestLookupSourceUnreachable(t *testing.T) {
	t.Setenv("BINANCE_URL", "http://127.0.0.1:1/spot/klines") // port tertutup
	_, err := lookupSource(context.Background(), "crypto", "sol")
	var le *lookupError
	if !errors.As(err, &le) || le.status != http.StatusBadGateway {
		t.Fatalf("sumber mati harus 502, dapat %v", err)
	}
}

func TestIngestRunner(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	release := make(chan struct{})
	r := &ingestRunner{busy: map[string]bool{}, exec: func(_ context.Context, script string, args ...string) error {
		<-release
		mu.Lock()
		calls = append(calls, script+" "+strings.Join(args, " "))
		mu.Unlock()
		return nil
	}}
	if !r.start("BBCA.JK", "stock") {
		t.Fatal("start pertama harus jalan")
	}
	if r.start("BBCA.JK", "stock") || !r.running("BBCA.JK") {
		t.Fatal("start kedua saat berjalan harus ditolak")
	}
	close(release)
	r.wg.Wait()
	if r.running("BBCA.JK") {
		t.Fatal("setelah selesai tidak boleh berstatus berjalan")
	}
	want := []string{"ingest.py --timeframe 1h --symbols BBCA.JK", "ingest.py --timeframe 1d --symbols BBCA.JK",
		"fundamentals.py --symbols BBCA.JK"}
	if !slices.Equal(calls, want) {
		t.Fatalf("langkah backfill saham: %v", calls)
	}
	if steps := ingestSteps("SOLUSDT", "crypto"); len(steps) != 2 {
		t.Fatalf("kripto tanpa fundamental: %v", steps)
	}
	if jobOf(ingestSteps("X", "stock")[2]) != "fundamentals" || jobOf(ingestSteps("X", "stock")[0]) != "ingest_1h" {
		t.Fatal("nama job harus sama dengan yang ditulis skrip Python")
	}
}

// Uji ke sumber asli (internet). Hanya jalan dengan TA_LIVE_LOOKUP=1:
//
//	TA_LIVE_LOOKUP=1 go test -run LookupSourceLive -v
func TestLookupSourceLive(t *testing.T) {
	if env("TA_LIVE_LOOKUP", "") != "1" {
		t.Skip("set TA_LIVE_LOOKUP=1 untuk menguji ke Yahoo dan Binance asli")
	}
	ctx := context.Background()
	for _, c := range [][2]string{{"stock", "BBCA"}, {"crypto", "PEPE"}, {"commodity", "XAU"}} {
		got, err := lookupSource(ctx, c[0], c[1])
		if err != nil {
			t.Errorf("%s %s: %v", c[0], c[1], err)
			continue
		}
		t.Logf("%s %s -> %s %q %s harga %v", c[0], c[1], got.Symbol, got.Name, got.Exchange, *got.Price)
	}
	for _, c := range [][2]string{{"stock", "ZZZZ"}, {"crypto", "ZZZZ"}, {"commodity", "BTC"}} {
		if _, err := lookupSource(ctx, c[0], c[1]); err == nil {
			t.Errorf("%s %s seharusnya ditolak", c[0], c[1])
		} else {
			t.Logf("%s %s ditolak: %v", c[0], c[1], err)
		}
	}
}

package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Handshake WebSocket yang ditolak (403 dengan halaman error kecil, seperti CDN Binance) tidak boleh
// membuat percobaan berikutnya memakai ulang koneksi TCP yang sama: setiap percobaan harus koneksi baru.
func TestPriceFeedRedialUsesFreshConnection(t *testing.T) {
	var conns, reqs atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs.Add(1)
		http.Error(w, "403 ERROR: the request could not be satisfied", http.StatusForbidden)
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()

	f := &PriceFeed{}
	m := &marketStream{name: "futures"}
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/market/stream?streams=xauusdt@aggTrade"
	for i := 0; i < 3; i++ {
		if err := f.read(context.Background(), m, url); err == nil || !strings.Contains(err.Error(), "403") {
			t.Fatalf("percobaan %d: err = %v, mau 403", i+1, err)
		}
	}
	if reqs.Load() != 3 || conns.Load() != 3 {
		t.Fatalf("3 percobaan: %d request lewat %d koneksi TCP, mau 3 koneksi (tanpa pakai ulang)", reqs.Load(), conns.Load())
	}
}

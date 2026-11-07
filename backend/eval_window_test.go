package main

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

func TestEvalWindow(t *testing.T) {
	now := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	ctx := func(query string) echo.Context {
		return echo.New().NewContext(httptest.NewRequest("GET", "/api/admin/evaluation?"+query, nil), httptest.NewRecorder())
	}
	ts := func(s string) time.Time {
		v, _ := time.Parse(time.RFC3339, s)
		return v
	}

	// tanpa from: days terakhir sampai sekarang (perilaku lama)
	from, to, err := evalWindow(ctx(""), 7, now)
	if err != nil || !to.Equal(now) || !from.Equal(now.AddDate(0, 0, -7)) {
		t.Fatalf("default = %v..%v %v", from, to, err)
	}
	// jendela tetap satu run benchmark
	from, to, err = evalWindow(ctx("from=2026-11-09T02:00:00Z&to=2026-11-16T02:00:00Z"), 30, now)
	if err != nil || !from.Equal(ts("2026-11-09T02:00:00Z")) || !to.Equal(ts("2026-11-16T02:00:00Z")) {
		t.Fatalf("tetap = %v..%v %v", from, to, err)
	}
	// "+" offset yang tidak di-encode sampai sebagai spasi
	from, _, err = evalWindow(ctx("from=2026-11-09T09:00:00+07:00"), 30, now)
	if err != nil || !from.Equal(ts("2026-11-09T02:00:00Z")) {
		t.Fatalf("offset = %v %v", from, err)
	}
	for _, q := range []string{"from=kemarin", "to=2026-13-01", "from=2026-11-16T02:00:00Z&to=2026-11-09T02:00:00Z"} {
		if _, _, err := evalWindow(ctx(q), 30, now); err == nil {
			t.Errorf("%s harus ditolak", q)
		}
	}
}

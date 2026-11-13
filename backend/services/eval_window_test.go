package services

import (
	"testing"
	"time"
)

func TestEvalWindow(t *testing.T) {
	now := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	ts := func(s string) time.Time {
		v, _ := time.Parse(time.RFC3339, s)
		return v
	}

	// tanpa from: days terakhir sampai sekarang (perilaku lama)
	from, to, err := evalWindow("", "", 7, now)
	if err != nil || !to.Equal(now) || !from.Equal(now.AddDate(0, 0, -7)) {
		t.Fatalf("default = %v..%v %v", from, to, err)
	}
	// jendela tetap satu run benchmark
	from, to, err = evalWindow("2026-11-09T02:00:00Z", "2026-11-16T02:00:00Z", 30, now)
	if err != nil || !from.Equal(ts("2026-11-09T02:00:00Z")) || !to.Equal(ts("2026-11-16T02:00:00Z")) {
		t.Fatalf("tetap = %v..%v %v", from, to, err)
	}
	// "+" offset yang tidak di-encode sampai sebagai spasi
	from, _, err = evalWindow("2026-11-09T09:00:00 07:00", "", 30, now)
	if err != nil || !from.Equal(ts("2026-11-09T02:00:00Z")) {
		t.Fatalf("offset = %v %v", from, err)
	}
	for _, q := range [][2]string{{"kemarin", ""}, {"", "2026-13-01"}, {"2026-11-16T02:00:00Z", "2026-11-09T02:00:00Z"}} {
		if _, _, err := evalWindow(q[0], q[1], 30, now); err == nil {
			t.Errorf("from=%s to=%s harus ditolak", q[0], q[1])
		}
	}
}

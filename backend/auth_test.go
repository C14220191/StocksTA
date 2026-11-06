package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

func TestAdminTokenSignVerify(t *testing.T) {
	a := newAdminAuthWith("rahasia", 12)
	tok, exp := a.issue()
	if got, ok := a.verify(tok); !ok || !got.Equal(exp) {
		t.Fatalf("token baru harus sah: ok=%v exp=%v got=%v", ok, exp, got)
	}
	// tanda tangan diubah, payload diubah (perpanjang masa berlaku), kata sandi lain, sampah
	bad := []string{tok[:len(tok)-2] + "xx", strings.Replace(tok, "v1.", "v1.9", 1), "v1.123", "", "Bearer " + tok}
	for _, b := range bad {
		if _, ok := a.verify(b); ok {
			t.Errorf("token %q seharusnya ditolak", b)
		}
	}
	if _, ok := newAdminAuthWith("lain", 12).verify(tok); ok {
		t.Error("token dari kata sandi lain seharusnya ditolak")
	}
	a.now = func() time.Time { return exp.Add(time.Second) }
	if _, ok := a.verify(tok); ok {
		t.Error("token kedaluwarsa seharusnya ditolak")
	}
	if off := newAdminAuthWith("", 12); off.enabled() || off.checkPassword("") {
		t.Error("tanpa ADMIN_PASSWORD admin harus mati dan kata sandi kosong ditolak")
	}
}

func TestAdminLoginThrottle(t *testing.T) {
	a := newAdminAuthWith("rahasia", 12)
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	for range loginFailMax {
		if a.throttled() {
			t.Fatal("belum boleh terkunci")
		}
		a.recordFail()
	}
	if !a.throttled() {
		t.Fatal("setelah batas percobaan gagal harus terkunci")
	}
	now = now.Add(loginFailWindow + time.Second)
	if a.throttled() {
		t.Fatal("setelah jendela lewat harus terbuka lagi")
	}
}

// adminServer: rute login + satu endpoint terlindungi, sama seperti main.go.
func adminServer(password string) *echo.Echo {
	app := &App{auth: newAdminAuthWith(password, 12)}
	e := echo.New()
	e.POST("/api/admin/login", app.AdminLogin)
	adm := e.Group("/api/admin", app.requireAdmin)
	adm.GET("/session", app.AdminSession)
	return e
}

func do(e *echo.Echo, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestAdminEndpointsRequireLogin(t *testing.T) {
	e := adminServer("rahasia")
	if rec := do(e, http.MethodGet, "/api/admin/session", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("tanpa token: %d, harus 401", rec.Code)
	}
	if rec := do(e, http.MethodGet, "/api/admin/session", "", "v1.1.abc"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token palsu: %d, harus 401", rec.Code)
	}
	if rec := do(e, http.MethodPost, "/api/admin/login", `{"password":"salah"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("kata sandi salah: %d, harus 401", rec.Code)
	}
	rec := do(e, http.MethodPost, "/api/admin/login", `{"password":"rahasia"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login benar: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Token == "" {
		t.Fatalf("respons login: %v %s", err, rec.Body)
	}
	if rec := do(e, http.MethodGet, "/api/admin/session", "", got.Token); rec.Code != http.StatusOK {
		t.Fatalf("dengan token sah: %d, harus 200", rec.Code)
	}
	// endpoint admin yang tidak ada tetap tertutup tanpa token (tidak membocorkan daftar rute)
	if rec := do(e, http.MethodGet, "/api/admin/tidak-ada", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("rute tak dikenal tanpa token: %d, harus 401", rec.Code)
	}
}

func TestAdminDisabledWithoutPassword(t *testing.T) {
	e := adminServer("")
	if rec := do(e, http.MethodPost, "/api/admin/login", `{"password":""}`, ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("login tanpa ADMIN_PASSWORD: %d, harus 503", rec.Code)
	}
	if rec := do(e, http.MethodGet, "/api/admin/session", "", "apa saja"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("endpoint tanpa ADMIN_PASSWORD: %d, harus 503", rec.Code)
	}
}

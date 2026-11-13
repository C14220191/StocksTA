package controllers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"ta-backend/services"
)

// Tes HTTP tanpa database: redirect / cookie login Google, middleware sesi, kunci login data.

func readyAuth(ready bool, opts services.AuthOptions) *services.AuthService {
	a := services.NewAuthServiceWith(nil, opts)
	a.SetReady(ready)
	return a
}

func TestGoogleStartAndStateCheck(t *testing.T) {
	// token endpoint Google palsu yang selalu menolak code (tukar code gagal)
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer tokenSrv.Close()
	g := services.NewGoogleOAuth("client-123.apps.googleusercontent.com", "rahasia", "http://localhost:8080/api/auth/google/callback")
	g.AuthURL, g.TokenURL = "https://accounts.google.test/auth", tokenSrv.URL
	h := &Controller{Auth: readyAuth(true, services.AuthOptions{AppURL: "http://localhost:5173", Google: g})}
	e := echo.New()
	e.GET("/api/auth/google/start", h.GoogleStart)
	e.GET("/api/auth/google/callback", h.GoogleCallback)

	// host UI (127.0.0.1) beda dengan host redirect URL (localhost) -> pindah host dulu
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/auth/google/start?next=/admin", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || !strings.HasPrefix(loc, "http://localhost:8080/api/auth/google/start?") {
		t.Fatalf("pindah host: %d %s", rec.Code, loc)
	}

	req = httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/auth/google/start?next=//jahat.id", nil)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	loc, _ := url.Parse(rec.Header().Get("Location"))
	q := loc.Query()
	if rec.Code != http.StatusFound || loc.Host != "accounts.google.test" || q.Get("code_challenge_method") != "S256" ||
		q.Get("code_challenge") == "" || q.Get("nonce") == "" || q.Get("redirect_uri") != g.RedirectURL() {
		t.Fatalf("redirect ke Google: %d %s", rec.Code, loc)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == oauthCookie {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value != q.Get("state") || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie state: %+v", cookie)
	}

	callback := func(query string, c *http.Cookie) string {
		req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/api/auth/google/callback?"+query, nil)
		if c != nil {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec.Header().Get("Location")
	}
	state := q.Get("state")
	// state benar tetapi cookie dari browser lain / tidak ada (login CSRF)
	if loc := callback("state="+state+"&code=x", nil); !strings.HasSuffix(loc, "/login?error=google_state") {
		t.Errorf("tanpa cookie: %s", loc)
	}
	if loc := callback("state="+state+"&code=x", &http.Cookie{Name: oauthCookie, Value: "lain"}); !strings.HasSuffix(loc, "error=google_state") {
		t.Errorf("cookie beda: %s", loc)
	}
	if loc := callback("error=access_denied&state="+state, cookie); !strings.HasSuffix(loc, "error=google_cancelled") {
		t.Errorf("dibatalkan: %s", loc)
	}
	// state + cookie benar, tukar code ditolak Google -> google_failed; state ikut terpakai
	if loc := callback("state="+state+"&code=x", cookie); !strings.HasSuffix(loc, "error=google_failed") {
		t.Errorf("tukar code gagal: %s", loc)
	}
	// state sekali pakai: tidak bisa diulang (kedaluwarsa diuji di services.TestGoogleBeginTake)
	if loc := callback("state="+state+"&code=x", cookie); !strings.HasSuffix(loc, "error=google_state") {
		t.Errorf("state dipakai ulang: %s", loc)
	}
}

func TestAuthMiddlewareWithoutSession(t *testing.T) {
	auth := readyAuth(false, services.AuthOptions{})
	h := &Controller{Auth: auth}
	e := echo.New()
	adm := e.Group("/api/admin", h.RequireAdmin)
	adm.GET("/overview", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })
	e.GET("/api/auth/me", h.Me, h.RequireUser)

	get := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}
	// migrasi 011 belum dijalankan: 503 dengan petunjuk, bukan terbuka
	if rec := get("/api/admin/overview", "apa-saja"); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "011_users.sql") {
		t.Fatalf("belum migrasi: %d %s", rec.Code, rec.Body)
	}
	auth.SetReady(true)
	for _, path := range []string{"/api/admin/overview", "/api/admin/tidak-ada", "/api/auth/me"} {
		if rec := get(path, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s tanpa token: %d, harus 401", path, rec.Code)
		}
	}
}

func TestDataRoutesRequireLogin(t *testing.T) {
	get := func(e *echo.Echo, path string) int {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code
	}
	paths := []string{"/api/analyze/BTCUSDT?method=debate", "/api/market/overview", "/api/search?q=", "/api/instruments/BTCUSDT",
		"/api/instruments/BTCUSDT/candles"}

	auth := readyAuth(true, services.AuthOptions{})
	locked := &Controller{Auth: auth}
	e := echo.New()
	locked.registerDataRoutes(e, true)
	for _, p := range paths {
		if code := get(e, p); code != http.StatusUnauthorized {
			t.Errorf("%s tanpa login: %d, harus 401", p, code)
		}
	}
	auth.SetReady(false) // migrasi 011 belum dijalankan: tertutup dengan petunjuk, bukan terbuka
	if code := get(e, "/api/search?q="); code != http.StatusServiceUnavailable {
		t.Errorf("belum migrasi: %d, harus 503", code)
	}

	// instans lab (REQUIRE_LOGIN=off): langsung ke handler, tanpa sesi
	lab := &Controller{Auth: readyAuth(false, services.AuthOptions{}), Market: services.NewMarketService(nil)}
	e = echo.New()
	lab.registerDataRoutes(e, false)
	if code := get(e, "/api/search?q="); code != http.StatusOK {
		t.Errorf("lab tanpa login: %d, harus 200", code)
	}
}

// Di belakang Tailscale Funnel (proxy di 127.0.0.1) IP klien = IP paling kanan di X-Forwarded-For yang
// ditambahkan proxy; X-Forwarded-For palsu dari klien tidak bisa dipakai menghindari pembatas per IP.
func TestClientIPBehindProxy(t *testing.T) {
	e := echo.New()
	e.IPExtractor = echo.ExtractIPFromXFFHeader() // sama dengan main.go
	ip := func(remote, xff string) string {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remote
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		return e.NewContext(req, httptest.NewRecorder()).RealIP()
	}
	cases := []struct{ remote, xff, want string }{
		{"127.0.0.1:50000", "203.0.113.9", "203.0.113.9"},          // lewat proxy lokal
		{"127.0.0.1:50000", "9.9.9.9, 203.0.113.9", "203.0.113.9"}, // klien memalsukan XFF, proxy menambahkan IP asli
		{"198.51.100.7:4000", "9.9.9.9", "198.51.100.7"},           // tanpa proxy: XFF dari klien diabaikan
		{"127.0.0.1:50000", "", "127.0.0.1"},                       // SSH tunnel: tanpa XFF
	}
	for _, c := range cases {
		if got := ip(c.remote, c.xff); got != c.want {
			t.Errorf("remote=%s xff=%q: %s, mau %s", c.remote, c.xff, got, c.want)
		}
	}
}

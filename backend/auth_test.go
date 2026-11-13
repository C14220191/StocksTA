package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

func TestEmailFormatError(t *testing.T) {
	valid := []string{"nama@gmail.com", "a.b+tag@kampus.ac.id", "x_y-z@sub.domain.co.id", "user@xn--80ak6aa92e.com", "o'neil@mail.id"}
	for _, e := range valid {
		if msg := emailFormatError(e); msg != "" {
			t.Errorf("%q seharusnya valid, dapat %q", e, msg)
		}
	}
	invalid := map[string]string{
		"":                                "wajib diisi",
		"namagmail.com":                   "tepat satu tanda @",
		"a@@gmail.com":                    "tepat satu tanda @",
		"@gmail.com":                      "sebelum @ kosong",
		"nama@":                           "setelah @ kosong",
		".nama@gmail.com":                 "Titik sebelum @",
		"na..ma@gmail.com":                "Titik sebelum @",
		"na ma@gmail.com":                 "tidak diizinkan",
		"ñame@gmail.com":                  "tidak diizinkan",
		"nama@gmail":                      "tidak lengkap",
		"nama@gmail..com":                 "titik di awal",
		"nama@-gmail.com":                 "tidak valid",
		"nama@gm_ail.com":                 "karakter yang tidak diizinkan",
		"nama@gmail.c":                    "Akhiran domain",
		"nama@gmail.123":                  "Akhiran domain",
		strings.Repeat("a", 65) + "@x.id": "terlalu panjang",
	}
	for e, want := range invalid {
		if msg := emailFormatError(e); !strings.Contains(msg, want) {
			t.Errorf("%q: pesan %q, harus memuat %q", e, msg, want)
		}
	}
}

func TestPasswordAndNameRules(t *testing.T) {
	cases := map[string]string{
		"":                         "wajib diisi",
		"abc12":                    "minimal 8",
		"abcdefgh":                 "huruf dan angka",
		"12345678":                 "huruf dan angka",
		strings.Repeat("a1", 37):   "maksimal 72",
		"budi2026":                 "sama dengan email",
		"rahasia-aman-2026":        "",
		"kata sandi dengan spasi9": "",
	}
	for pw, want := range cases {
		msg := passwordError(pw, "budi2026@gmail.com")
		if want == "" && msg != "" || want != "" && !strings.Contains(msg, want) {
			t.Errorf("password %q: %q, mau %q", pw, msg, want)
		}
	}
	f := fieldErrors{}
	checkNewPassword(f, "rahasia-aman-2026", "rahasia-lain-2026", "")
	if !strings.Contains(f["password_confirm"], "tidak sama") || f["password"] != "" {
		t.Errorf("konfirmasi beda: %v", f)
	}
	if nameError("") == "" || nameError("A") == "" || nameError(strings.Repeat("a", 81)) == "" || nameError("Budi") != "" {
		t.Error("aturan nama")
	}
}

func TestFailLimiter(t *testing.T) {
	l := newFailLimiter(3, 15*time.Minute)
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	for range 3 {
		if l.blocked("a@x.id") {
			t.Fatal("belum boleh terkunci")
		}
		l.fail("a@x.id")
	}
	if !l.blocked("a@x.id") || l.blocked("b@x.id") {
		t.Fatal("hanya email yang gagal yang terkunci")
	}
	now = now.Add(16 * time.Minute)
	if l.blocked("a@x.id") {
		t.Fatal("setelah jendela lewat harus terbuka lagi")
	}
	l.fail("a@x.id")
	l.reset("a@x.id")
	if len(l.fails) != 0 {
		t.Fatal("reset menghapus hitungan")
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/admin/users": "/admin/users", "/detail/BTCUSDT?x=1": "/detail/BTCUSDT?x=1",
		"": "/", "https://jahat.id": "/", "//jahat.id": "/", "/\\jahat.id": "/", "admin": "/", "/a\r\nb": "/",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, mau %q", in, got, want)
		}
	}
}

func TestBuildMessage(t *testing.T) {
	from, _ := mail.ParseAddress("StocksTA <noreply@kampus.ac.id>")
	msg, err := buildMessage(from, "budi@gmail.com", "Verifikasi email akun StocksTA", "Halo Budi,\n\nhttps://x.id/verify-email?token=abc=def\n",
		time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	s := string(msg)
	for _, want := range []string{"From: \"StocksTA\" <noreply@kampus.ac.id>\r\n", "To: budi@gmail.com\r\n",
		"Content-Transfer-Encoding: quoted-printable", "Message-ID: <", "@kampus.ac.id>", "token=3Dabc=3Ddef"} {
		if !strings.Contains(s, want) {
			t.Errorf("email tidak memuat %q:\n%s", want, s)
		}
	}
	if _, err := buildMessage(from, "a@b.id\r\nBcc: x@y.id", "s", "b", time.Now()); err == nil {
		t.Error("header injection lewat alamat tujuan harus ditolak")
	}
}

// ---- Google ----

type fakeGoogle struct {
	key  *rsa.PrivateKey
	kid  string
	srv  *httptest.Server
	g    *googleOAuth
	hits int
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeGoogle{key: key, kid: "kunci-1"}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits++
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": f.kid, "kty": "RSA", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(f.srv.Close)
	f.g = newGoogleOAuthWith("client-123.apps.googleusercontent.com", "rahasia", "http://localhost:8080/api/auth/google/callback")
	f.g.jwksURL = f.srv.URL
	return f
}

func (f *fakeGoogle) sign(t *testing.T, header, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	input := enc(header) + "." + enc(claims)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *fakeGoogle) claims(mod func(map[string]any)) map[string]any {
	now := time.Now().Unix()
	c := map[string]any{
		"iss": "https://accounts.google.com", "aud": f.g.clientID, "azp": f.g.clientID, "sub": "1098765",
		"email": "Budi@Gmail.com", "email_verified": true, "name": "Budi", "nonce": "nonce-abc",
		"iat": now, "exp": now + 3600,
	}
	if mod != nil {
		mod(c)
	}
	return c
}

func TestGoogleIDTokenVerification(t *testing.T) {
	f := newFakeGoogle(t)
	ctx := context.Background()
	hdr := map[string]any{"alg": "RS256", "kid": f.kid, "typ": "JWT"}

	cl, err := f.g.verifyIDToken(ctx, f.sign(t, hdr, f.claims(nil)), "nonce-abc")
	if err != nil || cl.Sub != "1098765" || !bool(cl.EmailVerified) || cl.Email != "Budi@Gmail.com" {
		t.Fatalf("token sah ditolak: %v %+v", err, cl)
	}
	// email_verified sebagai string "true" (format lama) tetap diterima
	if cl, err := f.g.verifyIDToken(ctx, f.sign(t, hdr, f.claims(func(c map[string]any) { c["email_verified"] = "true" })), "nonce-abc"); err != nil || !bool(cl.EmailVerified) {
		t.Fatalf("email_verified string: %v", err)
	}

	bad := map[string]string{
		"aud lain":    f.sign(t, hdr, f.claims(func(c map[string]any) { c["aud"] = "aplikasi-lain" })),
		"iss lain":    f.sign(t, hdr, f.claims(func(c map[string]any) { c["iss"] = "https://jahat.id" })),
		"kedaluwarsa": f.sign(t, hdr, f.claims(func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() })),
		"nonce lain":  f.sign(t, hdr, f.claims(func(c map[string]any) { c["nonce"] = "nonce-penyerang" })),
		"tanpa sub":   f.sign(t, hdr, f.claims(func(c map[string]any) { delete(c, "sub") })),
		"iat depan":   f.sign(t, hdr, f.claims(func(c map[string]any) { c["iat"] = time.Now().Add(time.Hour).Unix() })),
		"alg HS256":   f.sign(t, map[string]any{"alg": "HS256", "kid": f.kid}, f.claims(nil)),
		"kid asing":   f.sign(t, map[string]any{"alg": "RS256", "kid": "kunci-lain"}, f.claims(nil)),
		"bukan JWT":   "abc.def",
		"aud array tanpa azp": f.sign(t, hdr, f.claims(func(c map[string]any) {
			c["aud"] = []string{f.g.clientID, "lain"}
			c["azp"] = "lain"
		})),
	}
	for name, tok := range bad {
		if _, err := f.g.verifyIDToken(ctx, tok, "nonce-abc"); err == nil {
			t.Errorf("%s: seharusnya ditolak", name)
		}
	}
	// payload diubah setelah ditandatangani (mis. email diganti) -> tanda tangan tidak sah
	good := f.sign(t, hdr, f.claims(nil))
	parts := strings.Split(good, ".")
	forged, _ := json.Marshal(f.claims(func(c map[string]any) { c["email"] = "korban@gmail.com" }))
	parts[1] = base64.RawURLEncoding.EncodeToString(forged)
	if _, err := f.g.verifyIDToken(ctx, strings.Join(parts, "."), "nonce-abc"); err == nil || !strings.Contains(err.Error(), "tanda tangan") {
		t.Errorf("payload palsu: %v", err)
	}
	// JWKS di-cache; kid asing hanya memicu satu kali ambil ulang per menit
	if f.hits > 2 {
		t.Errorf("JWKS diambil %d kali, seharusnya di-cache", f.hits)
	}
}

func googleTestApp(g *googleOAuth) (*App, *echo.Echo) {
	app := &App{auth: &authService{cfg: authConfig{appURL: "http://localhost:5173"}, google: g, ready: true, now: time.Now,
		logins: newFailLimiter(loginFailMax, loginFailWindow), managed: map[string]bool{}}}
	e := echo.New()
	e.GET("/api/auth/google/start", app.GoogleStart)
	e.GET("/api/auth/google/callback", app.GoogleCallback)
	return app, e
}

func TestGoogleStartAndStateCheck(t *testing.T) {
	f := newFakeGoogle(t)
	f.g.authURL = "https://accounts.google.test/auth"
	_, e := googleTestApp(f.g)

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
		q.Get("code_challenge") == "" || q.Get("nonce") == "" || q.Get("redirect_uri") != f.g.redirectURL {
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
	if p := f.g.pending[q.Get("state")]; p.next != "/" {
		t.Errorf("next ke domain luar harus jadi \"/\", dapat %q", p.next)
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
	// state sekali pakai: setelah dipakai (atau kedaluwarsa) tidak bisa diulang
	f.g.pending[state] = oauthPending{exp: time.Now().Add(-time.Second)}
	if loc := callback("state="+state+"&code=x", cookie); !strings.HasSuffix(loc, "error=google_state") {
		t.Errorf("state kedaluwarsa: %s", loc)
	}
}

func TestLoginCodeSingleUse(t *testing.T) {
	g := newGoogleOAuthWith("id", "secret", "http://localhost:8080/api/auth/google/callback")
	code := g.issueCode(42, "/admin")
	if lc, ok := g.redeem(code); !ok || lc.userID != 42 || lc.next != "/admin" {
		t.Fatalf("redeem pertama: %v %+v", ok, lc)
	}
	if _, ok := g.redeem(code); ok {
		t.Fatal("kode login hanya sekali pakai")
	}
	code = g.issueCode(42, "/")
	g.now = func() time.Time { return time.Now().Add(loginCodeTTL + time.Second) }
	if _, ok := g.redeem(code); ok {
		t.Fatal("kode login kedaluwarsa harus ditolak")
	}
}

// ---- middleware tanpa database ----

func TestAuthMiddlewareWithoutSession(t *testing.T) {
	app := &App{auth: &authService{now: time.Now}}
	e := echo.New()
	adm := e.Group("/api/admin", app.requireAdmin)
	adm.GET("/overview", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })
	e.GET("/api/auth/me", app.Me, app.requireUser)

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
	app.auth.ready = true
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

	locked := &App{auth: &authService{now: time.Now, ready: true}}
	e := echo.New()
	locked.registerDataRoutes(e, true)
	for _, p := range paths {
		if code := get(e, p); code != http.StatusUnauthorized {
			t.Errorf("%s tanpa login: %d, harus 401", p, code)
		}
	}
	locked.auth.ready = false // migrasi 011 belum dijalankan: tertutup dengan petunjuk, bukan terbuka
	if code := get(e, "/api/search?q="); code != http.StatusServiceUnavailable {
		t.Errorf("belum migrasi: %d, harus 503", code)
	}

	// instans lab (REQUIRE_LOGIN=off): langsung ke handler, tanpa sesi
	lab := &App{auth: &authService{now: time.Now}}
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

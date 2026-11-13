package services

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
	"strings"
	"testing"
	"time"
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
	f := FieldErrors{}
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
		if got := SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q, mau %q", in, got, want)
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
	g    *GoogleOAuth
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
	f.g = NewGoogleOAuth("client-123.apps.googleusercontent.com", "rahasia", "http://localhost:8080/api/auth/google/callback")
	f.g.JWKSURL = f.srv.URL
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

func TestLoginCodeSingleUse(t *testing.T) {
	g := NewGoogleOAuth("id", "secret", "http://localhost:8080/api/auth/google/callback")
	code := g.IssueCode(42, "/admin")
	if lc, ok := g.redeem(code); !ok || lc.userID != 42 || lc.next != "/admin" {
		t.Fatalf("redeem pertama: %v %+v", ok, lc)
	}
	if _, ok := g.redeem(code); ok {
		t.Fatal("kode login hanya sekali pakai")
	}
	code = g.IssueCode(42, "/")
	g.now = func() time.Time { return time.Now().Add(loginCodeTTL + time.Second) }
	if _, ok := g.redeem(code); ok {
		t.Fatal("kode login kedaluwarsa harus ditolak")
	}
}

// Data login Google sekali pakai dan berumur (OAuthStateTTL): state yang sudah diambil atau kedaluwarsa
// ditolak. Pemeriksaan cookie dan redirect ada di tes controller.
func TestGoogleBeginTake(t *testing.T) {
	g := NewGoogleOAuth("id", "secret", "http://localhost:8080/api/auth/google/callback")
	state, authURL, err := g.Begin("/admin")
	if err != nil || state == "" || !strings.Contains(authURL, "code_challenge_method=S256") {
		t.Fatalf("begin: %v %s", err, authURL)
	}
	if p, ok := g.take(state); !ok || p.next != "/admin" || p.verifier == "" || p.nonce == "" {
		t.Fatalf("take pertama: %v %+v", ok, p)
	}
	if _, ok := g.take(state); ok {
		t.Fatal("state hanya sekali pakai")
	}
	state, _, _ = g.Begin("/")
	g.now = func() time.Time { return time.Now().Add(OAuthStateTTL + time.Second) }
	if _, ok := g.take(state); ok {
		t.Fatal("state kedaluwarsa harus ditolak")
	}
}

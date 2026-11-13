package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
)

// Tes alur akun terhadap PostgreSQL sungguhan, di schema sementara (dihapus setelah tes; tabel lain
// tidak disentuh). Dilewati kalau TEST_DATABASE_URL kosong, mis.:
//   TEST_DATABASE_URL=postgres://ta_user:...@127.0.0.1:5433/ta_db go test -run DB ./...

type sentMail struct{ to, subject, body string }

type captureMailer struct {
	mu   sync.Mutex
	sent []sentMail
}

func (m *captureMailer) kind() string { return "capture" }

func (m *captureMailer) send(_ context.Context, to, subject, body string) error {
	m.mu.Lock()
	m.sent = append(m.sent, sentMail{to, subject, body})
	m.mu.Unlock()
	return nil
}

var linkRe = regexp.MustCompile(`(/verify-email|/reset-password)\?token=([A-Za-z0-9_-]+)`)

// last: token dari email terakhir ke alamat to dengan path link tertentu.
func (m *captureMailer) last(t *testing.T, to, path string) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.sent) - 1; i >= 0; i-- {
		if mm := linkRe.FindStringSubmatch(m.sent[i].body); m.sent[i].to == to && mm != nil && mm[1] == path {
			return mm[2]
		}
	}
	t.Fatalf("tidak ada email %s ke %s", path, to)
	return ""
}

func (m *captureMailer) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

type authHarness struct {
	t    *testing.T
	app  *App
	e    *echo.Echo
	mail *captureMailer
}

func newAuthHarness(t *testing.T) *authHarness {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL kosong: tes database akun dilewati")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("auth_test_%d", time.Now().UnixNano())
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		conn.Close(context.Background())
	})
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	migration, err := os.ReadFile("../migrations/011_users.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("migrasi 011: %v", err)
	}

	bcryptCost = bcrypt.MinCost
	t.Cleanup(func() { bcryptCost = 12 })
	t.Setenv("ADMIN_EMAIL", "admin@example.com")
	t.Setenv("ADMIN_PASSWORD", "admin-demo-2026")
	t.Setenv("DEMO_USER_EMAIL", "user@example.com")
	t.Setenv("DEMO_USER_PASSWORD", "demo-user-2026")

	mail := &captureMailer{}
	app := &App{store: &Store{pool: pool}, auth: &authService{
		cfg:  authConfig{appName: "StocksTA", appURL: "http://ui.test", sessionTTL: time.Hour, requireVerify: true},
		mail: mail, logins: newFailLimiter(loginFailMax, loginFailWindow), managed: map[string]bool{},
		syncSend: true, now: time.Now,
		checkDomain: func(_ context.Context, d string) string {
			if d == "tidak-ada.test" {
				return "Domain tidak-ada.test tidak ditemukan."
			}
			return ""
		},
	}}
	app.initAuth(ctx)
	if !app.auth.ready {
		t.Fatal("tabel akun tidak terdeteksi setelah migrasi")
	}
	e := echo.New()
	adm := app.registerAuthRoutes(e, func(next echo.HandlerFunc) echo.HandlerFunc { return next })
	adm.GET("/overview", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })
	return &authHarness{t: t, app: app, e: e, mail: mail}
}

func (h *authHarness) call(method, path, token string, body any) (int, map[string]any) {
	h.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if token != "" {
		req.Header.Set(echo.HeaderAuthorization, "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.e.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// expect: status harus want; kalau code diisi, field "code" respons juga harus sama.
func (h *authHarness) expect(method, path, token string, body any, want int, code string) map[string]any {
	h.t.Helper()
	got, out := h.call(method, path, token, body)
	if got != want || (code != "" && out["code"] != code) {
		h.t.Fatalf("%s %s: HTTP %d %v, mau %d %s", method, path, got, out, want, code)
	}
	return out
}

func (h *authHarness) login(email, password string) string {
	h.t.Helper()
	out := h.expect(http.MethodPost, "/api/auth/login", "", map[string]string{"email": email, "password": password}, http.StatusOK, "")
	return out["token"].(string)
}

func fields(out map[string]any) map[string]any {
	f, _ := out["fields"].(map[string]any)
	return f
}

func TestAuthRegisterVerifyLoginResetDB(t *testing.T) {
	h := newAuthHarness(t)
	const pw = "rahasia-budi-2026"

	// validasi: semua kesalahan dikembalikan sekaligus, per field
	out := h.expect(http.MethodPost, "/api/auth/register", "", map[string]string{
		"name": "", "email": "budi@gmail", "password": "pendek", "password_confirm": "lain"}, http.StatusUnprocessableEntity, "")
	for _, k := range []string{"name", "email", "password", "password_confirm"} {
		if fields(out)[k] == nil {
			t.Errorf("field %s tidak dilaporkan: %v", k, out)
		}
	}
	out = h.expect(http.MethodPost, "/api/auth/register", "", map[string]string{
		"name": "Budi", "email": "budi@tidak-ada.test", "password": pw, "password_confirm": pw}, http.StatusUnprocessableEntity, "")
	if !strings.Contains(fmt.Sprint(fields(out)["email"]), "tidak ditemukan") {
		t.Errorf("domain tidak ada: %v", out)
	}

	out = h.expect(http.MethodPost, "/api/auth/register", "", map[string]string{
		"name": " Budi ", "email": " Budi@Gmail.com ", "password": pw, "password_confirm": pw}, http.StatusCreated, "")
	user := out["user"].(map[string]any)
	if user["email"] != "budi@gmail.com" || user["name"] != "Budi" || user["role"] != roleUser || user["email_verified"] != false {
		t.Fatalf("akun baru: %v", user)
	}
	if out["token"] != nil {
		t.Fatal("dengan verifikasi wajib, daftar tidak langsung login")
	}
	h.expect(http.MethodPost, "/api/auth/register", "", map[string]string{
		"name": "Budi 2", "email": "BUDI@gmail.com", "password": pw, "password_confirm": pw}, http.StatusConflict, "email_taken")

	// belum verifikasi: kata sandi salah tetap 401 (tidak membocorkan status), benar -> 403
	h.expect(http.MethodPost, "/api/auth/login", "", map[string]string{"email": "budi@gmail.com", "password": "salah-123"}, http.StatusUnauthorized, "invalid_credentials")
	h.expect(http.MethodPost, "/api/auth/login", "", map[string]string{"email": "budi@gmail.com", "password": pw}, http.StatusForbidden, "email_unverified")
	h.expect(http.MethodPost, "/api/auth/resend-verification", "", map[string]string{"email": "budi@gmail.com"}, http.StatusTooManyRequests, "cooldown")
	// email tak terdaftar: jawaban sama dengan sukses
	h.expect(http.MethodPost, "/api/auth/resend-verification", "", map[string]string{"email": "siapa@gmail.com"}, http.StatusOK, "")

	verify := h.mail.last(t, "budi@gmail.com", "/verify-email")
	h.expect(http.MethodPost, "/api/auth/verify-email", "", map[string]string{"token": "palsu"}, http.StatusBadRequest, "token_invalid")
	out = h.expect(http.MethodPost, "/api/auth/verify-email", "", map[string]string{"token": verify}, http.StatusOK, "")
	if out["email"] != "budi@gmail.com" {
		t.Fatalf("verifikasi: %v", out)
	}
	h.expect(http.MethodPost, "/api/auth/verify-email", "", map[string]string{"token": verify}, http.StatusConflict, "token_used")

	tok := h.login("BUDI@gmail.com", pw)
	out = h.expect(http.MethodGet, "/api/auth/me", tok, nil, http.StatusOK, "")
	if me := out["user"].(map[string]any); me["email_verified"] != true || me["has_password"] != true {
		t.Fatalf("me: %v", me)
	}
	h.expect(http.MethodGet, "/api/admin/overview", tok, nil, http.StatusForbidden, "forbidden")

	// lupa kata sandi -> link -> cek -> reset; semua sesi lama dicabut
	h.expect(http.MethodPost, "/api/auth/forgot-password", "", map[string]string{"email": "budi@gmail.com"}, http.StatusOK, "")
	reset := h.mail.last(t, "budi@gmail.com", "/reset-password")
	out = h.expect(http.MethodPost, "/api/auth/reset-password/check", "", map[string]string{"token": reset}, http.StatusOK, "")
	if out["email"] != "budi@gmail.com" {
		t.Fatalf("cek reset: %v", out)
	}
	out = h.expect(http.MethodPost, "/api/auth/reset-password", "", map[string]string{
		"token": reset, "password": "budi", "password_confirm": "budi"}, http.StatusUnprocessableEntity, "")
	if fields(out)["password"] == nil {
		t.Fatalf("kata sandi lemah: %v", out)
	}
	const pw2 = "kata-sandi-baru-77"
	h.expect(http.MethodPost, "/api/auth/reset-password", "", map[string]string{
		"token": reset, "password": pw2, "password_confirm": pw2}, http.StatusOK, "")
	h.expect(http.MethodPost, "/api/auth/reset-password", "", map[string]string{
		"token": reset, "password": pw2, "password_confirm": pw2}, http.StatusConflict, "token_used")
	h.expect(http.MethodGet, "/api/auth/me", tok, nil, http.StatusUnauthorized, "session_invalid")
	h.expect(http.MethodPost, "/api/auth/login", "", map[string]string{"email": "budi@gmail.com", "password": pw}, http.StatusUnauthorized, "")
	tokA, tokB := h.login("budi@gmail.com", pw2), h.login("budi@gmail.com", pw2)

	// ganti kata sandi: sesi ini tetap, sesi lain dicabut
	out = h.expect(http.MethodPost, "/api/auth/password", tokA, map[string]string{
		"current_password": "salah", "password": "lagi-baru-2026", "password_confirm": "lagi-baru-2026"}, http.StatusUnprocessableEntity, "")
	if fields(out)["current_password"] == nil {
		t.Fatalf("kata sandi saat ini salah: %v", out)
	}
	h.expect(http.MethodPost, "/api/auth/password", tokA, map[string]string{
		"current_password": pw2, "password": "lagi-baru-2026", "password_confirm": "lagi-baru-2026"}, http.StatusOK, "")
	h.expect(http.MethodGet, "/api/auth/me", tokA, nil, http.StatusOK, "")
	h.expect(http.MethodGet, "/api/auth/me", tokB, nil, http.StatusUnauthorized, "")

	h.expect(http.MethodPost, "/api/auth/logout", tokA, nil, http.StatusOK, "")
	h.expect(http.MethodGet, "/api/auth/me", tokA, nil, http.StatusUnauthorized, "")
}

func TestAuthAdminManagementDB(t *testing.T) {
	h := newAuthHarness(t)
	admin := h.login("admin@example.com", "admin-demo-2026")
	demo := h.login("user@example.com", "demo-user-2026")
	h.expect(http.MethodGet, "/api/admin/overview", admin, nil, http.StatusOK, "")
	h.expect(http.MethodGet, "/api/admin/overview", demo, nil, http.StatusForbidden, "forbidden")

	out := h.expect(http.MethodGet, "/api/auth/me", admin, nil, http.StatusOK, "")
	me := out["user"].(map[string]any)
	if me["role"] != roleAdmin || me["managed"] != true {
		t.Fatalf("admin bawaan: %v", me)
	}
	// akun bawaan: kata sandi diatur dari .env, lupa kata sandi tidak mengirim email
	h.expect(http.MethodPost, "/api/auth/password", demo, map[string]string{
		"current_password": "demo-user-2026", "password": "baru-sekali-1", "password_confirm": "baru-sekali-1"}, http.StatusForbidden, "managed_account")
	before := h.mail.count()
	h.expect(http.MethodPost, "/api/auth/forgot-password", "", map[string]string{"email": "admin@example.com"}, http.StatusOK, "")
	if h.mail.count() != before {
		t.Fatal("lupa kata sandi akun bawaan tidak boleh mengirim email")
	}

	// buat akun lewat admin: validasi sama dengan daftar, langsung terverifikasi
	out = h.expect(http.MethodPost, "/api/admin/users", admin, map[string]string{
		"name": "Sari", "email": "sari@", "password": "x", "role": "superadmin"}, http.StatusUnprocessableEntity, "")
	for _, k := range []string{"email", "password", "role"} {
		if fields(out)[k] == nil {
			t.Errorf("field %s: %v", k, out)
		}
	}
	out = h.expect(http.MethodPost, "/api/admin/users", admin, map[string]string{
		"name": "Sari", "email": "sari@kampus.ac.id", "password": "sari-kampus-1", "role": "user"}, http.StatusCreated, "")
	sariID := int64(out["user"].(map[string]any)["id"].(float64))
	h.expect(http.MethodPost, "/api/admin/users", admin, map[string]string{
		"name": "Sari", "email": "sari@kampus.ac.id", "password": "sari-kampus-1"}, http.StatusConflict, "email_taken")
	sari := h.login("sari@kampus.ac.id", "sari-kampus-1")

	out = h.expect(http.MethodGet, "/api/admin/users?q=kampus", admin, nil, http.StatusOK, "")
	if out["total"] != float64(1) {
		t.Fatalf("cari: %v", out)
	}
	out = h.expect(http.MethodGet, "/api/admin/users", admin, nil, http.StatusOK, "")
	if out["total"] != float64(3) {
		t.Fatalf("daftar: %v", out)
	}

	sariPath := fmt.Sprintf("/api/admin/users/%d", sariID)
	// promosi berlaku langsung pada sesi yang sama (role dibaca setiap request)
	h.expect(http.MethodPatch, sariPath, admin, map[string]string{"role": roleAdmin}, http.StatusOK, "")
	h.expect(http.MethodGet, "/api/admin/overview", sari, nil, http.StatusOK, "")
	adminID := int64(me["id"].(float64))
	adminPath := fmt.Sprintf("/api/admin/users/%d", adminID)
	h.expect(http.MethodPatch, adminPath, sari, map[string]string{"role": roleUser}, http.StatusConflict, "managed_account")
	h.expect(http.MethodDelete, adminPath, sari, nil, http.StatusConflict, "managed_account")
	h.expect(http.MethodPatch, adminPath, admin, map[string]any{"disabled": true}, http.StatusConflict, "self")
	h.expect(http.MethodPatch, sariPath, admin, map[string]string{"role": roleUser}, http.StatusOK, "")
	h.expect(http.MethodGet, "/api/admin/overview", sari, nil, http.StatusForbidden, "")

	// nonaktif: sesi dicabut, login ditolak dengan alasan jelas
	h.expect(http.MethodPatch, sariPath, admin, map[string]any{"disabled": true}, http.StatusOK, "")
	h.expect(http.MethodGet, "/api/auth/me", sari, nil, http.StatusUnauthorized, "")
	h.expect(http.MethodPost, "/api/auth/login", "", map[string]string{"email": "sari@kampus.ac.id", "password": "sari-kampus-1"}, http.StatusForbidden, "account_disabled")
	h.expect(http.MethodPost, sariPath+"/password-reset", admin, nil, http.StatusConflict, "account_disabled")
	h.expect(http.MethodPatch, sariPath, admin, map[string]any{"disabled": false}, http.StatusOK, "")
	h.expect(http.MethodPost, sariPath+"/password-reset", admin, nil, http.StatusOK, "")
	h.mail.last(t, "sari@kampus.ac.id", "/reset-password")

	h.expect(http.MethodDelete, sariPath, admin, nil, http.StatusOK, "")
	h.expect(http.MethodPatch, sariPath, admin, map[string]string{"name": "x"}, http.StatusNotFound, "not_found")
	h.expect(http.MethodPost, "/api/auth/login", "", map[string]string{"email": "sari@kampus.ac.id", "password": "sari-kampus-1"}, http.StatusUnauthorized, "")

	// ADMIN_PASSWORD diganti: start berikutnya menyamakan kata sandi dan mencabut sesi lama
	t.Setenv("ADMIN_PASSWORD", "admin-baru-2027")
	h.app.initAuth(context.Background())
	h.expect(http.MethodGet, "/api/auth/me", admin, nil, http.StatusUnauthorized, "")
	h.expect(http.MethodPost, "/api/auth/login", "", map[string]string{"email": "admin@example.com", "password": "admin-demo-2026"}, http.StatusUnauthorized, "")
	h.login("admin@example.com", "admin-baru-2027")
}

func TestGoogleAccountLinkingDB(t *testing.T) {
	h := newAuthHarness(t)
	ctx := context.Background()
	claims := func(sub, email string) *googleClaims {
		return &googleClaims{Sub: sub, Email: email, EmailVerified: true, Name: "Nama Google"}
	}

	// akun baru: role user, terverifikasi, tanpa kata sandi; login kedua memakai akun yang sama
	rina, err := h.app.googleUser(ctx, claims("g-1", "Rina@Gmail.com"))
	if err != nil || rina.Email != "rina@gmail.com" || rina.Role != roleUser || !rina.EmailVerified || rina.HasPassword || !rina.GoogleLinked {
		t.Fatalf("akun Google baru: %v %+v", err, rina)
	}
	if again, err := h.app.googleUser(ctx, claims("g-1", "rina@gmail.com")); err != nil || again.ID != rina.ID {
		t.Fatalf("login Google kedua: %v", err)
	}
	// akun Google tanpa kata sandi tidak bisa login dengan kata sandi apa pun, tapi boleh membuat kata sandi
	h.expect(http.MethodPost, "/api/auth/login", "", map[string]string{"email": "rina@gmail.com", "password": "apa-saja-1"}, http.StatusUnauthorized, "")

	// akun lokal terverifikasi + Google dengan email sama -> ditautkan, kata sandi tetap
	h.expect(http.MethodPost, "/api/admin/users", h.login("admin@example.com", "admin-demo-2026"), map[string]string{
		"name": "Doni", "email": "doni@gmail.com", "password": "doni-lokal-1"}, http.StatusCreated, "")
	if u, err := h.app.googleUser(ctx, claims("g-2", "doni@gmail.com")); err != nil || !u.HasPassword || !u.GoogleLinked {
		t.Fatalf("tautkan akun terverifikasi: %v %+v", err, u)
	}
	h.login("doni@gmail.com", "doni-lokal-1")
	// akun Google lain dengan email yang sama (sub beda) tidak boleh mengambil alih
	if _, err := h.app.googleUser(ctx, claims("g-3", "doni@gmail.com")); err != errGoogleConflict {
		t.Fatalf("sub beda: %v", err)
	}

	// akun lokal BELUM terverifikasi (bisa jadi didaftarkan orang lain) -> kata sandinya dihapus saat ditautkan
	h.expect(http.MethodPost, "/api/auth/register", "", map[string]string{
		"name": "Penyerang", "email": "eka@gmail.com", "password": "punya-penyerang-1", "password_confirm": "punya-penyerang-1"}, http.StatusCreated, "")
	u, err := h.app.googleUser(ctx, claims("g-4", "eka@gmail.com"))
	if err != nil || u.HasPassword || !u.EmailVerified {
		t.Fatalf("akun belum terverifikasi: %v %+v", err, u)
	}
	h.expect(http.MethodPost, "/api/auth/login", "", map[string]string{"email": "eka@gmail.com", "password": "punya-penyerang-1"}, http.StatusUnauthorized, "")

	// akun nonaktif tidak bisa masuk lewat Google
	if err := h.app.store.SetUserDisabled(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.googleUser(ctx, claims("g-4", "eka@gmail.com")); err != errAccountDisabled {
		t.Fatalf("akun nonaktif: %v", err)
	}

	// kode login sekali pakai (diterbitkan callback) -> sesi
	h.app.auth.google = newGoogleOAuthWith("id", "secret", "http://localhost:8080/api/auth/google/callback")
	lc := h.app.auth.google.issueCode(rina.ID, "/account")
	out := h.expect(http.MethodPost, "/api/auth/google/exchange", "", map[string]string{"code": lc}, http.StatusOK, "")
	if out["next"] != "/account" || out["token"] == nil {
		t.Fatalf("tukar kode: %v", out)
	}
	h.expect(http.MethodPost, "/api/auth/google/exchange", "", map[string]string{"code": lc}, http.StatusBadRequest, "google_code")
}

// 15 backend lab start bersamaan terhadap database yang sama: semuanya menyiapkan akun bawaan, hanya
// satu yang membuat, sisanya harus lanjut tanpa error.
func TestManagedAccountConcurrentStartDB(t *testing.T) {
	h := newAuthHarness(t)
	ctx := context.Background()
	u, err := h.app.store.UserByEmail(ctx, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.app.store.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 15)
	for range 15 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- h.app.ensureManagedAccount(ctx, "admin@example.com", "Admin", roleAdmin, "admin-demo-2026")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("instans gagal menyiapkan akun bawaan: %v", err)
		}
	}
	h.login("admin@example.com", "admin-demo-2026")
}

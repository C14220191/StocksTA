package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
)

// Akun pengguna dan sesi login (migrations/011_users.sql).
//
// Peran: "user" (default saat daftar) dan "admin" (boleh membuka /api/admin/*). Login dengan email +
// kata sandi (bcrypt) atau Google (google.go) menghasilkan token sesi acak yang dikirim UI sebagai
// "Authorization: Bearer <token>"; database hanya menyimpan sha256 token itu (user_sessions), jadi
// sesi bisa dicabut (logout, ganti kata sandi, akun dinonaktifkan).
//
// Akun baru wajib memverifikasi email sebelum bisa login (REQUIRE_EMAIL_VERIFICATION=on). Link
// verifikasi dan reset kata sandi berisi token sekali pakai (auth_tokens) dan menuju halaman UI
// (APP_URL), yang meneruskannya ke API.
//
// Akun bawaan (dibuat / disamakan setiap backend start, ensureManagedAccounts):
//   admin: ADMIN_EMAIL (run.env) + ADMIN_PASSWORD (.env, rahasia)
//   user : DEMO_USER_EMAIL + DEMO_USER_PASSWORD (run.env, akun demo tanpa hak admin)

// bcryptCost: diturunkan di tes supaya cepat.
var bcryptCost = 12

const (
	verifyTokenTTL     = 24 * time.Hour
	resetTokenTTL      = time.Hour
	mailCooldown       = 60 * time.Second // jeda minimal antar email ke akun yang sama
	loginFailWindow    = 15 * time.Minute
	loginFailMax       = 10 // percobaan gagal per email per jendela
	ctxUser            = "auth_user"
	ctxSessionHash     = "auth_session_hash"
	ctxSessionExpires  = "auth_session_expires"
	msgCheckFields     = "Periksa kembali isian yang ditandai."
	msgMigrationNeeded = "Tabel akun belum ada: jalankan ./ta-up.sh migrate 011_users.sql (Windows: .\\ta-up.ps1 migrate 011_users.sql) lalu restart backend."
)

type authConfig struct {
	appName       string
	appURL        string // UI, untuk link di email dan redirect setelah login Google
	sessionTTL    time.Duration
	requireVerify bool
}

type authService struct {
	cfg         authConfig
	mail        mailer
	google      *googleOAuth  // nil = login Google tidak dikonfigurasi
	checkDomain domainChecker // nil = tanpa cek DNS domain email (EMAIL_CHECK_DNS=off)
	logins      *failLimiter
	ready       bool            // tabel akun ada (migrasi 011)
	managed     map[string]bool // email akun bawaan dari .env/run.env
	syncSend    bool            // tes: kirim email langsung, bukan di goroutine
	lastPurge   atomic.Int64
	now         func() time.Time
}

func newAuthService() *authService {
	s := &authService{
		cfg: authConfig{
			appName:       env("APP_NAME", "StocksTA"),
			appURL:        strings.TrimRight(env("APP_URL", "http://localhost:5173"), "/"),
			sessionTTL:    time.Duration(max(envInt("SESSION_HOURS", 24), 1)) * time.Hour,
			requireVerify: env("REQUIRE_EMAIL_VERIFICATION", "on") != "off",
		},
		mail:    newMailer(),
		google:  newGoogleOAuth(),
		logins:  newFailLimiter(loginFailMax, loginFailWindow),
		managed: map[string]bool{},
		now:     time.Now,
	}
	if env("EMAIL_CHECK_DNS", "on") != "off" {
		s.checkDomain = checkEmailDomainDNS
	}
	return s
}

// managedAccounts: akun bawaan yang dikonfigurasi (kata sandi kosong = tidak dibuat).
func managedAccounts() []struct{ email, name, role, password string } {
	return []struct{ email, name, role, password string }{
		{normalizeEmail(env("ADMIN_EMAIL", "admin@example.com")), "Admin", roleAdmin, os.Getenv("ADMIN_PASSWORD")},
		{normalizeEmail(env("DEMO_USER_EMAIL", "user@example.com")), "Demo User", roleUser, os.Getenv("DEMO_USER_PASSWORD")},
	}
}

// initAuth: cek tabel akun lalu siapkan akun bawaan. Kegagalan tidak menghentikan backend (halaman
// pengguna dan benchmark tidak butuh akun); endpoint akun dan admin menjawab 503 dengan petunjuk.
func (a *App) initAuth(ctx context.Context) {
	ready, err := a.store.UsersTableReady(ctx)
	if err != nil || !ready {
		slog.Error("akun dan halaman admin tidak aktif", "err", err, "petunjuk", msgMigrationNeeded)
		return
	}
	a.auth.ready = true
	for _, m := range managedAccounts() {
		if m.password == "" {
			if m.role == roleAdmin {
				slog.Warn("ADMIN_PASSWORD kosong: akun admin bawaan tidak dibuat (isi di .env)", "email", m.email)
			}
			continue
		}
		if msg := emailFormatError(m.email); msg != "" {
			slog.Error("email akun bawaan tidak valid", "email", m.email, "err", msg)
			continue
		}
		a.auth.managed[m.email] = true
		if err := a.ensureManagedAccount(ctx, m.email, m.name, m.role, m.password); err != nil {
			slog.Error("gagal menyiapkan akun bawaan", "email", m.email, "err", err)
		}
	}
}

// ensureManagedAccount: buat kalau belum ada; kalau sudah ada, samakan kata sandinya dengan konfigurasi,
// tandai terverifikasi, dan (admin) pastikan tetap admin dan aktif.
func (a *App) ensureManagedAccount(ctx context.Context, email, name, role, password string) error {
	u, err := a.store.UserByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		hash, herr := hashPassword(password)
		if herr != nil {
			return herr
		}
		_, cerr := a.store.CreateUser(ctx, newUser{Email: email, Name: name, PasswordHash: hash, Role: role, Verified: true})
		if cerr == nil {
			slog.Info("akun bawaan dibuat", "email", email, "role", role)
			return nil
		}
		if !errors.Is(cerr, ErrExists) {
			return cerr
		}
		// instans lain (mis. 15 backend lab yang start bersamaan) baru saja membuatnya: lanjut menyamakan
		u, err = a.store.UserByEmail(ctx, email)
	}
	if err != nil {
		return err
	}
	if !u.HasPassword || bcrypt.CompareHashAndPassword([]byte(u.passwordHash), []byte(password)) != nil {
		hash, err := hashPassword(password)
		if err != nil {
			return err
		}
		if err := a.store.SetUserPassword(ctx, u.ID, hash, nil); err != nil {
			return err
		}
		slog.Info("kata sandi akun bawaan disamakan dengan konfigurasi", "email", email)
	}
	if !u.EmailVerified {
		if err := a.store.MarkEmailVerified(ctx, u.ID); err != nil {
			return err
		}
	}
	if role == roleAdmin {
		if u.Role != roleAdmin {
			if err := a.store.SetUserRole(ctx, u.ID, roleAdmin); err != nil {
				return err
			}
		}
		if u.Disabled {
			return a.store.SetUserDisabled(ctx, u.ID, false)
		}
	}
	return nil
}

func hashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	return string(h), err
}

// dummyHash: dibandingkan saat email tidak terdaftar, supaya waktu respons login sama dengan email
// terdaftar (tidak membocorkan email mana yang punya akun).
var dummyHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("stocksta-timing-equalizer"), bcryptCost)
	return h
})

// newToken: token acak 256 bit untuk browser / link email, dan sha256-nya untuk database.
func newToken() (string, []byte) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	raw := base64.RawURLEncoding.EncodeToString(b)
	return raw, tokenHash(raw)
}

func tokenHash(raw string) []byte {
	h := sha256.Sum256([]byte(raw))
	return h[:]
}

// present: tandai akun bawaan sebelum dikirim ke UI.
func (s *authService) present(u *User) *User {
	u.Managed = s.managed[u.Email]
	return u
}

// ---- pembatas percobaan login ----

type failLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
	now    func() time.Time
}

func newFailLimiter(maxFails int, window time.Duration) *failLimiter {
	return &failLimiter{max: maxFails, window: window, fails: map[string][]time.Time{}, now: time.Now}
}

func (l *failLimiter) prune(key string) []time.Time {
	cut := l.now().Add(-l.window)
	kept := l.fails[key][:0]
	for _, t := range l.fails[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, key)
		return nil
	}
	l.fails[key] = kept
	return kept
}

func (l *failLimiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key)) >= l.max
}

func (l *failLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.fails) > 10000 { // jaga memori kalau banyak email berbeda dicoba
		for k := range l.fails {
			l.prune(k)
		}
	}
	l.fails[key] = append(l.fails[key], l.now())
}

func (l *failLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.fails, key)
	l.mu.Unlock()
}

// ---- respons ----

func jsonError(c echo.Context, status int, code, msg string) error {
	body := map[string]any{"error": msg}
	if code != "" {
		body["code"] = code
	}
	return c.JSON(status, body)
}

func invalidFields(c echo.Context, f fieldErrors) error {
	return c.JSON(http.StatusUnprocessableEntity, map[string]any{"error": msgCheckFields, "fields": f})
}

func badJSON(c echo.Context) error {
	return jsonError(c, http.StatusBadRequest, "bad_request", "Body harus JSON yang valid.")
}

func domainOf(email string) string {
	_, d, _ := strings.Cut(email, "@")
	return d
}

// ---- middleware ----

func bearerToken(c echo.Context) string {
	t, ok := strings.CutPrefix(c.Request().Header.Get(echo.HeaderAuthorization), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(t)
}

func currentUser(c echo.Context) *User {
	u, _ := c.Get(ctxUser).(*User)
	return u
}

// authReady: endpoint akun hanya aktif setelah migrasi 011.
func (a *App) authReady(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !a.auth.ready {
			return jsonError(c, http.StatusServiceUnavailable, "not_ready", msgMigrationNeeded)
		}
		return next(c)
	}
}

// requireUser: sesi login yang sah (akun aktif). Header "Authorization: Bearer <token>".
func (a *App) requireUser(next echo.HandlerFunc) echo.HandlerFunc {
	return a.authReady(func(c echo.Context) error {
		raw := bearerToken(c)
		if raw == "" {
			return jsonError(c, http.StatusUnauthorized, "unauthenticated", "Perlu login.")
		}
		hash := tokenHash(raw)
		u, exp, err := a.store.SessionUser(c.Request().Context(), hash)
		if errors.Is(err, ErrNotFound) {
			return jsonError(c, http.StatusUnauthorized, "session_invalid", "Sesi tidak valid atau sudah habis. Silakan masuk lagi.")
		}
		if err != nil {
			return a.fail(c, err)
		}
		c.Set(ctxUser, a.auth.present(u))
		c.Set(ctxSessionHash, hash)
		c.Set(ctxSessionExpires, exp)
		return next(c)
	})
}

// requireAdmin: seperti requireUser, ditambah role admin (403 kalau bukan admin).
func (a *App) requireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
	return a.requireUser(func(c echo.Context) error {
		if currentUser(c).Role != roleAdmin {
			return jsonError(c, http.StatusForbidden, "forbidden", "Halaman ini khusus admin.")
		}
		return next(c)
	})
}

// registerDataRoutes: data pasar dan analisis AI untuk UI. requireLogin (REQUIRE_LOGIN, default on) =
// wajib sesi login. Instans lab benchmark (ta-up lab / bench) berjalan dengan REQUIRE_LOGIN=off: k6
// memanggil /api/analyze tanpa akun, dan kondisinya tetap sama dengan run benchmark yang sudah diukur
// (tanpa query sesi di jalur request).
func (a *App) registerDataRoutes(e *echo.Echo, requireLogin bool) {
	var mw []echo.MiddlewareFunc
	if requireLogin {
		mw = append(mw, a.requireUser)
	} else {
		slog.Warn("REQUIRE_LOGIN=off: data dan analisis bisa diakses tanpa login (hanya untuk instans lab benchmark)")
	}
	// AI inference (melewati cache policy dan dicatat ke inference_log = data eksperimen)
	e.GET("/api/analyze/:symbol", a.Analyze, mw...)
	// Data UI (hanya baca database, tanpa AI dan tanpa inference_log)
	e.GET("/api/market/overview", a.MarketOverview, mw...)
	e.GET("/api/search", a.Search, mw...)
	e.GET("/api/instruments/:symbol", a.InstrumentDetail, mw...)
	e.GET("/api/instruments/:symbol/candles", a.InstrumentCandles, mw...)
}

// registerAuthRoutes: endpoint akun (publik dan akun sendiri) + grup /api/admin (role admin) beserta
// manajemen pengguna. limit = pembatas per IP untuk endpoint yang memakai bcrypt atau mengirim email.
func (a *App) registerAuthRoutes(e *echo.Echo, authLimit echo.MiddlewareFunc) *echo.Group {
	e.GET("/api/auth/config", a.AuthConfig)
	e.POST("/api/auth/register", a.Register, a.authReady, authLimit)
	e.POST("/api/auth/login", a.Login, a.authReady, authLimit)
	e.POST("/api/auth/verify-email", a.VerifyEmail, a.authReady, authLimit)
	e.POST("/api/auth/resend-verification", a.ResendVerification, a.authReady, authLimit)
	e.POST("/api/auth/forgot-password", a.ForgotPassword, a.authReady, authLimit)
	e.POST("/api/auth/reset-password/check", a.CheckResetToken, a.authReady, authLimit)
	e.POST("/api/auth/reset-password", a.ResetPassword, a.authReady, authLimit)
	e.GET("/api/auth/google/start", a.GoogleStart)
	e.GET("/api/auth/google/callback", a.GoogleCallback)
	e.POST("/api/auth/google/exchange", a.GoogleExchange, a.authReady, authLimit)
	e.GET("/api/auth/me", a.Me, a.requireUser)
	e.PATCH("/api/auth/me", a.UpdateMe, a.requireUser)
	e.POST("/api/auth/password", a.ChangePassword, a.requireUser, authLimit)
	e.POST("/api/auth/logout", a.Logout, a.requireUser)
	e.POST("/api/auth/logout-all", a.LogoutAll, a.requireUser)

	adm := e.Group("/api/admin", a.requireAdmin)
	adm.GET("/users", a.AdminUsers)
	adm.POST("/users", a.AdminCreateUser)
	adm.PATCH("/users/:id", a.AdminUpdateUser)
	adm.POST("/users/:id/password-reset", a.AdminSendPasswordReset)
	adm.DELETE("/users/:id", a.AdminDeleteUser)
	return adm
}

// ---- sesi ----

// startSession: buat sesi baru untuk akun yang sudah lolos pemeriksaan login.
func (a *App) startSession(c echo.Context, u *User) (map[string]any, error) {
	ctx := c.Request().Context()
	raw, hash := newToken()
	exp := a.auth.now().Add(a.auth.cfg.sessionTTL).Truncate(time.Second)
	if err := a.store.CreateSession(ctx, u.ID, hash, exp, c.Request().UserAgent(), c.RealIP()); err != nil {
		return nil, err
	}
	if err := a.store.TouchLogin(ctx, u.ID); err != nil {
		return nil, err
	}
	now := a.auth.now().Unix()
	if last := a.auth.lastPurge.Load(); now-last > 3600 && a.auth.lastPurge.CompareAndSwap(last, now) {
		if err := a.store.PurgeExpired(ctx); err != nil {
			slog.Warn("bersihkan sesi kedaluwarsa gagal", "err", err)
		}
	}
	slog.Info("login", "user_id", u.ID, "role", u.Role, "ip", c.RealIP())
	return map[string]any{"token": raw, "expires_at": exp, "user": a.auth.present(u)}, nil
}

// ---- email ----

// sendLink: buat token sekali pakai lalu kirim email berisi link ke halaman UI (path?token=...).
func (a *App) sendLink(ctx context.Context, u *User, purpose string) error {
	ttl, path, subject, intro := verifyTokenTTL, "/verify-email", "Verifikasi email akun "+a.auth.cfg.appName,
		"Klik link berikut untuk memverifikasi email Anda (berlaku 24 jam):"
	if purpose == purposeResetPassword {
		ttl, path, subject, intro = resetTokenTTL, "/reset-password", "Atur ulang kata sandi "+a.auth.cfg.appName,
			"Kami menerima permintaan untuk mengatur ulang kata sandi akun Anda. Klik link berikut (berlaku 1 jam):"
	}
	raw, hash := newToken()
	if err := a.store.CreateAuthToken(ctx, u.ID, purpose, hash, a.auth.now().Add(ttl)); err != nil {
		return err
	}
	link := a.auth.cfg.appURL + path + "?token=" + url.QueryEscape(raw)
	body := fmt.Sprintf("Halo %s,\n\n%s\n\n%s\n\nKalau Anda tidak merasa meminta ini, abaikan email ini; akun Anda tetap aman.\n\n%s\n",
		u.Name, intro, link, a.auth.cfg.appName)
	send := func() {
		sctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := a.auth.mail.send(sctx, u.Email, subject, body); err != nil {
			slog.Error("kirim email gagal", "purpose", purpose, "user_id", u.ID, "err", err)
		}
	}
	if a.auth.syncSend {
		send()
	} else {
		go send()
	}
	return nil
}

// cooldownLeft: sisa jeda sebelum email dengan tujuan ini boleh dikirim lagi ke akun u.
func (a *App) cooldownLeft(ctx context.Context, u *User, purpose string) (time.Duration, error) {
	last, err := a.store.LastAuthTokenAt(ctx, u.ID, purpose)
	if err != nil || last.IsZero() {
		return 0, err
	}
	return max(mailCooldown-a.auth.now().Sub(last), 0), nil
}

// ---- handler publik ----

// AuthConfig: GET /api/auth/config, fitur yang aktif untuk UI.
func (a *App) AuthConfig(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{
		"app_name":           a.auth.cfg.appName,
		"ready":              a.auth.ready,
		"google":             a.auth.google != nil,
		"email_verification": a.auth.cfg.requireVerify,
		"mail":               a.auth.mail.kind(),
	})
}

// Register: POST /api/auth/register {"name", "email", "password", "password_confirm"}.
func (a *App) Register(c echo.Context) error {
	var body struct {
		Name            string `json:"name"`
		Email           string `json:"email"`
		Password        string `json:"password"`
		PasswordConfirm string `json:"password_confirm"`
	}
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	ctx := c.Request().Context()
	name, email := strings.TrimSpace(body.Name), normalizeEmail(body.Email)
	f := fieldErrors{}
	f.add("name", nameError(name))
	f.add("email", emailFormatError(email))
	if f["email"] == "" && a.auth.checkDomain != nil {
		f.add("email", a.auth.checkDomain(ctx, domainOf(email)))
	}
	checkNewPassword(f, body.Password, body.PasswordConfirm, email)
	if len(f) > 0 {
		return invalidFields(c, f)
	}
	if _, err := a.store.UserByEmail(ctx, email); err == nil {
		return emailTaken(c)
	} else if !errors.Is(err, ErrNotFound) {
		return a.fail(c, err)
	}
	hash, err := hashPassword(body.Password)
	if err != nil {
		return a.fail(c, err)
	}
	u, err := a.store.CreateUser(ctx, newUser{Email: email, Name: name, PasswordHash: hash, Role: roleUser})
	if errors.Is(err, ErrExists) {
		return emailTaken(c)
	}
	if err != nil {
		return a.fail(c, err)
	}
	slog.Info("akun baru", "user_id", u.ID)
	if err := a.sendLink(ctx, u, purposeVerifyEmail); err != nil {
		slog.Error("buat link verifikasi gagal", "user_id", u.ID, "err", err)
	}
	resp := map[string]any{"user": a.auth.present(u), "verification_required": a.auth.cfg.requireVerify, "mail": a.auth.mail.kind()}
	if !a.auth.cfg.requireVerify {
		sess, err := a.startSession(c, u)
		if err != nil {
			return a.fail(c, err)
		}
		for k, v := range sess {
			resp[k] = v
		}
	}
	return c.JSON(http.StatusCreated, resp)
}

func emailTaken(c echo.Context) error {
	return c.JSON(http.StatusConflict, map[string]any{
		"error":  "Email sudah terdaftar.",
		"code":   "email_taken",
		"fields": fieldErrors{"email": "Email sudah terdaftar. Silakan masuk, atau pakai Lupa kata sandi."},
	})
}

// Login: POST /api/auth/login {"email", "password"} -> {"token", "expires_at", "user"}.
func (a *App) Login(c echo.Context) error {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	email := normalizeEmail(body.Email)
	f := fieldErrors{}
	f.add("email", emailFormatError(email))
	if body.Password == "" {
		f.add("password", "Kata sandi wajib diisi.")
	}
	if len(f) > 0 {
		return invalidFields(c, f)
	}
	if a.auth.logins.blocked(email) {
		return jsonError(c, http.StatusTooManyRequests, "too_many_attempts",
			"Terlalu banyak percobaan login gagal untuk email ini. Coba lagi dalam 15 menit, atau pakai Lupa kata sandi.")
	}
	u, err := a.store.UserByEmail(c.Request().Context(), email)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return a.fail(c, err)
	}
	hash := dummyHash()
	if u != nil && u.HasPassword {
		hash = []byte(u.passwordHash)
	}
	match := bcrypt.CompareHashAndPassword(hash, []byte(body.Password)) == nil
	if u == nil || !u.HasPassword || !match {
		a.auth.logins.fail(email)
		slog.Warn("login gagal", "ip", c.RealIP())
		return jsonError(c, http.StatusUnauthorized, "invalid_credentials", "Email atau kata sandi salah.")
	}
	a.auth.logins.reset(email)
	if u.Disabled {
		return jsonError(c, http.StatusForbidden, "account_disabled", "Akun ini dinonaktifkan admin.")
	}
	if a.auth.cfg.requireVerify && !u.EmailVerified {
		return jsonError(c, http.StatusForbidden, "email_unverified",
			"Email belum diverifikasi. Buka link verifikasi di email Anda, atau kirim ulang link-nya.")
	}
	sess, err := a.startSession(c, u)
	if err != nil {
		return a.fail(c, err)
	}
	return c.JSON(http.StatusOK, sess)
}

// tokenFailure: respons untuk err (tidak nil) dari token link email: pesan per jenis kesalahan token,
// atau 500 untuk error lain.
func (a *App) tokenFailure(c echo.Context, err error, what, validity string) error {
	switch {
	case errors.Is(err, errTokenInvalid):
		return jsonError(c, http.StatusBadRequest, "token_invalid",
			fmt.Sprintf("Link %s tidak valid. Pastikan link disalin utuh, atau minta link baru.", what))
	case errors.Is(err, errTokenUsed):
		return jsonError(c, http.StatusConflict, "token_used",
			fmt.Sprintf("Link %s ini sudah tidak berlaku (sudah dipakai, atau sudah ada link yang lebih baru).", what))
	case errors.Is(err, errTokenExpired):
		return jsonError(c, http.StatusGone, "token_expired",
			fmt.Sprintf("Link %s sudah kedaluwarsa (berlaku %s). Minta link baru.", what, validity))
	}
	return a.fail(c, err)
}

// VerifyEmail: POST /api/auth/verify-email {"token"} (dari halaman UI /verify-email?token=...).
func (a *App) VerifyEmail(c echo.Context) error {
	var body struct {
		Token string `json:"token"`
	}
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	id, err := a.store.VerifyEmailToken(c.Request().Context(), tokenHash(strings.TrimSpace(body.Token)))
	if err != nil {
		return a.tokenFailure(c, err, "verifikasi", "24 jam")
	}
	u, err := a.store.UserByID(c.Request().Context(), id)
	if err != nil {
		return a.fail(c, err)
	}
	slog.Info("email terverifikasi", "user_id", id)
	return c.JSON(http.StatusOK, map[string]any{"verified": true, "email": u.Email})
}

// emailOnly: body {"email"} untuk kirim ulang verifikasi dan lupa kata sandi.
func emailOnly(c echo.Context) (string, error) {
	var body struct {
		Email string `json:"email"`
	}
	if err := c.Bind(&body); err != nil {
		return "", badJSON(c)
	}
	email := normalizeEmail(body.Email)
	if msg := emailFormatError(email); msg != "" {
		return "", invalidFields(c, fieldErrors{"email": msg})
	}
	return email, nil
}

// ResendVerification: POST /api/auth/resend-verification {"email"}. Jawaban sama untuk email yang
// tidak terdaftar atau sudah terverifikasi.
func (a *App) ResendVerification(c echo.Context) error {
	email, err := emailOnly(c)
	if email == "" {
		return err
	}
	ok := map[string]any{"message": "Kalau email terdaftar dan belum diverifikasi, link verifikasi baru sudah dikirim.", "mail": a.auth.mail.kind()}
	ctx := c.Request().Context()
	u, err := a.store.UserByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		return c.JSON(http.StatusOK, ok)
	}
	if err != nil {
		return a.fail(c, err)
	}
	if u.EmailVerified || u.Disabled {
		return c.JSON(http.StatusOK, ok)
	}
	left, err := a.cooldownLeft(ctx, u, purposeVerifyEmail)
	if err != nil {
		return a.fail(c, err)
	}
	if left > 0 {
		return c.JSON(http.StatusTooManyRequests, map[string]any{
			"error":       fmt.Sprintf("Link baru saja dikirim. Tunggu %d detik sebelum mengirim ulang.", int(left.Seconds()+0.999)),
			"code":        "cooldown",
			"retry_after": int(left.Seconds() + 0.999),
		})
	}
	if err := a.sendLink(ctx, u, purposeVerifyEmail); err != nil {
		return a.fail(c, err)
	}
	return c.JSON(http.StatusOK, ok)
}

// ForgotPassword: POST /api/auth/forgot-password {"email"}. Jawaban selalu sama (tidak membocorkan
// email mana yang terdaftar). Akun bawaan dilewati: kata sandinya diatur dari .env/run.env.
func (a *App) ForgotPassword(c echo.Context) error {
	email, err := emailOnly(c)
	if email == "" {
		return err
	}
	ok := map[string]any{"message": "Kalau email terdaftar, link untuk mengatur ulang kata sandi sudah dikirim. Cek inbox dan folder spam.", "mail": a.auth.mail.kind()}
	ctx := c.Request().Context()
	u, err := a.store.UserByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		return c.JSON(http.StatusOK, ok)
	}
	if err != nil {
		return a.fail(c, err)
	}
	if u.Disabled || a.auth.managed[u.Email] {
		slog.Info("lupa kata sandi dilewati", "user_id", u.ID, "disabled", u.Disabled, "managed", a.auth.managed[u.Email])
		return c.JSON(http.StatusOK, ok)
	}
	if left, err := a.cooldownLeft(ctx, u, purposeResetPassword); err != nil {
		return a.fail(c, err)
	} else if left > 0 {
		return c.JSON(http.StatusOK, ok)
	}
	if err := a.sendLink(ctx, u, purposeResetPassword); err != nil {
		return a.fail(c, err)
	}
	return c.JSON(http.StatusOK, ok)
}

// CheckResetToken: POST /api/auth/reset-password/check {"token"}, supaya halaman reset langsung
// memberi tahu kalau link-nya tidak berlaku (sebelum pengguna mengetik kata sandi baru).
func (a *App) CheckResetToken(c echo.Context) error {
	var body struct {
		Token string `json:"token"`
	}
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	id, err := a.store.PeekAuthToken(c.Request().Context(), purposeResetPassword, tokenHash(strings.TrimSpace(body.Token)))
	if err != nil {
		return a.tokenFailure(c, err, "reset kata sandi", "1 jam")
	}
	u, err := a.store.UserByID(c.Request().Context(), id)
	if err != nil {
		return a.fail(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"valid": true, "email": u.Email})
}

// ResetPassword: POST /api/auth/reset-password {"token", "password", "password_confirm"}.
func (a *App) ResetPassword(c echo.Context) error {
	var body struct {
		Token           string `json:"token"`
		Password        string `json:"password"`
		PasswordConfirm string `json:"password_confirm"`
	}
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	ctx := c.Request().Context()
	hash := tokenHash(strings.TrimSpace(body.Token))
	id, err := a.store.PeekAuthToken(ctx, purposeResetPassword, hash)
	if err != nil {
		return a.tokenFailure(c, err, "reset kata sandi", "1 jam")
	}
	u, err := a.store.UserByID(ctx, id)
	if err != nil {
		return a.fail(c, err)
	}
	f := fieldErrors{}
	checkNewPassword(f, body.Password, body.PasswordConfirm, u.Email)
	if len(f) > 0 {
		return invalidFields(c, f)
	}
	pwHash, err := hashPassword(body.Password)
	if err != nil {
		return a.fail(c, err)
	}
	if _, err := a.store.ResetPasswordToken(ctx, hash, pwHash); err != nil {
		return a.tokenFailure(c, err, "reset kata sandi", "1 jam")
	}
	a.auth.logins.reset(u.Email)
	slog.Info("kata sandi direset lewat email", "user_id", u.ID)
	return c.JSON(http.StatusOK, map[string]any{"message": "Kata sandi diganti. Silakan masuk dengan kata sandi baru.", "email": u.Email})
}

// ---- handler akun sendiri (requireUser) ----

// Me: GET /api/auth/me -> {"user", "expires_at"}.
func (a *App) Me(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{"user": currentUser(c), "expires_at": c.Get(ctxSessionExpires)})
}

// UpdateMe: PATCH /api/auth/me {"name"}.
func (a *App) UpdateMe(c echo.Context) error {
	var body struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	name := strings.TrimSpace(body.Name)
	if msg := nameError(name); msg != "" {
		return invalidFields(c, fieldErrors{"name": msg})
	}
	u := currentUser(c)
	if err := a.store.SetUserName(c.Request().Context(), u.ID, name); err != nil {
		return a.fail(c, err)
	}
	u.Name = name
	return c.JSON(http.StatusOK, map[string]any{"user": u})
}

// ChangePassword: POST /api/auth/password {"current_password", "password", "password_confirm"}.
// Akun Google tanpa kata sandi boleh membuat kata sandi pertama tanpa current_password. Sesi lain
// akun ini dicabut; sesi yang sedang dipakai tetap.
func (a *App) ChangePassword(c echo.Context) error {
	var body struct {
		Current         string `json:"current_password"`
		Password        string `json:"password"`
		PasswordConfirm string `json:"password_confirm"`
	}
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	u := currentUser(c)
	if u.Managed {
		return jsonError(c, http.StatusForbidden, "managed_account",
			"Kata sandi akun bawaan diatur lewat .env / run.env (ADMIN_PASSWORD, DEMO_USER_PASSWORD), bukan dari sini.")
	}
	key := fmt.Sprintf("password:%d", u.ID)
	if a.auth.logins.blocked(key) {
		return jsonError(c, http.StatusTooManyRequests, "too_many_attempts", "Terlalu banyak percobaan. Coba lagi dalam 15 menit.")
	}
	f := fieldErrors{}
	if u.HasPassword {
		if body.Current == "" {
			f.add("current_password", "Kata sandi saat ini wajib diisi.")
		} else if bcrypt.CompareHashAndPassword([]byte(u.passwordHash), []byte(body.Current)) != nil {
			a.auth.logins.fail(key)
			f.add("current_password", "Kata sandi saat ini salah.")
		} else if body.Password == body.Current {
			f.add("password", "Kata sandi baru harus berbeda dari kata sandi saat ini.")
		}
	}
	checkNewPassword(f, body.Password, body.PasswordConfirm, u.Email)
	if len(f) > 0 {
		return invalidFields(c, f)
	}
	hash, err := hashPassword(body.Password)
	if err != nil {
		return a.fail(c, err)
	}
	keep, _ := c.Get(ctxSessionHash).([]byte)
	if err := a.store.SetUserPassword(c.Request().Context(), u.ID, hash, keep); err != nil {
		return a.fail(c, err)
	}
	a.auth.logins.reset(key)
	u.HasPassword = true
	slog.Info("kata sandi diganti", "user_id", u.ID)
	return c.JSON(http.StatusOK, map[string]any{"user": u, "message": "Kata sandi diganti. Sesi di perangkat lain sudah dikeluarkan."})
}

// Logout: POST /api/auth/logout (sesi ini) atau /api/auth/logout-all (semua perangkat).
func (a *App) Logout(c echo.Context) error {
	hash, _ := c.Get(ctxSessionHash).([]byte)
	if err := a.store.DeleteSession(c.Request().Context(), hash); err != nil {
		return a.fail(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"ok": true})
}

func (a *App) LogoutAll(c echo.Context) error {
	if err := a.store.DeleteUserSessions(c.Request().Context(), currentUser(c).ID); err != nil {
		return a.fail(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"ok": true})
}

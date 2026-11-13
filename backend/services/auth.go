package services

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

	"golang.org/x/crypto/bcrypt"

	"ta-backend/models"
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
// Akun bawaan (dibuat / disamakan setiap backend start, Init):
//   admin: ADMIN_EMAIL (run.env) + ADMIN_PASSWORD (.env, rahasia)
//   user : DEMO_USER_EMAIL (run.env) + DEMO_USER_PASSWORD (.env)

// BcryptCost: diturunkan di tes supaya cepat.
var BcryptCost = 12

const (
	verifyTokenTTL  = 24 * time.Hour
	resetTokenTTL   = time.Hour
	mailCooldown    = 60 * time.Second // jeda minimal antar email ke akun yang sama
	loginFailWindow = 15 * time.Minute
	loginFailMax    = 10 // percobaan gagal per email per jendela
	msgCheckFields  = "Periksa kembali isian yang ditandai."

	// MsgMigrationNeeded: petunjuk kalau tabel akun belum ada (respons 503 not_ready).
	MsgMigrationNeeded = "Tabel akun belum ada: jalankan ./ta-up.sh migrate 011_users.sql (Windows: .\\ta-up.ps1 migrate 011_users.sql) lalu restart backend."
)

// AuthOptions: konfigurasi AuthService. NewAuthService membacanya dari environment; tes mengisinya langsung.
type AuthOptions struct {
	AppName       string
	AppURL        string // UI, untuk link di email dan redirect setelah login Google
	SessionTTL    time.Duration
	RequireVerify bool
	Mailer        Mailer
	Google        *GoogleOAuth  // nil = login Google tidak dikonfigurasi
	CheckDomain   DomainChecker // nil = tanpa cek DNS domain email (EMAIL_CHECK_DNS=off)
	SyncSend      bool          // tes: kirim email langsung, bukan di goroutine
}

// AuthService: akun, sesi, verifikasi email, reset kata sandi, login Google, dan manajemen pengguna.
type AuthService struct {
	store     *Store
	cfg       AuthOptions
	logins    *failLimiter
	ready     bool            // tabel akun ada (migrasi 011)
	managed   map[string]bool // email akun bawaan dari .env/run.env
	lastPurge atomic.Int64
	now       func() time.Time
}

func NewAuthService(store *Store) *AuthService {
	opts := AuthOptions{
		AppName:       env("APP_NAME", "StocksTA"),
		AppURL:        strings.TrimRight(env("APP_URL", "http://localhost:5173"), "/"),
		SessionTTL:    time.Duration(max(envInt("SESSION_HOURS", 24), 1)) * time.Hour,
		RequireVerify: env("REQUIRE_EMAIL_VERIFICATION", "on") != "off",
		Mailer:        newMailer(),
		Google:        newGoogleOAuth(),
	}
	if env("EMAIL_CHECK_DNS", "on") != "off" {
		opts.CheckDomain = checkEmailDomainDNS
	}
	return NewAuthServiceWith(store, opts)
}

func NewAuthServiceWith(store *Store, opts AuthOptions) *AuthService {
	return &AuthService{store: store, cfg: opts, logins: newFailLimiter(loginFailMax, loginFailWindow),
		managed: map[string]bool{}, now: time.Now}
}

// Ready: tabel akun ada (Init sudah memeriksanya).
func (a *AuthService) Ready() bool { return a.ready }

// SetReady: tandai tabel akun ada / tidak ada tanpa memeriksa database. Hanya untuk tes controller
// tanpa database; backend memakai Init.
func (a *AuthService) SetReady(ready bool) { a.ready = ready }

// AppURL: alamat UI (link email, redirect setelah login Google).
func (a *AuthService) AppURL() string { return a.cfg.AppURL }

// Google: konfigurasi login Google, nil kalau tidak aktif.
func (a *AuthService) Google() *GoogleOAuth { return a.cfg.Google }

// managedAccounts: akun bawaan yang dikonfigurasi (kata sandi kosong = tidak dibuat).
func managedAccounts() []struct{ email, name, role, password string } {
	return []struct{ email, name, role, password string }{
		{normalizeEmail(env("ADMIN_EMAIL", "admin@example.com")), "Admin", roleAdmin, os.Getenv("ADMIN_PASSWORD")},
		{normalizeEmail(env("DEMO_USER_EMAIL", "user@example.com")), "Demo User", roleUser, os.Getenv("DEMO_USER_PASSWORD")},
	}
}

// Init: cek tabel akun lalu siapkan akun bawaan. Kegagalan tidak menghentikan backend (benchmark
// tidak butuh akun); endpoint akun dan admin menjawab 503 dengan petunjuk.
func (a *AuthService) Init(ctx context.Context) {
	ready, err := a.store.UsersTableReady(ctx)
	if err != nil || !ready {
		slog.Error("akun dan halaman admin tidak aktif", "err", err, "petunjuk", MsgMigrationNeeded)
		return
	}
	a.ready = true
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
		a.managed[m.email] = true
		if err := a.EnsureManagedAccount(ctx, m.email, m.name, m.role, m.password); err != nil {
			slog.Error("gagal menyiapkan akun bawaan", "email", m.email, "err", err)
		}
	}
}

// EnsureManagedAccount: buat kalau belum ada; kalau sudah ada, samakan kata sandinya dengan
// konfigurasi, tandai terverifikasi, dan (admin) pastikan tetap admin dan aktif.
func (a *AuthService) EnsureManagedAccount(ctx context.Context, email, name, role, password string) error {
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
	if !u.HasPassword || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
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
	h, err := bcrypt.GenerateFromPassword([]byte(pw), BcryptCost)
	return string(h), err
}

// dummyHash: dibandingkan saat email tidak terdaftar, supaya waktu respons login sama dengan email
// terdaftar (tidak membocorkan email mana yang punya akun).
var dummyHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("stocksta-timing-equalizer"), BcryptCost)
	return h
})

// newToken: token acak 256 bit untuk browser / link email, dan sha256-nya untuk database.
func newToken() (string, []byte) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	raw := base64.RawURLEncoding.EncodeToString(b)
	return raw, TokenHash(raw)
}

// TokenHash: sha256 token sesi / link email (yang disimpan di database).
func TokenHash(raw string) []byte {
	h := sha256.Sum256([]byte(raw))
	return h[:]
}

// present: tandai akun bawaan sebelum dikirim ke UI.
func (a *AuthService) present(u *models.User) *models.User {
	u.Managed = a.managed[u.Email]
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

func domainOf(email string) string {
	_, d, _ := strings.Cut(email, "@")
	return d
}

func emailTaken() *Error {
	return &Error{Status: http.StatusConflict, Code: "email_taken", Message: "Email sudah terdaftar.",
		Fields: FieldErrors{"email": "Email sudah terdaftar. Silakan masuk, atau pakai Lupa kata sandi."}}
}

// ---- sesi ----

// Session: pemilik sesi token raw yang masih berlaku, beserta hash dan kedaluwarsanya.
// *Error 401 kalau token tidak sah atau sudah habis.
func (a *AuthService) Session(ctx context.Context, raw string) (*models.User, []byte, time.Time, error) {
	hash := TokenHash(raw)
	u, exp, err := a.store.SessionUser(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return nil, nil, time.Time{}, newError(http.StatusUnauthorized, "session_invalid",
			"Sesi tidak valid atau sudah habis. Silakan masuk lagi.")
	}
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	return a.present(u), hash, exp, nil
}

// StartSession: buat sesi baru untuk akun yang sudah lolos pemeriksaan login.
func (a *AuthService) StartSession(ctx context.Context, u *models.User, userAgent, ip string) (map[string]any, error) {
	raw, hash := newToken()
	exp := a.now().Add(a.cfg.SessionTTL).Truncate(time.Second)
	if err := a.store.CreateSession(ctx, u.ID, hash, exp, userAgent, ip); err != nil {
		return nil, err
	}
	if err := a.store.TouchLogin(ctx, u.ID); err != nil {
		return nil, err
	}
	now := a.now().Unix()
	if last := a.lastPurge.Load(); now-last > 3600 && a.lastPurge.CompareAndSwap(last, now) {
		if err := a.store.PurgeExpired(ctx); err != nil {
			slog.Warn("bersihkan sesi kedaluwarsa gagal", "err", err)
		}
	}
	slog.Info("login", "user_id", u.ID, "role", u.Role, "ip", ip)
	return map[string]any{"token": raw, "expires_at": exp, "user": a.present(u)}, nil
}

// ---- email ----

// sendLink: buat token sekali pakai lalu kirim email berisi link ke halaman UI (path?token=...).
func (a *AuthService) sendLink(ctx context.Context, u *models.User, purpose string) error {
	ttl, path, subject, intro := verifyTokenTTL, "/verify-email", "Verifikasi email akun "+a.cfg.AppName,
		"Klik link berikut untuk memverifikasi email Anda (berlaku 24 jam):"
	if purpose == purposeResetPassword {
		ttl, path, subject, intro = resetTokenTTL, "/reset-password", "Atur ulang kata sandi "+a.cfg.AppName,
			"Kami menerima permintaan untuk mengatur ulang kata sandi akun Anda. Klik link berikut (berlaku 1 jam):"
	}
	raw, hash := newToken()
	if err := a.store.CreateAuthToken(ctx, u.ID, purpose, hash, a.now().Add(ttl)); err != nil {
		return err
	}
	link := a.cfg.AppURL + path + "?token=" + url.QueryEscape(raw)
	body := fmt.Sprintf("Halo %s,\n\n%s\n\n%s\n\nKalau Anda tidak merasa meminta ini, abaikan email ini; akun Anda tetap aman.\n\n%s\n",
		u.Name, intro, link, a.cfg.AppName)
	send := func() {
		sctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := a.cfg.Mailer.Send(sctx, u.Email, subject, body); err != nil {
			slog.Error("kirim email gagal", "purpose", purpose, "user_id", u.ID, "err", err)
		}
	}
	if a.cfg.SyncSend {
		send()
	} else {
		go send()
	}
	return nil
}

// cooldownLeft: sisa jeda sebelum email dengan tujuan ini boleh dikirim lagi ke akun u.
func (a *AuthService) cooldownLeft(ctx context.Context, u *models.User, purpose string) (time.Duration, error) {
	last, err := a.store.LastAuthTokenAt(ctx, u.ID, purpose)
	if err != nil || last.IsZero() {
		return 0, err
	}
	return max(mailCooldown-a.now().Sub(last), 0), nil
}

// ---- alur publik ----

// Config: GET /api/auth/config, fitur yang aktif untuk UI.
func (a *AuthService) Config() map[string]any {
	return map[string]any{
		"app_name":           a.cfg.AppName,
		"ready":              a.ready,
		"google":             a.cfg.Google != nil,
		"email_verification": a.cfg.RequireVerify,
		"mail":               a.cfg.Mailer.Kind(),
	}
}

// RegisterInput: body POST /api/auth/register.
type RegisterInput struct {
	Name            string `json:"name"`
	Email           string `json:"email"`
	Password        string `json:"password"`
	PasswordConfirm string `json:"password_confirm"`
}

// Register: akun baru (role user) + email verifikasi. userAgent/ip untuk sesi kalau verifikasi
// tidak diwajibkan.
func (a *AuthService) Register(ctx context.Context, in RegisterInput, userAgent, ip string) (map[string]any, error) {
	name, email := strings.TrimSpace(in.Name), normalizeEmail(in.Email)
	f := FieldErrors{}
	f.add("name", nameError(name))
	f.add("email", emailFormatError(email))
	if f["email"] == "" && a.cfg.CheckDomain != nil {
		f.add("email", a.cfg.CheckDomain(ctx, domainOf(email)))
	}
	checkNewPassword(f, in.Password, in.PasswordConfirm, email)
	if len(f) > 0 {
		return nil, invalid(f)
	}
	if _, err := a.store.UserByEmail(ctx, email); err == nil {
		return nil, emailTaken()
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	u, err := a.store.CreateUser(ctx, newUser{Email: email, Name: name, PasswordHash: hash, Role: roleUser})
	if errors.Is(err, ErrExists) {
		return nil, emailTaken()
	}
	if err != nil {
		return nil, err
	}
	slog.Info("akun baru", "user_id", u.ID)
	if err := a.sendLink(ctx, u, purposeVerifyEmail); err != nil {
		slog.Error("buat link verifikasi gagal", "user_id", u.ID, "err", err)
	}
	resp := map[string]any{"user": a.present(u), "verification_required": a.cfg.RequireVerify, "mail": a.cfg.Mailer.Kind()}
	if !a.cfg.RequireVerify {
		sess, err := a.StartSession(ctx, u, userAgent, ip)
		if err != nil {
			return nil, err
		}
		for k, v := range sess {
			resp[k] = v
		}
	}
	return resp, nil
}

// Login: email + kata sandi -> sesi {"token", "expires_at", "user"}.
func (a *AuthService) Login(ctx context.Context, rawEmail, password, userAgent, ip string) (map[string]any, error) {
	email := normalizeEmail(rawEmail)
	f := FieldErrors{}
	f.add("email", emailFormatError(email))
	if password == "" {
		f.add("password", "Kata sandi wajib diisi.")
	}
	if len(f) > 0 {
		return nil, invalid(f)
	}
	if a.logins.blocked(email) {
		return nil, newError(http.StatusTooManyRequests, "too_many_attempts",
			"Terlalu banyak percobaan login gagal untuk email ini. Coba lagi dalam 15 menit, atau pakai Lupa kata sandi.")
	}
	u, err := a.store.UserByEmail(ctx, email)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	hash := dummyHash()
	if u != nil && u.HasPassword {
		hash = []byte(u.PasswordHash)
	}
	match := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
	if u == nil || !u.HasPassword || !match {
		a.logins.fail(email)
		slog.Warn("login gagal", "ip", ip)
		return nil, newError(http.StatusUnauthorized, "invalid_credentials", "Email atau kata sandi salah.")
	}
	a.logins.reset(email)
	if u.Disabled {
		return nil, newError(http.StatusForbidden, "account_disabled", "Akun ini dinonaktifkan admin.")
	}
	if a.cfg.RequireVerify && !u.EmailVerified {
		return nil, newError(http.StatusForbidden, "email_unverified",
			"Email belum diverifikasi. Buka link verifikasi di email Anda, atau kirim ulang link-nya.")
	}
	return a.StartSession(ctx, u, userAgent, ip)
}

// tokenError: kesalahan token link email (pesan per jenis), atau err apa adanya kalau bukan
// kesalahan token.
func tokenError(err error, what, validity string) error {
	switch {
	case errors.Is(err, errTokenInvalid):
		return newError(http.StatusBadRequest, "token_invalid",
			fmt.Sprintf("Link %s tidak valid. Pastikan link disalin utuh, atau minta link baru.", what))
	case errors.Is(err, errTokenUsed):
		return newError(http.StatusConflict, "token_used",
			fmt.Sprintf("Link %s ini sudah tidak berlaku (sudah dipakai, atau sudah ada link yang lebih baru).", what))
	case errors.Is(err, errTokenExpired):
		return newError(http.StatusGone, "token_expired",
			fmt.Sprintf("Link %s sudah kedaluwarsa (berlaku %s). Minta link baru.", what, validity))
	}
	return err
}

// VerifyEmail: token dari halaman UI /verify-email?token=...
func (a *AuthService) VerifyEmail(ctx context.Context, token string) (map[string]any, error) {
	id, err := a.store.VerifyEmailToken(ctx, TokenHash(strings.TrimSpace(token)))
	if err != nil {
		return nil, tokenError(err, "verifikasi", "24 jam")
	}
	u, err := a.store.UserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	slog.Info("email terverifikasi", "user_id", id)
	return map[string]any{"verified": true, "email": u.Email}, nil
}

// emailOnly: email untuk kirim ulang verifikasi dan lupa kata sandi (422 kalau formatnya salah).
func emailOnly(raw string) (string, error) {
	email := normalizeEmail(raw)
	if msg := emailFormatError(email); msg != "" {
		return "", invalid(FieldErrors{"email": msg})
	}
	return email, nil
}

// ResendVerification: jawaban sama untuk email yang tidak terdaftar atau sudah terverifikasi.
func (a *AuthService) ResendVerification(ctx context.Context, rawEmail string) (map[string]any, error) {
	email, err := emailOnly(rawEmail)
	if err != nil {
		return nil, err
	}
	ok := map[string]any{"message": "Kalau email terdaftar dan belum diverifikasi, link verifikasi baru sudah dikirim.", "mail": a.cfg.Mailer.Kind()}
	u, err := a.store.UserByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		return ok, nil
	}
	if err != nil {
		return nil, err
	}
	if u.EmailVerified || u.Disabled {
		return ok, nil
	}
	left, err := a.cooldownLeft(ctx, u, purposeVerifyEmail)
	if err != nil {
		return nil, err
	}
	if left > 0 {
		sec := int(left.Seconds() + 0.999)
		return nil, &Error{Status: http.StatusTooManyRequests, Code: "cooldown", RetryAfter: sec,
			Message: fmt.Sprintf("Link baru saja dikirim. Tunggu %d detik sebelum mengirim ulang.", sec)}
	}
	if err := a.sendLink(ctx, u, purposeVerifyEmail); err != nil {
		return nil, err
	}
	return ok, nil
}

// ForgotPassword: jawaban selalu sama (tidak membocorkan email mana yang terdaftar). Akun bawaan
// dilewati: kata sandinya diatur dari .env/run.env.
func (a *AuthService) ForgotPassword(ctx context.Context, rawEmail string) (map[string]any, error) {
	email, err := emailOnly(rawEmail)
	if err != nil {
		return nil, err
	}
	ok := map[string]any{"message": "Kalau email terdaftar, link untuk mengatur ulang kata sandi sudah dikirim. Cek inbox dan folder spam.", "mail": a.cfg.Mailer.Kind()}
	u, err := a.store.UserByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		return ok, nil
	}
	if err != nil {
		return nil, err
	}
	if u.Disabled || a.managed[u.Email] {
		slog.Info("lupa kata sandi dilewati", "user_id", u.ID, "disabled", u.Disabled, "managed", a.managed[u.Email])
		return ok, nil
	}
	if left, err := a.cooldownLeft(ctx, u, purposeResetPassword); err != nil {
		return nil, err
	} else if left > 0 {
		return ok, nil
	}
	if err := a.sendLink(ctx, u, purposeResetPassword); err != nil {
		return nil, err
	}
	return ok, nil
}

// CheckResetToken: supaya halaman reset langsung memberi tahu kalau link-nya tidak berlaku (sebelum
// pengguna mengetik kata sandi baru). Token tidak dipakai.
func (a *AuthService) CheckResetToken(ctx context.Context, token string) (map[string]any, error) {
	id, err := a.store.PeekAuthToken(ctx, purposeResetPassword, TokenHash(strings.TrimSpace(token)))
	if err != nil {
		return nil, tokenError(err, "reset kata sandi", "1 jam")
	}
	u, err := a.store.UserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"valid": true, "email": u.Email}, nil
}

// ResetPassword: token link email + kata sandi baru; semua sesi akun dicabut.
func (a *AuthService) ResetPassword(ctx context.Context, token, password, confirm string) (map[string]any, error) {
	hash := TokenHash(strings.TrimSpace(token))
	id, err := a.store.PeekAuthToken(ctx, purposeResetPassword, hash)
	if err != nil {
		return nil, tokenError(err, "reset kata sandi", "1 jam")
	}
	u, err := a.store.UserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	f := FieldErrors{}
	checkNewPassword(f, password, confirm, u.Email)
	if len(f) > 0 {
		return nil, invalid(f)
	}
	pwHash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	if _, err := a.store.ResetPasswordToken(ctx, hash, pwHash); err != nil {
		return nil, tokenError(err, "reset kata sandi", "1 jam")
	}
	a.logins.reset(u.Email)
	slog.Info("kata sandi direset lewat email", "user_id", u.ID)
	return map[string]any{"message": "Kata sandi diganti. Silakan masuk dengan kata sandi baru.", "email": u.Email}, nil
}

// ---- akun sendiri ----

// UpdateName: PATCH /api/auth/me {"name"}.
func (a *AuthService) UpdateName(ctx context.Context, u *models.User, rawName string) (map[string]any, error) {
	name := strings.TrimSpace(rawName)
	if msg := nameError(name); msg != "" {
		return nil, invalid(FieldErrors{"name": msg})
	}
	if err := a.store.SetUserName(ctx, u.ID, name); err != nil {
		return nil, err
	}
	u.Name = name
	return map[string]any{"user": u}, nil
}

// PasswordInput: body POST /api/auth/password.
type PasswordInput struct {
	Current         string `json:"current_password"`
	Password        string `json:"password"`
	PasswordConfirm string `json:"password_confirm"`
}

// ChangePassword: akun Google tanpa kata sandi boleh membuat kata sandi pertama tanpa
// current_password. Sesi lain akun ini dicabut; sesi yang sedang dipakai (keepSession) tetap.
func (a *AuthService) ChangePassword(ctx context.Context, u *models.User, keepSession []byte, in PasswordInput) (map[string]any, error) {
	if u.Managed {
		return nil, newError(http.StatusForbidden, "managed_account",
			"Kata sandi akun bawaan diatur lewat .env / run.env (ADMIN_PASSWORD, DEMO_USER_PASSWORD), bukan dari sini.")
	}
	key := fmt.Sprintf("password:%d", u.ID)
	if a.logins.blocked(key) {
		return nil, newError(http.StatusTooManyRequests, "too_many_attempts", "Terlalu banyak percobaan. Coba lagi dalam 15 menit.")
	}
	f := FieldErrors{}
	if u.HasPassword {
		if in.Current == "" {
			f.add("current_password", "Kata sandi saat ini wajib diisi.")
		} else if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(in.Current)) != nil {
			a.logins.fail(key)
			f.add("current_password", "Kata sandi saat ini salah.")
		} else if in.Password == in.Current {
			f.add("password", "Kata sandi baru harus berbeda dari kata sandi saat ini.")
		}
	}
	checkNewPassword(f, in.Password, in.PasswordConfirm, u.Email)
	if len(f) > 0 {
		return nil, invalid(f)
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	if err := a.store.SetUserPassword(ctx, u.ID, hash, keepSession); err != nil {
		return nil, err
	}
	a.logins.reset(key)
	u.HasPassword = true
	slog.Info("kata sandi diganti", "user_id", u.ID)
	return map[string]any{"user": u, "message": "Kata sandi diganti. Sesi di perangkat lain sudah dikeluarkan."}, nil
}

// Logout: cabut satu sesi (hash token).
func (a *AuthService) Logout(ctx context.Context, sessionHash []byte) error {
	return a.store.DeleteSession(ctx, sessionHash)
}

// LogoutAll: cabut semua sesi akun.
func (a *AuthService) LogoutAll(ctx context.Context, userID int64) error {
	return a.store.DeleteUserSessions(ctx, userID)
}

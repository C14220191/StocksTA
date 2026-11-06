package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
)

// Autentikasi halaman admin. Satu kata sandi admin (ADMIN_PASSWORD di .env, rahasia) ditukar
// dengan token sesi bertanda tangan HMAC-SHA256 yang berlaku ADMIN_SESSION_HOURS (default 12).
// Token stateless (tanpa tabel sesi): kuncinya diturunkan dari kata sandi, jadi token tetap
// berlaku setelah backend restart dan otomatis batal semua kalau kata sandi diganti.
// Tanpa ADMIN_PASSWORD, seluruh /api/admin ditutup (fail closed), bukan dibuka.

const (
	loginFailWindow = 10 * time.Minute
	loginFailMax    = 10 // percobaan gagal per jendela, dihitung global (tidak per IP, yang bisa dipalsukan)
)

type adminAuth struct {
	password []byte // sha256(kata sandi); dibandingkan constant-time
	key      []byte // kunci HMAC token
	ttl      time.Duration

	mu    sync.Mutex
	fails []time.Time
	now   func() time.Time // diganti di tes
}

func newAdminAuth() *adminAuth {
	return newAdminAuthWith(os.Getenv("ADMIN_PASSWORD"), envInt("ADMIN_SESSION_HOURS", 12))
}

func newAdminAuthWith(password string, hours int) *adminAuth {
	a := &adminAuth{ttl: time.Duration(max(hours, 1)) * time.Hour, now: time.Now}
	if password == "" {
		return a
	}
	pw := sha256.Sum256([]byte(password))
	key := sha256.Sum256([]byte("stocksta-admin-session\x00" + password))
	a.password, a.key = pw[:], key[:]
	return a
}

func (a *adminAuth) enabled() bool { return a.key != nil }

func (a *adminAuth) sign(exp int64) string {
	payload := "v1." + strconv.FormatInt(exp, 10)
	m := hmac.New(sha256.New, a.key)
	m.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// issue membuat token baru beserta waktu kedaluwarsanya.
func (a *adminAuth) issue() (string, time.Time) {
	exp := a.now().Add(a.ttl).Truncate(time.Second)
	return a.sign(exp.Unix()), exp
}

// verify: token sah dan belum kedaluwarsa.
func (a *adminAuth) verify(token string) (time.Time, bool) {
	if !a.enabled() {
		return time.Time{}, false
	}
	rest, ok := strings.CutPrefix(token, "v1.")
	if !ok {
		return time.Time{}, false
	}
	expStr, _, ok := strings.Cut(rest, ".")
	if !ok {
		return time.Time{}, false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || !hmac.Equal([]byte(a.sign(exp)), []byte(token)) {
		return time.Time{}, false
	}
	t := time.Unix(exp, 0)
	if !a.now().Before(t) {
		return time.Time{}, false
	}
	return t, true
}

func (a *adminAuth) checkPassword(pw string) bool {
	got := sha256.Sum256([]byte(pw))
	return a.enabled() && subtle.ConstantTimeCompare(got[:], a.password) == 1
}

// throttled: terlalu banyak percobaan gagal dalam loginFailWindow terakhir.
func (a *adminAuth) throttled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	cut := a.now().Add(-loginFailWindow)
	kept := a.fails[:0]
	for _, t := range a.fails {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	a.fails = kept
	return len(a.fails) >= loginFailMax
}

func (a *adminAuth) recordFail() {
	a.mu.Lock()
	a.fails = append(a.fails, a.now())
	a.mu.Unlock()
}

var errAdminDisabled = map[string]string{
	"error": "Admin belum dikonfigurasi: isi ADMIN_PASSWORD di .env lalu restart backend.",
}

// AdminLogin: POST /api/admin/login {"password": "..."} -> {"token", "expires_at"}.
func (a *App) AdminLogin(c echo.Context) error {
	if !a.auth.enabled() {
		return c.JSON(http.StatusServiceUnavailable, errAdminDisabled)
	}
	if a.auth.throttled() {
		return c.JSON(http.StatusTooManyRequests,
			map[string]string{"error": "Terlalu banyak percobaan login gagal. Coba lagi dalam beberapa menit."})
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "body harus JSON {\"password\": \"...\"}"})
	}
	if !a.auth.checkPassword(body.Password) {
		a.auth.recordFail()
		slog.Warn("login admin gagal", "ip", c.RealIP())
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Kata sandi salah."})
	}
	token, exp := a.auth.issue()
	slog.Info("login admin", "ip", c.RealIP(), "berlaku_sampai", exp)
	return c.JSON(http.StatusOK, map[string]any{"token": token, "expires_at": exp})
}

// AdminSession: GET /api/admin/session, untuk UI memeriksa token yang tersimpan masih berlaku.
func (a *App) AdminSession(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{"expires_at": c.Get("admin_expires_at")})
}

// requireAdmin: middleware untuk semua /api/admin/* selain login. Header
// "Authorization: Bearer <token>".
func (a *App) requireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !a.auth.enabled() {
			return c.JSON(http.StatusServiceUnavailable, errAdminDisabled)
		}
		token, ok := strings.CutPrefix(c.Request().Header.Get(echo.HeaderAuthorization), "Bearer ")
		if !ok || strings.TrimSpace(token) == "" {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Perlu login admin."})
		}
		exp, ok := a.auth.verify(strings.TrimSpace(token))
		if !ok {
			return c.JSON(http.StatusUnauthorized,
				map[string]string{"error": "Sesi admin tidak valid atau sudah habis. Silakan login lagi."})
		}
		c.Set("admin_expires_at", exp)
		return next(c)
	}
}

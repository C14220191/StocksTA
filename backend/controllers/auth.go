package controllers

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"ta-backend/models"
	"ta-backend/services"
)

// Akun pengguna: middleware sesi / peran dan handler /api/auth/*. Logikanya di services.AuthService.

const (
	ctxUser           = "auth_user"
	ctxSessionHash    = "auth_session_hash"
	ctxSessionExpires = "auth_session_expires"
)

func bearerToken(c echo.Context) string {
	t, ok := strings.CutPrefix(c.Request().Header.Get(echo.HeaderAuthorization), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(t)
}

func currentUser(c echo.Context) *models.User {
	u, _ := c.Get(ctxUser).(*models.User)
	return u
}

func sessionHash(c echo.Context) []byte {
	h, _ := c.Get(ctxSessionHash).([]byte)
	return h
}

// AuthReady: endpoint akun hanya aktif setelah migrasi 011.
func (h *Controller) AuthReady(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !h.Auth.Ready() {
			return jsonError(c, http.StatusServiceUnavailable, "not_ready", services.MsgMigrationNeeded)
		}
		return next(c)
	}
}

// RequireUser: sesi login yang sah (akun aktif). Header "Authorization: Bearer <token>".
func (h *Controller) RequireUser(next echo.HandlerFunc) echo.HandlerFunc {
	return h.AuthReady(func(c echo.Context) error {
		raw := bearerToken(c)
		if raw == "" {
			return jsonError(c, http.StatusUnauthorized, "unauthenticated", "Perlu login.")
		}
		u, hash, exp, err := h.Auth.Session(c.Request().Context(), raw)
		if err != nil {
			return writeError(c, err)
		}
		c.Set(ctxUser, u)
		c.Set(ctxSessionHash, hash)
		c.Set(ctxSessionExpires, exp)
		return next(c)
	})
}

// RequireAdmin: seperti RequireUser, ditambah role admin (403 kalau bukan admin).
func (h *Controller) RequireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
	return h.RequireUser(func(c echo.Context) error {
		if currentUser(c).Role != "admin" {
			return jsonError(c, http.StatusForbidden, "forbidden", "Halaman ini khusus admin.")
		}
		return next(c)
	})
}

// AuthConfig: GET /api/auth/config, fitur yang aktif untuk UI.
func (h *Controller) AuthConfig(c echo.Context) error {
	return c.JSON(http.StatusOK, h.Auth.Config())
}

// Register: POST /api/auth/register {"name", "email", "password", "password_confirm"} -> 201.
func (h *Controller) Register(c echo.Context) error {
	var body services.RegisterInput
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	out, err := h.Auth.Register(c.Request().Context(), body, c.Request().UserAgent(), c.RealIP())
	return respond(c, http.StatusCreated, out, err)
}

// Login: POST /api/auth/login {"email", "password"} -> {"token", "expires_at", "user"}.
func (h *Controller) Login(c echo.Context) error {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	out, err := h.Auth.Login(c.Request().Context(), body.Email, body.Password, c.Request().UserAgent(), c.RealIP())
	return respond(c, http.StatusOK, out, err)
}

// tokenBody: body {"token"} untuk link email.
func tokenBody(c echo.Context) (string, bool) {
	var body struct {
		Token string `json:"token"`
	}
	return body.Token, c.Bind(&body) == nil
}

// emailBody: body {"email"} untuk kirim ulang verifikasi dan lupa kata sandi.
func emailBody(c echo.Context) (string, bool) {
	var body struct {
		Email string `json:"email"`
	}
	return body.Email, c.Bind(&body) == nil
}

// VerifyEmail: POST /api/auth/verify-email {"token"} (dari halaman UI /verify-email?token=...).
func (h *Controller) VerifyEmail(c echo.Context) error {
	token, ok := tokenBody(c)
	if !ok {
		return badJSON(c)
	}
	out, err := h.Auth.VerifyEmail(c.Request().Context(), token)
	return respond(c, http.StatusOK, out, err)
}

// ResendVerification: POST /api/auth/resend-verification {"email"}.
func (h *Controller) ResendVerification(c echo.Context) error {
	email, ok := emailBody(c)
	if !ok {
		return badJSON(c)
	}
	out, err := h.Auth.ResendVerification(c.Request().Context(), email)
	return respond(c, http.StatusOK, out, err)
}

// ForgotPassword: POST /api/auth/forgot-password {"email"}.
func (h *Controller) ForgotPassword(c echo.Context) error {
	email, ok := emailBody(c)
	if !ok {
		return badJSON(c)
	}
	out, err := h.Auth.ForgotPassword(c.Request().Context(), email)
	return respond(c, http.StatusOK, out, err)
}

// CheckResetToken: POST /api/auth/reset-password/check {"token"}.
func (h *Controller) CheckResetToken(c echo.Context) error {
	token, ok := tokenBody(c)
	if !ok {
		return badJSON(c)
	}
	out, err := h.Auth.CheckResetToken(c.Request().Context(), token)
	return respond(c, http.StatusOK, out, err)
}

// ResetPassword: POST /api/auth/reset-password {"token", "password", "password_confirm"}.
func (h *Controller) ResetPassword(c echo.Context) error {
	var body struct {
		Token           string `json:"token"`
		Password        string `json:"password"`
		PasswordConfirm string `json:"password_confirm"`
	}
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	out, err := h.Auth.ResetPassword(c.Request().Context(), body.Token, body.Password, body.PasswordConfirm)
	return respond(c, http.StatusOK, out, err)
}

// Me: GET /api/auth/me -> {"user", "expires_at"}.
func (h *Controller) Me(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{"user": currentUser(c), "expires_at": c.Get(ctxSessionExpires)})
}

// UpdateMe: PATCH /api/auth/me {"name"}.
func (h *Controller) UpdateMe(c echo.Context) error {
	var body struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	out, err := h.Auth.UpdateName(c.Request().Context(), currentUser(c), body.Name)
	return respond(c, http.StatusOK, out, err)
}

// ChangePassword: POST /api/auth/password {"current_password", "password", "password_confirm"}.
func (h *Controller) ChangePassword(c echo.Context) error {
	var body services.PasswordInput
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	out, err := h.Auth.ChangePassword(c.Request().Context(), currentUser(c), sessionHash(c), body)
	return respond(c, http.StatusOK, out, err)
}

// Logout: POST /api/auth/logout (sesi ini).
func (h *Controller) Logout(c echo.Context) error {
	err := h.Auth.Logout(c.Request().Context(), sessionHash(c))
	return respond(c, http.StatusOK, map[string]any{"ok": true}, err)
}

// LogoutAll: POST /api/auth/logout-all (semua perangkat).
func (h *Controller) LogoutAll(c echo.Context) error {
	err := h.Auth.LogoutAll(c.Request().Context(), currentUser(c).ID)
	return respond(c, http.StatusOK, map[string]any{"ok": true}, err)
}

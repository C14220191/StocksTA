package controllers

import (
	"log/slog"

	"github.com/labstack/echo/v4"
)

// RegisterRoutes: semua endpoint backend. requireLogin (REQUIRE_LOGIN, default on) = data pasar dan
// analisis wajib sesi login. authLimit = pembatas per IP untuk endpoint yang memakai bcrypt atau
// mengirim email.
func (h *Controller) RegisterRoutes(e *echo.Echo, requireLogin bool, authLimit echo.MiddlewareFunc) {
	e.GET("/health", h.Health)
	h.registerDataRoutes(e, requireLogin)

	adm := h.registerAuthRoutes(e, authLimit)
	adm.GET("/overview", h.AdminOverview)
	adm.GET("/cache", h.AdminCache)
	adm.POST("/cache/:symbol/invalidate", h.AdminInvalidate)
	adm.GET("/prompt", h.AdminPrompt)
	adm.GET("/evaluation", h.AdminEvaluation)
	adm.GET("/health", h.AdminHealth)
	adm.GET("/instruments", h.AdminInstruments)
	adm.GET("/instruments/lookup", h.AdminInstrumentLookup)
	adm.POST("/instruments", h.AdminAddInstrument)
	adm.POST("/instruments/:symbol/ingest", h.AdminIngestInstrument)
}

// registerDataRoutes: data pasar dan analisis AI untuk UI. Instans lab benchmark (ta-up lab / bench)
// berjalan dengan REQUIRE_LOGIN=off: k6 memanggil /api/analyze tanpa akun, dan kondisinya tetap sama
// dengan run benchmark yang sudah diukur (tanpa query sesi di jalur request).
func (h *Controller) registerDataRoutes(e *echo.Echo, requireLogin bool) {
	var mw []echo.MiddlewareFunc
	if requireLogin {
		mw = append(mw, h.RequireUser)
	} else {
		slog.Warn("REQUIRE_LOGIN=off: data dan analisis bisa diakses tanpa login (hanya untuk instans lab benchmark)")
	}
	// AI inference (melewati cache policy dan dicatat ke inference_log = data eksperimen)
	e.GET("/api/analyze/:symbol", h.Analyze, mw...)
	// Data UI (hanya baca database, tanpa AI dan tanpa inference_log)
	e.GET("/api/market/overview", h.MarketOverview, mw...)
	e.GET("/api/search", h.Search, mw...)
	e.GET("/api/instruments/:symbol", h.InstrumentDetail, mw...)
	e.GET("/api/instruments/:symbol/candles", h.InstrumentCandles, mw...)
}

// registerAuthRoutes: endpoint akun (publik dan akun sendiri) + grup /api/admin (role admin) beserta
// manajemen pengguna.
func (h *Controller) registerAuthRoutes(e *echo.Echo, authLimit echo.MiddlewareFunc) *echo.Group {
	e.GET("/api/auth/config", h.AuthConfig)
	e.POST("/api/auth/register", h.Register, h.AuthReady, authLimit)
	e.POST("/api/auth/login", h.Login, h.AuthReady, authLimit)
	e.POST("/api/auth/verify-email", h.VerifyEmail, h.AuthReady, authLimit)
	e.POST("/api/auth/resend-verification", h.ResendVerification, h.AuthReady, authLimit)
	e.POST("/api/auth/forgot-password", h.ForgotPassword, h.AuthReady, authLimit)
	e.POST("/api/auth/reset-password/check", h.CheckResetToken, h.AuthReady, authLimit)
	e.POST("/api/auth/reset-password", h.ResetPassword, h.AuthReady, authLimit)
	e.GET("/api/auth/google/start", h.GoogleStart)
	e.GET("/api/auth/google/callback", h.GoogleCallback)
	e.POST("/api/auth/google/exchange", h.GoogleExchange, h.AuthReady, authLimit)
	e.GET("/api/auth/me", h.Me, h.RequireUser)
	e.PATCH("/api/auth/me", h.UpdateMe, h.RequireUser)
	e.POST("/api/auth/password", h.ChangePassword, h.RequireUser, authLimit)
	e.POST("/api/auth/logout", h.Logout, h.RequireUser)
	e.POST("/api/auth/logout-all", h.LogoutAll, h.RequireUser)

	adm := e.Group("/api/admin", h.RequireAdmin)
	adm.GET("/users", h.AdminUsers)
	adm.POST("/users", h.AdminCreateUser)
	adm.PATCH("/users/:id", h.AdminUpdateUser)
	adm.POST("/users/:id/password-reset", h.AdminSendPasswordReset)
	adm.DELETE("/users/:id", h.AdminDeleteUser)
	return adm
}

// Package controllers berisi handler HTTP (Echo). Setiap handler hanya membaca request (path, query,
// header, body JSON), memanggil service, lalu menulis respons JSON. Logika aplikasi dan query SQL ada
// di package services, struct tabel di package models.
package controllers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"ta-backend/services"
)

// Controller: semua handler HTTP beserta service yang dipanggilnya.
type Controller struct {
	Analysis *services.AnalysisService // /api/analyze, /health
	Market   *services.MarketService   // Home, Search, Detail
	Admin    *services.AdminService    // /api/admin/* (dashboard, cache, evaluasi, instrumen)
	Auth     *services.AuthService     // akun, sesi, Google, manajemen pengguna
}

// ---- respons ----

// writeError: *services.Error ditulis dengan status dan isinya, error lain menjadi 500.
func writeError(c echo.Context, err error) error {
	var e *services.Error
	if errors.As(err, &e) {
		body := map[string]any{"error": e.Message}
		if e.Code != "" {
			body["code"] = e.Code
		}
		if e.Fields != nil {
			body["fields"] = e.Fields
		}
		if e.RetryAfter > 0 {
			body["retry_after"] = e.RetryAfter
		}
		return c.JSON(e.Status, body)
	}
	return fail(c, err)
}

func fail(c echo.Context, err error) error {
	slog.Error("request gagal", "err", err)
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
}

// respond: body dengan status kalau err nil, selain itu writeError.
func respond(c echo.Context, status int, body any, err error) error {
	if err != nil {
		return writeError(c, err)
	}
	return c.JSON(status, body)
}

// respondInstrument: seperti respond, ErrNotFound = instrument tidak ada (404).
func respondInstrument(c echo.Context, status int, body any, err error) error {
	if errors.Is(err, services.ErrNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "instrument tidak ditemukan"})
	}
	return respond(c, status, body, err)
}

func jsonError(c echo.Context, status int, code, msg string) error {
	body := map[string]any{"error": msg}
	if code != "" {
		body["code"] = code
	}
	return c.JSON(status, body)
}

func badJSON(c echo.Context) error {
	return jsonError(c, http.StatusBadRequest, "bad_request", "Body harus JSON yang valid.")
}

// ---- parameter ----

func queryInt(c echo.Context, key string, def, min, max int) int {
	v, err := strconv.Atoi(c.QueryParam(key))
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func parseAssetType(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "all", "semua":
		return "", true
	case "stock", "saham":
		return "stock", true
	case "crypto", "kripto":
		return "crypto", true
	case "commodity", "komoditas":
		return "commodity", true
	}
	return "", false
}

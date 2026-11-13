package controllers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"ta-backend/services"
)

// Analyze: GET /api/analyze/:symbol?method=debate|mtf|grounded. AI inference lewat cache policy;
// setiap request dicatat di inference_log (data eksperimen).
func (h *Controller) Analyze(c echo.Context) error {
	start := time.Now() // latency dihitung dari awal, termasuk baca database
	symbol := strings.ToUpper(c.Param("symbol"))
	if !services.ValidSymbol(symbol) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "format simbol tidak valid"})
	}
	method := strings.ToLower(c.QueryParam("method"))
	if !services.ValidMethod(method) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "method harus salah satu dari " + services.MethodsList})
	}
	// Sumber request (header X-TA-Trigger), supaya data k6 / pilot tidak bercampur dengan UI.
	trigger := strings.ToLower(c.Request().Header.Get("X-TA-Trigger"))
	if trigger == "" {
		trigger = "ui"
	}
	if !services.ValidTriggers[trigger] || trigger == "scheduled" { // scheduled khusus pre-generation internal
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "X-TA-Trigger harus ui, k6, manual, atau pilot"})
	}

	resp, err := h.Analysis.Analyze(c.Request().Context(), services.AnalyzeRequest{
		Symbol: symbol, Method: method, Trigger: trigger, Start: start,
	})
	switch {
	case errors.Is(err, services.ErrNotFound):
		return c.JSON(http.StatusNotFound, map[string]string{"error": "instrument tidak ditemukan"})
	case errors.Is(err, services.ErrNotEnoughData):
		return c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "data OHLCV belum cukup, jalankan ingest.py"})
	case err != nil:
		return fail(c, err)
	}
	return c.JSON(http.StatusOK, resp)
}

// Health: GET /health (503 kalau database atau Redis bermasalah).
func (h *Controller) Health(c echo.Context) error {
	body, ok := h.Analysis.Health(c.Request().Context())
	if !ok {
		return c.JSON(http.StatusServiceUnavailable, body)
	}
	return c.JSON(http.StatusOK, body)
}

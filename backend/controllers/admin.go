package controllers

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"ta-backend/services"
)

// Halaman admin (/admin di UI). Route-nya di bawah grup /api/admin (wajib akun ber-role admin).

// AdminOverview: GET /api/admin/overview?period=24h|7d|30d&trigger=all|ui|k6|scheduled|manual
func (h *Controller) AdminOverview(c echo.Context) error {
	out, err := h.Admin.Overview(c.Request().Context(), c.QueryParam("period"), c.QueryParam("trigger"))
	return respond(c, http.StatusOK, out, err)
}

// AdminCache: GET /api/admin/cache
func (h *Controller) AdminCache(c echo.Context) error {
	out, err := h.Admin.Cache(c.Request().Context())
	return respond(c, http.StatusOK, out, err)
}

// AdminInvalidate: POST /api/admin/cache/:symbol/invalidate?method=debate (tanpa method: semua metode)
func (h *Controller) AdminInvalidate(c echo.Context) error {
	out, err := h.Admin.Invalidate(c.Request().Context(), c.Param("symbol"), c.QueryParam("method"))
	return respondInstrument(c, http.StatusOK, out, err)
}

// AdminPrompt: GET /api/admin/prompt?symbol=BTCUSDT&method=debate
func (h *Controller) AdminPrompt(c echo.Context) error {
	out, err := h.Admin.Prompt(c.Request().Context(), c.QueryParam("symbol"), c.QueryParam("method"))
	return respondInstrument(c, http.StatusOK, out, err)
}

// AdminEvaluation: GET /api/admin/evaluation?days=30&trigger=all&prompt=current|all&method=all|debate|mtf|grounded
// [&from=RFC3339&to=RFC3339]
func (h *Controller) AdminEvaluation(c echo.Context) error {
	out, err := h.Admin.Evaluation(c.Request().Context(), services.EvalQuery{
		Days: queryInt(c, "days", 30, 1, 3650),
		From: c.QueryParam("from"), To: c.QueryParam("to"),
		Trigger: c.QueryParam("trigger"), Prompt: c.QueryParam("prompt"), Method: c.QueryParam("method"),
	})
	return respond(c, http.StatusOK, out, err)
}

// AdminHealth: GET /api/admin/health
func (h *Controller) AdminHealth(c echo.Context) error {
	out, err := h.Admin.APIHealth(c.Request().Context())
	return respond(c, http.StatusOK, out, err)
}

// AdminInstruments: GET /api/admin/instruments
func (h *Controller) AdminInstruments(c echo.Context) error {
	out, err := h.Admin.Instruments(c.Request().Context())
	return respond(c, http.StatusOK, out, err)
}

// AdminInstrumentLookup: GET /api/admin/instruments/lookup?asset_type=&symbol= (pratinjau, tanpa menyimpan)
func (h *Controller) AdminInstrumentLookup(c echo.Context) error {
	out, err := h.Admin.LookupInstrument(c.Request().Context(), c.QueryParam("asset_type"), c.QueryParam("symbol"))
	return respond(c, http.StatusOK, out, err)
}

// AdminAddInstrument: POST /api/admin/instruments {"asset_type", "symbol", "name"?}
func (h *Controller) AdminAddInstrument(c echo.Context) error {
	var body struct {
		AssetType string `json:"asset_type"`
		Symbol    string `json:"symbol"`
		Name      string `json:"name"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "body harus JSON {asset_type, symbol, name}"})
	}
	out, err := h.Admin.AddInstrument(c.Request().Context(), body.AssetType, body.Symbol, body.Name, c.RealIP())
	return respond(c, http.StatusCreated, out, err)
}

// AdminIngestInstrument: POST /api/admin/instruments/:symbol/ingest, jalankan ulang backfill.
func (h *Controller) AdminIngestInstrument(c echo.Context) error {
	out, err := h.Admin.Reingest(c.Request().Context(), c.Param("symbol"))
	return respondInstrument(c, http.StatusAccepted, out, err)
}

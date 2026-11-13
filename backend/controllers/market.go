package controllers

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// Halaman pengguna (Home, Search, Detail): hanya membaca database, tanpa AI dan tanpa inference_log.

const msgBadAssetType = "type harus all, stock, crypto, atau commodity"

// MarketOverview: GET /api/market/overview?type=all|stock|crypto|commodity&limit=5&movers=3
func (h *Controller) MarketOverview(c echo.Context) error {
	assetType, ok := parseAssetType(c.QueryParam("type"))
	if !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": msgBadAssetType})
	}
	limit := queryInt(c, "limit", 10, 1, 50)
	movers := queryInt(c, "movers", 3, 1, 20)
	out, err := h.Market.Overview(c.Request().Context(), assetType, limit, movers)
	return respond(c, http.StatusOK, out, err)
}

// Search: GET /api/search?q=antm&type=all|stock|crypto|commodity
func (h *Controller) Search(c echo.Context) error {
	assetType, ok := parseAssetType(c.QueryParam("type"))
	if !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": msgBadAssetType})
	}
	out, err := h.Market.Search(c.Request().Context(), assetType, c.QueryParam("q"))
	return respond(c, http.StatusOK, out, err)
}

// InstrumentDetail: GET /api/instruments/:symbol
func (h *Controller) InstrumentDetail(c echo.Context) error {
	out, err := h.Market.InstrumentDetail(c.Request().Context(), c.Param("symbol"))
	return respondInstrument(c, http.StatusOK, out, err)
}

// InstrumentCandles: GET /api/instruments/:symbol/candles?range=1D|1W|1M|3M|6M|1Y|ALL&tf=1h|2h|4h|6h|1d
func (h *Controller) InstrumentCandles(c echo.Context) error {
	out, err := h.Market.Candles(c.Request().Context(), c.Param("symbol"), c.QueryParam("range"), c.QueryParam("tf"))
	return respondInstrument(c, http.StatusOK, out, err)
}

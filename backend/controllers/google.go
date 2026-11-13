package controllers

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"

	"ta-backend/services"
)

// Login Google: bagian HTTP (redirect browser dan cookie). Alur dan verifikasinya dijelaskan di
// services/google.go.

const oauthCookie = "ta_oauth_state"

func (h *Controller) loginErrorURL(code string) string {
	return h.Auth.AppURL() + "/login?error=" + url.QueryEscape(code)
}

// oauthCookieFor: cookie state di path /api/auth/google host redirect URL (value kosong = hapus).
func oauthCookieFor(ru *url.URL, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: oauthCookie, Value: value, Path: strings.TrimSuffix(ru.Path, "/callback"),
		MaxAge: maxAge, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: ru.Scheme == "https",
	}
}

// GoogleStart: GET /api/auth/google/start?next=/path (navigasi browser).
func (h *Controller) GoogleStart(c echo.Context) error {
	g := h.Auth.Google()
	if g == nil || !h.Auth.Ready() {
		return c.Redirect(http.StatusFound, h.loginErrorURL("google_disabled"))
	}
	next := services.SafeNext(c.QueryParam("next"))
	ru, _ := url.Parse(g.RedirectURL())
	// Cookie state harus terpasang di host yang sama dengan callback (mis. UI memanggil 127.0.0.1
	// tetapi redirect URL memakai localhost): pindah dulu ke host itu, sekali saja (hop).
	if !strings.EqualFold(c.Request().Host, ru.Host) && c.QueryParam("hop") == "" {
		start := ru.Scheme + "://" + ru.Host + strings.TrimSuffix(ru.Path, "/callback") + "/start"
		return c.Redirect(http.StatusFound, start+"?"+url.Values{"next": {next}, "hop": {"1"}}.Encode())
	}
	state, authURL, err := g.Begin(next)
	if err != nil {
		slog.Warn("login Google ditolak", "err", err)
		return c.Redirect(http.StatusFound, h.loginErrorURL("google_busy"))
	}
	c.SetCookie(oauthCookieFor(ru, state, int(services.OAuthStateTTL.Seconds())))
	return c.Redirect(http.StatusFound, authURL)
}

// GoogleCallback: GET /api/auth/google/callback?code&state (dari Google).
func (h *Controller) GoogleCallback(c echo.Context) error {
	g := h.Auth.Google()
	if g == nil || !h.Auth.Ready() {
		return c.Redirect(http.StatusFound, h.loginErrorURL("google_disabled"))
	}
	ru, _ := url.Parse(g.RedirectURL())
	cookie, _ := c.Cookie(oauthCookie)
	c.SetCookie(oauthCookieFor(ru, "", -1))

	if e := c.QueryParam("error"); e != "" {
		if e == "access_denied" {
			return c.Redirect(http.StatusFound, h.loginErrorURL("google_cancelled"))
		}
		slog.Warn("Google mengembalikan error", "error", e[:min(len(e), 100)])
		return c.Redirect(http.StatusFound, h.loginErrorURL("google_failed"))
	}
	state := c.QueryParam("state")
	if state == "" || cookie == nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
		slog.Warn("login Google: state tidak cocok dengan cookie", "ip", c.RealIP())
		return c.Redirect(http.StatusFound, h.loginErrorURL("google_state"))
	}
	code, err := h.Auth.GoogleCallback(c.Request().Context(), state, c.QueryParam("code"))
	switch {
	case errors.Is(err, services.ErrOAuthState):
		return c.Redirect(http.StatusFound, h.loginErrorURL("google_state"))
	case errors.Is(err, services.ErrGoogleUnverified):
		return c.Redirect(http.StatusFound, h.loginErrorURL("google_unverified"))
	case errors.Is(err, services.ErrAccountDisabled):
		return c.Redirect(http.StatusFound, h.loginErrorURL("account_disabled"))
	case errors.Is(err, services.ErrGoogleConflict):
		return c.Redirect(http.StatusFound, h.loginErrorURL("google_conflict"))
	case err != nil:
		slog.Error("login Google gagal", "err", err)
		return c.Redirect(http.StatusFound, h.loginErrorURL("google_failed"))
	}
	return c.Redirect(http.StatusFound, h.Auth.AppURL()+"/auth/google?code="+url.QueryEscape(code))
}

// GoogleExchange: POST /api/auth/google/exchange {"code"} -> sesi seperti login biasa + "next".
func (h *Controller) GoogleExchange(c echo.Context) error {
	if h.Auth.Google() == nil {
		return writeError(c, services.ErrGoogleDisabled)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := c.Bind(&body); err != nil {
		return badJSON(c)
	}
	out, err := h.Auth.GoogleExchange(c.Request().Context(), body.Code, c.Request().UserAgent(), c.RealIP())
	return respond(c, http.StatusOK, out, err)
}

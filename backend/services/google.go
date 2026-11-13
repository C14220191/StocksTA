package services

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"ta-backend/models"
)

// Login Google: OAuth 2.0 authorization code + PKCE, OpenID Connect.
//
//  1. UI membuka GET /api/auth/google/start?next=/... sebagai navigasi penuh (bukan fetch). Backend
//     membuat state + nonce + code_verifier, memasang cookie state, lalu redirect ke Google.
//  2. Google redirect ke GOOGLE_REDIRECT_URL (/api/auth/google/callback?code&state).
//  3. Backend memverifikasi, menukar code dengan ID token, mencari / membuat / menautkan akun, lalu
//     redirect ke UI APP_URL/auth/google?code=<kode login sekali pakai, 2 menit>.
//  4. UI menukar kode itu dengan token sesi: POST /api/auth/google/exchange {"code"}. Token sesi tidak
//     pernah muncul di URL.
//
// Verifikasi: state harus sama dengan cookie di browser yang memulai login (mencegah login CSRF),
// sekali pakai, 10 menit; PKCE S256 (code tidak berguna tanpa code_verifier yang hanya ada di
// backend); ID token: tanda tangan RS256 dengan kunci publik Google (JWKS), iss, aud = client ID,
// exp, iat, nonce; dan email_verified harus true.
//
// Konfigurasi (.env): GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, GOOGLE_REDIRECT_URL (default
// http://localhost:$PORT/api/auth/google/callback). Redirect URL itu harus didaftarkan persis sama
// di Google Cloud Console (Credentials > OAuth client ID > Web application > Authorized redirect URIs).

const (
	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
	googleJWKSURL  = "https://www.googleapis.com/oauth2/v3/certs"
	// OAuthStateTTL: umur state login Google (dan cookie pengikatnya di browser).
	OAuthStateTTL   = 10 * time.Minute
	loginCodeTTL    = 2 * time.Minute
	oauthMaxPending = 5000
	idTokenSkew     = time.Minute
)

var (
	ErrAccountDisabled  = errors.New("akun dinonaktifkan")
	ErrGoogleConflict   = errors.New("akun lokal sudah tertaut ke akun Google lain")
	ErrOAuthBusy        = errors.New("terlalu banyak login Google yang belum selesai")
	ErrOAuthState       = errors.New("state login Google tidak dikenal, sudah dipakai, atau kedaluwarsa")
	ErrGoogleUnverified = errors.New("email akun Google belum diverifikasi Google")
)

// ErrGoogleDisabled: GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET belum diisi (503).
var ErrGoogleDisabled = newError(http.StatusServiceUnavailable, "google_disabled", "Login Google belum dikonfigurasi di backend.")

type oauthPending struct {
	nonce, verifier, next string
	exp                   time.Time
}

type loginCode struct {
	userID int64
	next   string
	exp    time.Time
}

// GoogleOAuth: konfigurasi dan keadaan login Google satu backend.
type GoogleOAuth struct {
	clientID, clientSecret, redirectURL string
	AuthURL, TokenURL, JWKSURL          string // endpoint Google (diganti di tes)
	client                              *http.Client
	now                                 func() time.Time

	mu      sync.Mutex
	pending map[string]oauthPending // state -> data login yang sedang berjalan
	codes   map[string]loginCode    // kode login sekali pakai -> akun

	keysMu      sync.Mutex
	keys        map[string]*rsa.PublicKey // kid -> kunci publik Google
	keysExp     time.Time
	keysFetched time.Time
}

func newGoogleOAuth() *GoogleOAuth {
	id, secret := strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_ID")), strings.TrimSpace(os.Getenv("GOOGLE_CLIENT_SECRET"))
	if id == "" || secret == "" {
		slog.Info("GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET kosong: login Google tidak aktif")
		return nil
	}
	redirect := env("GOOGLE_REDIRECT_URL", "http://localhost:"+env("PORT", "8080")+"/api/auth/google/callback")
	if u, err := url.Parse(redirect); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		slog.Error("GOOGLE_REDIRECT_URL tidak valid, login Google tidak aktif", "url", redirect)
		return nil
	}
	return NewGoogleOAuth(id, secret, redirect)
}

// NewGoogleOAuth: login Google dengan client ID/secret dan redirect URL tertentu.
func NewGoogleOAuth(id, secret, redirect string) *GoogleOAuth {
	return &GoogleOAuth{
		clientID: id, clientSecret: secret, redirectURL: redirect,
		AuthURL: googleAuthURL, TokenURL: googleTokenURL, JWKSURL: googleJWKSURL,
		client:  &http.Client{Timeout: 15 * time.Second},
		now:     time.Now,
		pending: map[string]oauthPending{},
		codes:   map[string]loginCode{},
	}
}

// RedirectURL: GOOGLE_REDIRECT_URL (alamat callback di backend).
func (g *GoogleOAuth) RedirectURL() string { return g.redirectURL }

func randomToken() string {
	raw, _ := newToken()
	return raw
}

// SafeNext: tujuan setelah login hanya path di UI sendiri (bukan URL luar: mencegah open redirect).
func SafeNext(next string) string {
	if next == "" || len(next) > 512 || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") ||
		strings.HasPrefix(next, "/\\") || strings.ContainsAny(next, "\r\n\t") {
		return "/"
	}
	return next
}

// Begin: simpan login baru, kembalikan state dan URL halaman izin Google.
func (g *GoogleOAuth) Begin(next string) (string, string, error) {
	state, nonce, verifier := randomToken(), randomToken(), randomToken()
	now := g.now()
	g.mu.Lock()
	for k, p := range g.pending {
		if now.After(p.exp) {
			delete(g.pending, k)
		}
	}
	if len(g.pending) >= oauthMaxPending {
		g.mu.Unlock()
		return "", "", ErrOAuthBusy
	}
	g.pending[state] = oauthPending{nonce: nonce, verifier: verifier, next: next, exp: now.Add(OAuthStateTTL)}
	g.mu.Unlock()

	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id":             {g.clientID},
		"redirect_uri":          {g.redirectURL},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"},
	}
	return state, g.AuthURL + "?" + q.Encode(), nil
}

// take: ambil (dan hapus) login yang sedang berjalan; false kalau tidak ada atau kedaluwarsa.
func (g *GoogleOAuth) take(state string) (oauthPending, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.pending[state]
	delete(g.pending, state)
	return p, ok && g.now().Before(p.exp)
}

// IssueCode: kode login sekali pakai (2 menit) untuk akun userID.
func (g *GoogleOAuth) IssueCode(userID int64, next string) string {
	code := randomToken()
	now := g.now()
	g.mu.Lock()
	for k, c := range g.codes {
		if now.After(c.exp) {
			delete(g.codes, k)
		}
	}
	g.codes[code] = loginCode{userID: userID, next: next, exp: now.Add(loginCodeTTL)}
	g.mu.Unlock()
	return code
}

func (g *GoogleOAuth) redeem(code string) (loginCode, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.codes[code]
	delete(g.codes, code)
	return c, ok && g.now().Before(c.exp)
}

// exchange: tukar authorization code dengan ID token (back-channel ke Google, dengan PKCE verifier).
func (g *GoogleOAuth) exchange(ctx context.Context, code, verifier string) (string, error) {
	if code == "" {
		return "", errors.New("code kosong")
	}
	form := url.Values{
		"code":          {code},
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"redirect_uri":  {g.redirectURL},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var tr struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tr); err != nil || tr.IDToken == "" {
		return "", errors.New("respons token endpoint tanpa id_token")
	}
	return tr.IDToken, nil
}

// flexBool: email_verified kadang dikirim sebagai string "true".
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	*b = flexBool(s == "true")
	return nil
}

// audience: klaim aud bisa string atau array.
type audience []string

func (a *audience) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*a = audience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

type GoogleClaims struct {
	Iss           string   `json:"iss"`
	Aud           audience `json:"aud"`
	Azp           string   `json:"azp"`
	Sub           string   `json:"sub"`
	Email         string   `json:"email"`
	EmailVerified flexBool `json:"email_verified"`
	Name          string   `json:"name"`
	Nonce         string   `json:"nonce"`
	Exp           int64    `json:"exp"`
	Iat           int64    `json:"iat"`
}

func decodeSegment(seg string, dst any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// verifyIDToken: tanda tangan + klaim ID token (OpenID Connect Core 3.1.3.7).
func (g *GoogleOAuth) verifyIDToken(ctx context.Context, raw, nonce string) (*GoogleClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("ID token bukan JWT")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeSegment(parts[0], &hdr); err != nil {
		return nil, fmt.Errorf("header ID token: %w", err)
	}
	if hdr.Alg != "RS256" {
		return nil, fmt.Errorf("algoritma ID token %q ditolak (harus RS256)", hdr.Alg)
	}
	key, err := g.publicKey(ctx, hdr.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("tanda tangan ID token: %w", err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return nil, errors.New("tanda tangan ID token tidak sah")
	}
	var cl GoogleClaims
	if err := decodeSegment(parts[1], &cl); err != nil {
		return nil, fmt.Errorf("klaim ID token: %w", err)
	}
	now := g.now()
	switch {
	case cl.Iss != "https://accounts.google.com" && cl.Iss != "accounts.google.com":
		return nil, fmt.Errorf("iss %q bukan Google", cl.Iss)
	case !containsString(cl.Aud, g.clientID):
		return nil, errors.New("aud bukan GOOGLE_CLIENT_ID aplikasi ini")
	case len(cl.Aud) > 1 && cl.Azp != g.clientID:
		return nil, errors.New("azp bukan GOOGLE_CLIENT_ID aplikasi ini")
	case cl.Exp == 0 || now.After(time.Unix(cl.Exp, 0).Add(idTokenSkew)):
		return nil, errors.New("ID token kedaluwarsa")
	case time.Unix(cl.Iat, 0).After(now.Add(5 * time.Minute)):
		return nil, errors.New("iat ID token di masa depan")
	case nonce == "" || subtle.ConstantTimeCompare([]byte(cl.Nonce), []byte(nonce)) != 1:
		return nil, errors.New("nonce ID token tidak cocok")
	case cl.Sub == "":
		return nil, errors.New("ID token tanpa sub")
	case cl.Email == "":
		return nil, errors.New("ID token tanpa email (scope email?)")
	}
	return &cl, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

var maxAgeRe = regexp.MustCompile(`max-age=(\d+)`)

// publicKey: kunci publik Google untuk kid, dari cache JWKS (umur mengikuti Cache-Control). Kid yang
// tidak dikenal memicu ambil ulang (rotasi kunci), paling sering sekali per menit.
func (g *GoogleOAuth) publicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	g.keysMu.Lock()
	defer g.keysMu.Unlock()
	now := g.now()
	if k, ok := g.keys[kid]; ok && now.Before(g.keysExp) {
		return k, nil
	}
	if _, ok := g.keys[kid]; !ok && now.Before(g.keysExp) && now.Sub(g.keysFetched) < time.Minute {
		return nil, fmt.Errorf("kid ID token %q tidak dikenal", kid)
	}
	keys, exp, err := g.fetchKeys(ctx)
	if err != nil {
		if k, ok := g.keys[kid]; ok { // kunci lama masih lebih baik daripada gagal total
			slog.Warn("ambil JWKS Google gagal, pakai kunci tersimpan", "err", err)
			return k, nil
		}
		return nil, err
	}
	g.keys, g.keysExp, g.keysFetched = keys, exp, now
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("kid ID token %q tidak dikenal", kid)
}

func (g *GoogleOAuth) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.JWKSURL, nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("JWKS Google: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, fmt.Errorf("JWKS Google HTTP %d", resp.StatusCode)
	}
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, time.Time{}, fmt.Errorf("JWKS Google: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	if len(keys) == 0 {
		return nil, time.Time{}, errors.New("JWKS Google tanpa kunci RSA")
	}
	ttl := time.Hour
	if m := maxAgeRe.FindStringSubmatch(resp.Header.Get("Cache-Control")); m != nil {
		if sec, err := strconv.Atoi(m[1]); err == nil {
			ttl = min(max(time.Duration(sec)*time.Second, time.Minute), 24*time.Hour)
		}
	}
	return keys, g.now().Add(ttl), nil
}

// GoogleCallback: selesaikan login dari callback Google (state sudah dicek controller terhadap
// cookie): ambil data login, tukar code, verifikasi ID token, cari / buat / tautkan akun, lalu terbitkan
// kode login sekali pakai untuk UI. Error: ErrOAuthState, ErrGoogleUnverified, ErrAccountDisabled,
// ErrGoogleConflict, selain itu gagal verifikasi.
func (a *AuthService) GoogleCallback(ctx context.Context, state, code string) (string, error) {
	g := a.cfg.Google
	p, ok := g.take(state)
	if !ok {
		return "", ErrOAuthState
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	idToken, err := g.exchange(ctx, code, p.verifier)
	if err != nil {
		return "", fmt.Errorf("tukar code gagal: %w", err)
	}
	claims, err := g.verifyIDToken(ctx, idToken, p.nonce)
	if err != nil {
		return "", fmt.Errorf("ID token ditolak: %w", err)
	}
	if !claims.EmailVerified {
		return "", ErrGoogleUnverified
	}
	u, err := a.GoogleUser(ctx, claims)
	if err != nil {
		return "", err
	}
	return g.IssueCode(u.ID, p.next), nil
}

// GoogleExchange: POST /api/auth/google/exchange {"code"} -> sesi seperti login biasa + "next".
func (a *AuthService) GoogleExchange(ctx context.Context, code, userAgent, ip string) (map[string]any, error) {
	g := a.cfg.Google
	if g == nil {
		return nil, ErrGoogleDisabled
	}
	invalidCode := newError(http.StatusBadRequest, "google_code", "Kode login Google tidak valid atau kedaluwarsa. Silakan masuk lagi.")
	lc, ok := g.redeem(strings.TrimSpace(code))
	if !ok {
		return nil, invalidCode
	}
	u, err := a.store.UserByID(ctx, lc.userID)
	if errors.Is(err, ErrNotFound) {
		return nil, invalidCode
	}
	if err != nil {
		return nil, err
	}
	if u.Disabled {
		return nil, newError(http.StatusForbidden, "account_disabled", "Akun ini dinonaktifkan admin.")
	}
	sess, err := a.StartSession(ctx, u, userAgent, ip)
	if err != nil {
		return nil, err
	}
	sess["next"] = lc.next
	return sess, nil
}

// GoogleUser: akun untuk login Google. Urutan: akun yang sudah tertaut (sub), lalu akun dengan
// email yang sama (ditautkan; lihat Store.LinkGoogle), lalu akun baru (role user, terverifikasi,
// tanpa kata sandi).
func (a *AuthService) GoogleUser(ctx context.Context, cl *GoogleClaims) (*models.User, error) {
	u, err := a.store.UserByGoogle(ctx, cl.Sub)
	if err == nil {
		if u.Disabled {
			return nil, ErrAccountDisabled
		}
		return u, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	email := normalizeEmail(cl.Email)
	u, err = a.store.UserByEmail(ctx, email)
	if err == nil {
		if u.Disabled {
			return nil, ErrAccountDisabled
		}
		if u.GoogleLinked {
			return nil, ErrGoogleConflict
		}
		if err := a.store.LinkGoogle(ctx, u.ID, cl.Sub, u.EmailVerified); err != nil {
			if errors.Is(err, ErrExists) {
				return nil, ErrGoogleConflict
			}
			return nil, err
		}
		slog.Info("akun ditautkan ke Google", "user_id", u.ID, "kata_sandi_dihapus", !u.EmailVerified)
		return a.store.UserByID(ctx, u.ID)
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if msg := emailFormatError(email); msg != "" {
		return nil, fmt.Errorf("email Google %q: %s", email, msg)
	}
	name := strings.TrimSpace(cl.Name)
	if nameError(name) != "" {
		name, _, _ = strings.Cut(email, "@")
		if nameError(name) != "" {
			name = "Pengguna Google"
		}
	}
	u, err = a.store.CreateUser(ctx, newUser{Email: email, Name: name, Role: roleUser, GoogleSub: cl.Sub, Verified: true})
	if errors.Is(err, ErrExists) { // login Google bersamaan untuk akun yang sama
		return a.store.UserByGoogle(ctx, cl.Sub)
	}
	if err == nil {
		slog.Info("akun baru lewat Google", "user_id", u.ID)
	}
	return u, err
}

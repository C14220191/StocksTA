package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(env(key, ""), 64); err == nil {
		return v
	}
	return def
}

func main() {
	ctx := context.Background()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL belum di-set")
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("gagal membuat pool: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("tidak bisa konek ke database: %v", err)
	}

	// CACHE_MODE menentukan kondisi eksperimen: no_cache | static_ttl | dynamic_ttl | event_only | hybrid
	// (+ INVALIDATION_THRESHOLD_PCT, INVALIDATE_ON_STATE, PREGEN). Lihat cache.go.
	store := &Store{pool: pool}
	cfg, err := loadPolicyConfig()
	if err != nil {
		log.Fatal(err)
	}
	policy, err := NewPolicy(ctx, cfg, store)
	if err != nil {
		log.Fatal(err)
	}

	ai := NewAIClient(env("AI_MODE", "stub"), env("AI_URL", "http://127.0.0.1:8000/analyze"))

	app := &App{
		store:  store,
		policy: policy,
		cfg:    cfg,
		ai:     ai,
		rdb:    newAdminRedis(),
		auth:   newAuthService(),
		ingest: newIngestRunner(store),
	}
	app.initAuth(ctx) // akun (migrasi 011) + akun bawaan admin / demo user, lihat auth.go

	// Watcher: event-driven invalidation proaktif untuk kondisi yang memakai θ (event_only, hybrid).
	var watcher *Watcher
	if rp, ok := policy.(*RedisPolicy); ok && rp.threshold > 0 {
		watcher = newWatcher(app, rp)
		go watcher.Run(ctx)
	}

	// Price feed (WebSocket Binance) berjalan di SEMUA kondisi (termasuk no_cache), supaya harga di
	// prompt dan metrik freshness berbasis harga diukur dengan cara yang sama di setiap kondisi.
	if env("PRICE_FEED", "on") == "on" {
		spot, err := store.SymbolsBySource(ctx, "binance")
		if err != nil {
			log.Fatalf("baca instrument binance: %v", err)
		}
		futures, err := store.SymbolsBySource(ctx, "binance_futures")
		if err != nil {
			log.Fatalf("baca instrument binance_futures: %v", err)
		}
		app.prices = NewPriceFeed(spot, futures)
		if watcher != nil {
			app.prices.onTick = watcher.OnTick
		}
		app.prices.Run(ctx)
	}

	if cfg.pregen {
		if app.pregen, err = loadPregenConfig(); err != nil {
			log.Fatal(err)
		}
		go app.runPregen(ctx)
	}

	e := echo.New()
	e.HideBanner = true
	// IP klien untuk rate limit dan log: di belakang reverse proxy lokal (Tailscale Funnel, nginx) ambil IP
	// paling kanan di X-Forwarded-For yang bukan proxy tepercaya (loopback / jaringan privat). Default Echo
	// memakai IP paling kiri, yang bisa diisi klien sendiri untuk menghindari pembatas per IP.
	e.IPExtractor = echo.ExtractIPFromXFFHeader()
	e.Use(middleware.Recover())
	// CORS untuk dev server React (Vite :5173 / CRA :3000). POST/PATCH/DELETE hanya untuk akun dan aksi
	// admin; header Authorization (token sesi) dan Content-Type (body JSON) membuat browser mengirim
	// preflight, yang di-cache MaxAge detik supaya halaman admin yang polling tidak menggandakan request.
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: strings.Split(env("CORS_ORIGINS", "http://localhost:5173,http://127.0.0.1:5173,http://localhost:3000"), ","),
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete},
		AllowHeaders: []string{echo.HeaderAuthorization, echo.HeaderContentType, "X-TA-Trigger"},
		MaxAge:       600,
	}))
	// Rate limiting per IP (Bab 3.3.1). k6 dan pilot dikecualikan supaya beban uji tidak terpotong;
	// RATE_LIMIT_RPS=0 mematikannya.
	if rps, _ := strconv.ParseFloat(env("RATE_LIMIT_RPS", "20"), 64); rps > 0 {
		e.Use(middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
			Skipper: func(c echo.Context) bool {
				t := strings.ToLower(c.Request().Header.Get("X-TA-Trigger"))
				return t == "k6" || t == "pilot" || !strings.HasPrefix(c.Path(), "/api/")
			},
			Store: middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
				Rate: rate.Limit(rps), Burst: int(rps * 2), ExpiresIn: 3 * time.Minute,
			}),
			DenyHandler: func(c echo.Context, _ string, _ error) error {
				return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "terlalu banyak request, coba lagi sebentar"})
			},
		}))
	}
	e.GET("/health", app.Health)
	app.registerDataRoutes(e, env("REQUIRE_LOGIN", "on") != "off")

	// Akun pengguna (auth.go, google.go). Login -> token sesi; endpoint akun sendiri dan /api/admin/*
	// wajib "Authorization: Bearer <token>", /api/admin/* juga wajib role admin. Endpoint yang memakai
	// bcrypt atau mengirim email dibatasi lebih ketat per IP (authLimit).
	authLimit := middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
			Rate: rate.Limit(envFloat("AUTH_RATE_LIMIT_RPS", 1)), Burst: 10, ExpiresIn: 10 * time.Minute,
		}),
		DenyHandler: func(c echo.Context, _ string, _ error) error {
			return jsonError(c, http.StatusTooManyRequests, "rate_limited", "Terlalu banyak percobaan, tunggu sebentar lalu coba lagi.")
		},
	})
	adm := app.registerAuthRoutes(e, authLimit)
	adm.GET("/overview", app.AdminOverview)
	adm.GET("/cache", app.AdminCache)
	adm.POST("/cache/:symbol/invalidate", app.AdminInvalidate)
	adm.GET("/prompt", app.AdminPrompt)
	adm.GET("/evaluation", app.AdminEvaluation)
	adm.GET("/health", app.AdminHealth)
	adm.GET("/instruments", app.AdminInstruments)
	adm.GET("/instruments/lookup", app.AdminInstrumentLookup)
	adm.POST("/instruments", app.AdminAddInstrument)
	adm.POST("/instruments/:symbol/ingest", app.AdminIngestInstrument)

	port := env("PORT", "8080")
	slog.Info("server jalan", "port", port, "instance", cfg.instanceKey(), "cache_mode", policy.Name(),
		"check_state", cfg.checkState, "pregen", cfg.pregen, "ai_mode", env("AI_MODE", "stub"))
	log.Fatal(e.Start(env("LISTEN_HOST", "127.0.0.1") + ":" + port))
}

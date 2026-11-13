// Backend StocksTA: cached AI inference untuk analisis saham IDX, kripto, dan komoditas.
//
// Struktur:
//
//	models/       struct tabel database (ohlcv, ai_summaries, inference_log, cache_events, users, ...)
//	services/     logika + query SQL: cache policy, AI, price feed, watcher, pre-generation, evaluasi, akun
//	controllers/  handler HTTP (Echo): baca request, panggil service, tulis respons JSON; daftar route
//	main.go       wiring: konfigurasi, database, Redis, kondisi eksperimen, server
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

	"ta-backend/controllers"
	"ta-backend/services"
)

var env = services.Env

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
	// (+ INVALIDATION_THRESHOLD_PCT, INVALIDATE_ON_STATE, PREGEN). Lihat services/cache.go.
	store := services.NewStore(pool)
	cfg, err := services.LoadPolicyConfig()
	if err != nil {
		log.Fatal(err)
	}
	policy, err := services.NewPolicy(ctx, cfg, store)
	if err != nil {
		log.Fatal(err)
	}

	ai := services.NewAIClient(env("AI_MODE", "stub"), env("AI_URL", "http://127.0.0.1:8000/analyze"))
	analysis := services.NewAnalysisService(store, policy, cfg, ai, services.NewAdminRedis())
	auth := services.NewAuthService(store)
	auth.Init(ctx) // akun (migrasi 011) + akun bawaan admin / demo user, lihat services/auth.go

	// Watcher: event-driven invalidation proaktif untuk kondisi yang memakai θ (event_only, hybrid).
	var watcher *services.Watcher
	if rp, ok := policy.(*services.RedisPolicy); ok && rp.Threshold() != nil {
		watcher = services.NewWatcher(analysis, rp)
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
		feed := services.NewPriceFeed(spot, futures)
		analysis.SetPriceFeed(feed)
		if watcher != nil {
			feed.SetTickHandler(watcher.OnTick)
		}
		feed.Run(ctx)
	}

	if cfg.PregenOn() {
		pc, err := services.LoadPregenConfig()
		if err != nil {
			log.Fatal(err)
		}
		analysis.SetPregen(pc)
		go analysis.RunPregen(ctx)
	}

	h := &controllers.Controller{
		Analysis: analysis,
		Market:   services.NewMarketService(store),
		Admin:    services.NewAdminService(analysis, services.NewIngestRunner(store)),
		Auth:     auth,
	}

	e := echo.New()
	e.HideBanner = true
	// IP klien untuk rate limit dan log: di belakang reverse proxy lokal (Tailscale Funnel, nginx) ambil IP
	// paling kanan di X-Forwarded-For yang bukan proxy tepercaya (loopback / jaringan privat). Default Echo
	// memakai IP paling kiri, yang bisa diisi klien sendiri untuk menghindari pembatas per IP.
	e.IPExtractor = echo.ExtractIPFromXFFHeader()
	e.Use(middleware.Recover())
	// CORS untuk dev server React (Vite :5173 / CRA :3000) dan UI Vercel. POST/PATCH/DELETE hanya untuk akun
	// dan aksi admin; header Authorization (token sesi) dan Content-Type (body JSON) membuat browser
	// mengirim preflight, yang di-cache MaxAge detik supaya halaman admin yang polling tidak menggandakan request.
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
	// Endpoint akun yang memakai bcrypt atau mengirim email dibatasi lebih ketat per IP.
	authLimit := middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
			Rate: rate.Limit(services.EnvFloat("AUTH_RATE_LIMIT_RPS", 1)), Burst: 10, ExpiresIn: 10 * time.Minute,
		}),
		DenyHandler: func(c echo.Context, _ string, _ error) error {
			return c.JSON(http.StatusTooManyRequests, map[string]any{"error": "Terlalu banyak percobaan, tunggu sebentar lalu coba lagi.", "code": "rate_limited"})
		},
	})
	h.RegisterRoutes(e, env("REQUIRE_LOGIN", "on") != "off", authLimit)

	port := env("PORT", "8080")
	slog.Info("server jalan", "port", port, "instance", cfg.InstanceKey(), "cache_mode", policy.Name(),
		"check_state", cfg.CheckState(), "pregen", cfg.PregenOn(), "ai_mode", env("AI_MODE", "stub"))
	log.Fatal(e.Start(env("LISTEN_HOST", "127.0.0.1") + ":" + port))
}

package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
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
	store := &Store{pool: pool}
	cacheMode := env("CACHE_MODE", "no_cache")
	policy, err := NewPolicy(ctx, cacheMode, store)
	if err != nil {
		log.Fatal(err)
	}

	ai := NewAIClient(env("AI_MODE", "stub"), env("AI_URL", "http://127.0.0.1:8000/analyze"))

	// Price feed berjalan di SEMUA kondisi (termasuk no_cache), supaya harga di prompt dan
	// metrik freshness berbasis harga diukur dengan cara yang sama di setiap kondisi.
	var prices *PriceFeed
	if env("PRICE_FEED", "on") == "on" {
		spot, err := store.SymbolsBySource(ctx, "binance")
		if err != nil {
			log.Fatalf("baca instrument binance: %v", err)
		}
		futures, err := store.SymbolsBySource(ctx, "binance_futures")
		if err != nil {
			log.Fatalf("baca instrument binance_futures: %v", err)
		}
		prices = NewPriceFeed(spot, futures)
		go prices.Run(ctx)
	}

	app := &App{
		store:  store,
		policy: policy,
		ai:     ai,
		prices: prices,
	}

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover())
	// CORS untuk dev server React (Vite :5173 / CRA :3000). Hanya GET yang dipakai UI.
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: strings.Split(env("CORS_ORIGINS", "http://localhost:5173,http://127.0.0.1:5173,http://localhost:3000"), ","),
		AllowMethods: []string{http.MethodGet},
	}))
	e.GET("/health", app.Health)

	// AI inference (melewati cache policy dan dicatat ke inference_log = data eksperimen)
	e.GET("/api/analyze/:symbol", app.Analyze)

	// Data UI (hanya baca database, tanpa AI dan tanpa inference_log)
	e.GET("/api/market/overview", app.MarketOverview)
	e.GET("/api/search", app.Search)
	e.GET("/api/instruments/:symbol", app.InstrumentDetail)
	e.GET("/api/instruments/:symbol/candles", app.InstrumentCandles)

	port := env("PORT", "8080")
	slog.Info("server jalan", "port", port, "cache_mode", policy.Name(), "ai_mode", env("AI_MODE", "stub"))
	log.Fatal(e.Start(env("LISTEN_HOST", "127.0.0.1") + ":" + port))
}

package services

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Konfigurasi dibaca dari environment (.env + run.env, dimuat ta-up).

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Env: nilai environment atau def kalau kosong.
func Env(key, def string) string { return env(key, def) }

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(env(key, "")); err == nil {
		return v
	}
	return def
}

// EnvFloat: angka desimal dari environment, def kalau kosong / tidak valid.
func EnvFloat(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(env(key, ""), 64); err == nil {
		return v
	}
	return def
}

func envSeconds(key string, def int) (time.Duration, error) {
	sec, err := strconv.Atoi(env(key, strconv.Itoa(def)))
	if err != nil || sec <= 0 {
		return 0, fmt.Errorf("%s tidak valid", key)
	}
	return time.Duration(sec) * time.Second, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ingestRunner menjalankan backfill untuk instrumen yang baru ditambahkan lewat admin, memakai skrip
// ingestion Python yang sama dengan cron (ingest.py, fundamentals.py), supaya hanya ada satu jalur
// pengambilan data. Satu backfill per simbol pada satu waktu. Skrip Python mencatat hasilnya sendiri
// di job_runs; runner hanya menambah baris fail kalau skrip tidak bisa dijalankan sama sekali.
//
// Lokasi: TA_ROOT (default "..", karena backend berjalan dari folder backend/), Python dari
// INGEST_PYTHON atau .venv di TA_ROOT. Environment backend (DATABASE_URL, BINANCE_URL, ...) diwariskan.
type ingestRunner struct {
	store *Store
	exec  func(ctx context.Context, script string, args ...string) error // diganti di tes

	mu   sync.Mutex
	busy map[string]bool
	wg   sync.WaitGroup // hanya untuk tes
}

func newIngestRunner(store *Store) *ingestRunner {
	return &ingestRunner{store: store, exec: runPython, busy: map[string]bool{}}
}

func (r *ingestRunner) running(symbol string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.busy[symbol]
}

// ingestSteps: urutan skrip untuk satu instrumen.
func ingestSteps(symbol, assetType string) [][]string {
	steps := [][]string{
		{"ingest.py", "--timeframe", "1h", "--symbols", symbol},
		{"ingest.py", "--timeframe", "1d", "--symbols", symbol},
	}
	if assetType == "stock" {
		steps = append(steps, []string{"fundamentals.py", "--symbols", symbol})
	}
	return steps
}

// jobOf: nama job di job_runs untuk satu langkah, sama dengan yang ditulis skrip Python.
func jobOf(step []string) string {
	if step[0] == "fundamentals.py" {
		return "fundamentals"
	}
	return "ingest_" + step[2]
}

// start: false kalau backfill simbol ini sedang berjalan.
func (r *ingestRunner) start(symbol, assetType string) bool {
	r.mu.Lock()
	if r.busy[symbol] {
		r.mu.Unlock()
		return false
	}
	r.busy[symbol] = true
	r.mu.Unlock()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() {
			r.mu.Lock()
			delete(r.busy, symbol)
			r.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		for _, step := range ingestSteps(symbol, assetType) {
			started := time.Now()
			if err := r.exec(ctx, step[0], step[1:]...); err != nil {
				slog.Error("backfill instrumen gagal", "symbol", symbol, "langkah", strings.Join(step, " "), "err", err)
				if r.store != nil {
					if lerr := r.store.LogJob(ctx, jobOf(step), symbol, started, "fail", truncate(err.Error(), 500)); lerr != nil {
						slog.Error("gagal menulis job_runs", "err", lerr)
					}
				}
			}
		}
		slog.Info("backfill instrumen selesai", "symbol", symbol)
	}()
	return true
}

func ingestPaths() (python, dir string, err error) {
	root, err := filepath.Abs(env("TA_ROOT", ".."))
	if err != nil {
		return "", "", err
	}
	dir = filepath.Join(root, "ingestion")
	if _, err := os.Stat(filepath.Join(dir, "ingest.py")); err != nil {
		return "", "", fmt.Errorf("ingestion/ingest.py tidak ditemukan di %s (set TA_ROOT)", root)
	}
	if p := env("INGEST_PYTHON", ""); p != "" {
		return p, dir, nil
	}
	for _, p := range []string{filepath.Join(root, ".venv", "bin", "python"), filepath.Join(root, ".venv", "Scripts", "python.exe")} {
		if _, err := os.Stat(p); err == nil {
			return p, dir, nil
		}
	}
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, dir, nil
		}
	}
	return "", "", errors.New("python tidak ditemukan (set INGEST_PYTHON)")
}

func runPython(ctx context.Context, script string, args ...string) error {
	python, dir, err := ingestPaths()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, python, append([]string{filepath.Join(dir, script)}, args...)...)
	cmd.Env = append(os.Environ(), "PYTHONWARNINGS=ignore", "PYTHONIOENCODING=utf-8")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	text := strings.TrimSpace(out.String())
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", script, strings.Join(args, " "), err, tailText(text, 400))
	}
	slog.Info("ingestion", "script", script, "args", strings.Join(args, " "), "output", tailText(text, 300))
	return nil
}

func tailText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

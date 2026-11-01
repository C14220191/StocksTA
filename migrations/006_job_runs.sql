-- 006: riwayat eksekusi job terjadwal (ingestion, backtest) untuk halaman admin Cache & Job dan
-- API Health. Satu baris per job per target (instrument), termasuk yang gagal.
-- Idempoten: .\ta-up.ps1 migrate 006_job_runs.sql

CREATE TABLE IF NOT EXISTS job_runs (
    id          bigserial PRIMARY KEY,
    job         text NOT NULL,          -- ingest_1h | ingest_1d | backtest
    target      text,                   -- symbol untuk ingest, NULL untuk backtest
    started_at  timestamptz NOT NULL,
    finished_at timestamptz NOT NULL DEFAULT now(),
    status      text NOT NULL CHECK (status IN ('ok', 'fail')),
    rows        integer NOT NULL DEFAULT 0,
    message     text
);
CREATE INDEX IF NOT EXISTS idx_job_runs_job ON job_runs (job, finished_at DESC);

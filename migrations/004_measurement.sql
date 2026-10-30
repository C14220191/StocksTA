-- 004: kolom pengukuran tambahan di inference_log (freshness berbasis harga, dekomposisi latency,
-- sumber request) dan status ERROR. Idempoten: aman dijalankan ulang di database yang sudah ada
-- (.\ta-up.ps1 migrate 004_measurement.sql).

ALTER TABLE inference_log
    -- ui = dibuka dari browser, k6 = load test, scheduled = pre-warm, manual = tes tangan (header X-TA-Trigger)
    ADD COLUMN IF NOT EXISTS trigger             text NOT NULL DEFAULT 'ui',
    ADD COLUMN IF NOT EXISTS prompt_version      text,
    ADD COLUMN IF NOT EXISTS summary_id          bigint REFERENCES ai_summaries(id) ON DELETE SET NULL,
    -- Freshness berbasis harga: harga yang dilihat AI vs harga saat request dilayani
    ADD COLUMN IF NOT EXISTS price_at_generation numeric(24,8),
    ADD COLUMN IF NOT EXISTS price_at_request    numeric(24,8),
    ADD COLUMN IF NOT EXISTS price_source        text,  -- live = price feed, db = close 1h terakhir
    ADD COLUMN IF NOT EXISTS price_deviation_pct numeric(10,4),  -- (request / generation - 1) * 100, bertanda
    -- Freshness berbasis waktu: data_age_seconds = umur data saat summary dibuat + cache_age_seconds
    ADD COLUMN IF NOT EXISTS cache_age_seconds   integer,  -- now - generated_at (0 saat MISS)
    -- Dekomposisi latency saat MISS (dari AI service)
    ADD COLUMN IF NOT EXISTS ai_queue_ms         integer,  -- menunggu giliran proses Hermes
    ADD COLUMN IF NOT EXISTS ai_exec_ms          integer,  -- proses Hermes berjalan
    ADD COLUMN IF NOT EXISTS ai_format_ok        boolean,  -- output AI berupa JSON valid
    ADD COLUMN IF NOT EXISTS error               text;

ALTER TABLE inference_log DROP CONSTRAINT IF EXISTS inference_log_trigger_check;
ALTER TABLE inference_log ADD CONSTRAINT inference_log_trigger_check
    CHECK (trigger IN ('ui', 'k6', 'scheduled', 'manual'));

ALTER TABLE inference_log DROP CONSTRAINT IF EXISTS inference_log_price_source_check;
ALTER TABLE inference_log ADD CONSTRAINT inference_log_price_source_check
    CHECK (price_source IN ('live', 'db'));

-- ERROR = request gagal (AI timeout/error). Saring dengan cache_status <> 'ERROR' untuk metrik latency.
ALTER TABLE inference_log DROP CONSTRAINT IF EXISTS inference_log_cache_status_check;
ALTER TABLE inference_log ADD CONSTRAINT inference_log_cache_status_check
    CHECK (cache_status IN ('HIT', 'MISS', 'BYPASS', 'ERROR'));

CREATE INDEX IF NOT EXISTS idx_inflog_trigger ON inference_log (trigger, experiment_condition, created_at);

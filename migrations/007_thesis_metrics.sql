-- 007: kolom untuk metrik skripsi Bab 2.7 / 3.3 yang belum tercatat:
--   - pre-generation (Lapis 1) dan wasted inference ratio W
--   - vektor kondisi indikator S (SFR, pemeriksaan ketiga hybrid invalidation)
--   - invalidation latency L_inv = t_invalidated - t_crossing (watcher proaktif)
--   - kondisi eksperimen pada cache_events, karena beberapa instance berjalan paralel
-- Idempoten: .\ta-up.ps1 migrate 007_thesis_metrics.sql

-- inference_log ---------------------------------------------------------------
ALTER TABLE inference_log
    ADD COLUMN IF NOT EXISTS pregen              boolean NOT NULL DEFAULT false, -- instance menjalankan pre-generation
    ADD COLUMN IF NOT EXISTS state_at_generation text,   -- S saat ringkasan dibuat
    ADD COLUMN IF NOT EXISTS state_at_request    text,   -- S saat ringkasan disajikan
    ADD COLUMN IF NOT EXISTS state_changed       boolean;

-- PREGEN = generate terjadwal (bukan request pengguna). Disaring dari metrik latency dan hit rate.
ALTER TABLE inference_log DROP CONSTRAINT IF EXISTS inference_log_cache_status_check;
ALTER TABLE inference_log ADD CONSTRAINT inference_log_cache_status_check
    CHECK (cache_status IN ('HIT', 'MISS', 'BYPASS', 'ERROR', 'PREGEN'));

-- pilot = studi pendahuluan (Bab 3.4)
ALTER TABLE inference_log DROP CONSTRAINT IF EXISTS inference_log_trigger_check;
ALTER TABLE inference_log ADD CONSTRAINT inference_log_trigger_check
    CHECK (trigger IN ('ui', 'k6', 'scheduled', 'manual', 'pilot'));

CREATE INDEX IF NOT EXISTS idx_inflog_summary ON inference_log (summary_id);

-- ai_summaries ----------------------------------------------------------------
ALTER TABLE ai_summaries
    ADD COLUMN IF NOT EXISTS origin              text NOT NULL DEFAULT 'ondemand',
    ADD COLUMN IF NOT EXISTS state_at_generation text,
    ADD COLUMN IF NOT EXISTS indicators          jsonb;   -- nilai indikator yang dikirim ke prompt

ALTER TABLE ai_summaries DROP CONSTRAINT IF EXISTS ai_summaries_origin_check;
ALTER TABLE ai_summaries ADD CONSTRAINT ai_summaries_origin_check
    CHECK (origin IN ('ondemand', 'pregen'));

-- cache_events ----------------------------------------------------------------
ALTER TABLE cache_events
    ADD COLUMN IF NOT EXISTS experiment_condition       text,
    ADD COLUMN IF NOT EXISTS invalidation_threshold_pct numeric(5,2),
    ADD COLUMN IF NOT EXISTS pregen                     boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS summary_id                 bigint REFERENCES ai_summaries(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS detected_by                text,         -- watcher | request | admin
    ADD COLUMN IF NOT EXISTS crossed_at                 timestamptz,  -- t_crossing (waktu tick yang melewati ambang)
    ADD COLUMN IF NOT EXISTS invalidation_latency_ms    numeric(12,3),-- L_inv = created_at - crossed_at
    ADD COLUMN IF NOT EXISTS state_from                 text,
    ADD COLUMN IF NOT EXISTS state_to                   text;

ALTER TABLE cache_events DROP CONSTRAINT IF EXISTS cache_events_reason_check;
ALTER TABLE cache_events ADD CONSTRAINT cache_events_reason_check
    CHECK (reason IN ('ttl_expired', 'price_move', 'state_change', 'manual'));

ALTER TABLE cache_events DROP CONSTRAINT IF EXISTS cache_events_detected_by_check;
ALTER TABLE cache_events ADD CONSTRAINT cache_events_detected_by_check
    CHECK (detected_by IN ('watcher', 'request', 'admin'));

CREATE INDEX IF NOT EXISTS idx_cache_events_condition ON cache_events (experiment_condition, created_at);

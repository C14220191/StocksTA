-- 009: tiga metode analisis AI (Hermes skills di .hermes/skills/trading-analysis) dan data
-- fundamental saham. Satu instrument kini punya tiga analisis terpisah, masing-masing di-cache,
-- diinvalidasi, dan diukur sendiri:
--   debate   = Multi-Agent Debate (Bull vs Bear)            skill ta-bull-bear-debate
--   mtf      = Structured Multi-Timeframe Technical Analysis skill ta-multi-timeframe
--   grounded = Grounded Research Brief + Devil's Advocate   skill ta-grounded-brief
--   summary  = ringkasan tunggal hermes-v4 dan sebelumnya (data lama)
-- Idempoten: .\ta-up.ps1 migrate 009_methods.sql  /  ./ta-up.sh migrate 009_methods.sql

-- ai_summaries ----------------------------------------------------------------
ALTER TABLE ai_summaries
    ADD COLUMN IF NOT EXISTS method     text NOT NULL DEFAULT 'summary',
    ADD COLUMN IF NOT EXISTS confidence smallint,   -- 0-100 dari AI
    ADD COLUMN IF NOT EXISTS detail     jsonb;      -- isi terstruktur per metode (analis, debat, timeframe, ...)

ALTER TABLE ai_summaries DROP CONSTRAINT IF EXISTS ai_summaries_method_check;
ALTER TABLE ai_summaries ADD CONSTRAINT ai_summaries_method_check
    CHECK (method IN ('summary', 'debate', 'mtf', 'grounded'));
ALTER TABLE ai_summaries DROP CONSTRAINT IF EXISTS ai_summaries_confidence_check;
ALTER TABLE ai_summaries ADD CONSTRAINT ai_summaries_confidence_check
    CHECK (confidence BETWEEN 0 AND 100);

CREATE INDEX IF NOT EXISTS idx_ai_summaries_inst_method ON ai_summaries (instrument_id, method, generated_at DESC);

-- inference_log dan cache_events: metode ikut dicatat supaya metrik Bab 4 bisa dihitung per metode
ALTER TABLE inference_log ADD COLUMN IF NOT EXISTS method text NOT NULL DEFAULT 'summary';
ALTER TABLE cache_events  ADD COLUMN IF NOT EXISTS method text;

-- Perbaikan: pre-generation mencatat invalidation dengan detected_by = 'scheduler'
ALTER TABLE cache_events DROP CONSTRAINT IF EXISTS cache_events_detected_by_check;
ALTER TABLE cache_events ADD CONSTRAINT cache_events_detected_by_check
    CHECK (detected_by IN ('watcher', 'request', 'scheduler', 'admin'));

-- fundamentals: snapshot laporan keuangan saham dari Yahoo Finance (ingestion/fundamentals.py,
-- harian). Satu baris per instrument, ditimpa setiap pengambilan. Kripto dan komoditas tidak punya.
CREATE TABLE IF NOT EXISTS fundamentals (
    instrument_id integer PRIMARY KEY REFERENCES instruments(id) ON DELETE CASCADE,
    fetched_at    timestamptz NOT NULL DEFAULT now(),
    source        text NOT NULL DEFAULT 'yahoo',
    data          jsonb NOT NULL  -- {currency, sector, industry, business_summary, valuation{...}, annual[...]}
);

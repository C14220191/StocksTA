-- Skema Tugas Akhir (migrasi 001 dan 002 digabung), disusun ulang dari struktur tabel di VM.
-- Pakai file ini HANYA kalau 001_init.sql dan 002_cache_read_tokens.sql asli tidak bisa diambil.
-- Jangan taruh keduanya bersamaan di folder migrations.

CREATE TABLE instruments (
    id         serial PRIMARY KEY,
    symbol     text NOT NULL UNIQUE,
    name       text,
    asset_type text NOT NULL CHECK (asset_type IN ('stock', 'crypto')),
    source     text NOT NULL CHECK (source IN ('yahoo', 'binance'))
);

-- Urutan insert menentukan id: 1 BBCA.JK, 2 BBRI.JK, 3 TLKM.JK, 4 BTCUSDT, 5 ETHUSDT
INSERT INTO instruments (symbol, name, asset_type, source) VALUES
    ('BBCA.JK', 'Bank Central Asia', 'stock',  'yahoo'),
    ('BBRI.JK', 'Bank Rakyat Indonesia', 'stock', 'yahoo'),
    ('TLKM.JK', 'Telkom Indonesia', 'stock', 'yahoo'),
    ('BTCUSDT', 'Bitcoin', 'crypto', 'binance'),
    ('ETHUSDT', 'Ethereum', 'crypto', 'binance');

-- ts = WAKTU BUKA candle
CREATE TABLE ohlcv (
    instrument_id integer NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    timeframe     text NOT NULL,
    ts            timestamptz NOT NULL,
    open          numeric(24,8) NOT NULL,
    high          numeric(24,8) NOT NULL,
    low           numeric(24,8) NOT NULL,
    close         numeric(24,8) NOT NULL,
    volume        numeric(30,8) NOT NULL DEFAULT 0,
    fetched_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (instrument_id, timeframe, ts)
);
CREATE INDEX idx_ohlcv_latest ON ohlcv (instrument_id, timeframe, ts DESC);

CREATE TABLE ai_summaries (
    id                  bigserial PRIMARY KEY,
    instrument_id       integer NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    generated_at        timestamptz NOT NULL DEFAULT now(),
    source_data_ts      timestamptz NOT NULL,
    prompt_version      text NOT NULL,
    model               text NOT NULL,
    content             text NOT NULL,
    signal              text CHECK (signal IN ('BUY', 'SELL', 'HOLD')),
    price_at_generation numeric(24,8),
    price_after_24h     numeric(24,8),
    signal_correct      boolean
);
CREATE INDEX idx_summaries_instrument ON ai_summaries (instrument_id, generated_at DESC);

CREATE TABLE cache_events (
    id               bigserial PRIMARY KEY,
    created_at       timestamptz NOT NULL DEFAULT now(),
    instrument_id    integer NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    reason           text NOT NULL CHECK (reason IN ('ttl_expired', 'price_move', 'manual')),
    price_change_pct numeric(8,4)
);
CREATE INDEX idx_cache_events_instrument ON cache_events (instrument_id, created_at DESC);

CREATE TABLE inference_log (
    id                         bigserial PRIMARY KEY,
    created_at                 timestamptz NOT NULL DEFAULT now(),
    instrument_id              integer REFERENCES instruments(id) ON DELETE SET NULL,
    experiment_condition       text NOT NULL CHECK (experiment_condition IN
                                   ('no_cache', 'static_ttl', 'dynamic_ttl', 'event_only', 'hybrid')),
    invalidation_threshold_pct numeric(5,2),
    cache_status               text NOT NULL CHECK (cache_status IN ('HIT', 'MISS', 'BYPASS')),
    latency_ms                 integer NOT NULL,
    model                      text,
    tokens_in                  integer NOT NULL DEFAULT 0,
    tokens_out                 integer NOT NULL DEFAULT 0,
    cost_usd                   numeric(12,6) NOT NULL DEFAULT 0,
    data_age_seconds           integer,
    ttl_applied_seconds        integer,
    realized_volatility        numeric(12,8),
    cache_read_tokens          integer NOT NULL DEFAULT 0
);
CREATE INDEX idx_inflog_condition ON inference_log (experiment_condition, created_at);

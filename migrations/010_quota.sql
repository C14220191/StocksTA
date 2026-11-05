-- 010: snapshot kuota langganan AI (mis. Codex lewat ChatGPT: jendela 5 jam "Session" dan "Weekly").
-- Dengan langganan, biaya per token = 0, jadi biaya komputasi diukur sebagai konsumsi kuota:
-- persen kuota yang terpakai selama eksperimen, dibagi ke kondisi menurut token yang di-generate
-- (lihat /api/admin/evaluation -> quota). Diisi ingestion/quota.py (`hermes usage --json`), tiap 10 menit.
-- Idempoten: .\ta-up.ps1 migrate 010_quota.sql  /  ./ta-up.sh migrate 010_quota.sql

CREATE TABLE IF NOT EXISTS quota_snapshots (
    id           bigserial PRIMARY KEY,
    fetched_at   timestamptz NOT NULL DEFAULT now(),
    provider     text NOT NULL,   -- openai-codex | anthropic | nous | ...
    plan         text,
    window_label text NOT NULL,   -- Session (5 jam) | Weekly | ...
    used_percent numeric(7,3),    -- 0-100
    resets_at    timestamptz
);
CREATE INDEX IF NOT EXISTS idx_quota_snapshots ON quota_snapshots (provider, window_label, fetched_at);

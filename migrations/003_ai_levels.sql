-- 003: support dan resistance hasil AI, disimpan terpisah dari teks ringkasan
-- supaya bisa diambil UI tanpa parsing ulang. NULL = AI tidak memberi angka yang valid.
ALTER TABLE ai_summaries ADD COLUMN IF NOT EXISTS support    numeric(24,8);
ALTER TABLE ai_summaries ADD COLUMN IF NOT EXISTS resistance numeric(24,8);

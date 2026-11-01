-- 005: kategori komoditas + sumber Binance Futures, dan instrument XAUUSDT (emas, USDⓈ-M perpetual).
-- XAUUSDT tidak ada di Binance spot; data diambil dari fapi.binance.com (riwayat mulai 11 Des 2025).
-- Idempoten: .\ta-up.ps1 migrate 005_commodity.sql

ALTER TABLE instruments DROP CONSTRAINT IF EXISTS instruments_asset_type_check;
ALTER TABLE instruments ADD CONSTRAINT instruments_asset_type_check
    CHECK (asset_type IN ('stock', 'crypto', 'commodity'));

ALTER TABLE instruments DROP CONSTRAINT IF EXISTS instruments_source_check;
ALTER TABLE instruments ADD CONSTRAINT instruments_source_check
    CHECK (source IN ('yahoo', 'binance', 'binance_futures'));

INSERT INTO instruments (symbol, name, asset_type, source)
VALUES ('XAUUSDT', 'Emas (Gold)', 'commodity', 'binance_futures')
ON CONFLICT (symbol) DO NOTHING;

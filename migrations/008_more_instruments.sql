-- 008: perluasan instrument (1 Nov 2026). Semua simbol sudah diverifikasi punya data:
--   saham  : Yahoo Finance (1h 5 hari + 1d 1 tahun), blue chip LQ45 lintas sektor
--   kripto : Binance spot USDT, likuiditas tinggi, status TRADING (TONUSDT tidak dipakai: status BREAK
--            sejak 30 Jun 2026)
--   komoditas: semua COMMODITY TradFi perpetual di Binance USDⓈ-M futures
--             (XAG sejak Jan 2026, XPT/XPD sejak Jan 2026, COPPER sejak Mar 2026,
--              CL/BZ/NATGAS sejak Apr 2026 -> riwayat < 1 tahun; XPT, XPD, COPPER, NATGAS likuiditas rendah)
-- Idempoten: .\ta-up.ps1 migrate 008_more_instruments.sql, lalu .\ta-up.ps1 ingest (backfill otomatis)
-- dan restart backend (price feed + sigma_ref dibaca saat start).

INSERT INTO instruments (symbol, name, asset_type, source) VALUES
    -- Saham IDX
    ('BMRI.JK', 'Bank Mandiri', 'stock', 'yahoo'),
    ('BBNI.JK', 'Bank Negara Indonesia', 'stock', 'yahoo'),
    ('BRIS.JK', 'Bank Syariah Indonesia', 'stock', 'yahoo'),
    ('ASII.JK', 'Astra International', 'stock', 'yahoo'),
    ('UNVR.JK', 'Unilever Indonesia', 'stock', 'yahoo'),
    ('ICBP.JK', 'Indofood CBP Sukses Makmur', 'stock', 'yahoo'),
    ('INDF.JK', 'Indofood Sukses Makmur', 'stock', 'yahoo'),
    ('KLBF.JK', 'Kalbe Farma', 'stock', 'yahoo'),
    ('GOTO.JK', 'GoTo Gojek Tokopedia', 'stock', 'yahoo'),
    ('ANTM.JK', 'Aneka Tambang', 'stock', 'yahoo'),
    ('MDKA.JK', 'Merdeka Copper Gold', 'stock', 'yahoo'),
    ('INCO.JK', 'Vale Indonesia', 'stock', 'yahoo'),
    ('PTBA.JK', 'Bukit Asam', 'stock', 'yahoo'),
    ('ADRO.JK', 'Alamtri Resources Indonesia (Adaro)', 'stock', 'yahoo'),
    ('PGAS.JK', 'Perusahaan Gas Negara', 'stock', 'yahoo'),
    ('MEDC.JK', 'Medco Energi Internasional', 'stock', 'yahoo'),
    ('UNTR.JK', 'United Tractors', 'stock', 'yahoo'),
    ('CPIN.JK', 'Charoen Pokphand Indonesia', 'stock', 'yahoo'),
    ('AMRT.JK', 'Sumber Alfaria Trijaya (Alfamart)', 'stock', 'yahoo'),
    ('TOWR.JK', 'Sarana Menara Nusantara', 'stock', 'yahoo'),
    ('ISAT.JK', 'Indosat Ooredoo Hutchison', 'stock', 'yahoo'),
    ('EXCL.JK', 'XLSmart Telecom Sejahtera', 'stock', 'yahoo'),
    ('SMGR.JK', 'Semen Indonesia', 'stock', 'yahoo'),
    -- Kripto (Binance spot)
    ('BNBUSDT', 'BNB', 'crypto', 'binance'),
    ('SOLUSDT', 'Solana', 'crypto', 'binance'),
    ('XRPUSDT', 'XRP', 'crypto', 'binance'),
    ('DOGEUSDT', 'Dogecoin', 'crypto', 'binance'),
    ('ADAUSDT', 'Cardano', 'crypto', 'binance'),
    ('TRXUSDT', 'TRON', 'crypto', 'binance'),
    ('LINKUSDT', 'Chainlink', 'crypto', 'binance'),
    ('AVAXUSDT', 'Avalanche', 'crypto', 'binance'),
    ('LTCUSDT', 'Litecoin', 'crypto', 'binance'),
    ('NEARUSDT', 'NEAR Protocol', 'crypto', 'binance'),
    ('SUIUSDT', 'Sui', 'crypto', 'binance'),
    ('UNIUSDT', 'Uniswap', 'crypto', 'binance'),
    -- Komoditas (Binance USDⓈ-M futures, TradFi perpetual)
    ('XAGUSDT', 'Perak (Silver)', 'commodity', 'binance_futures'),
    ('XPTUSDT', 'Platinum', 'commodity', 'binance_futures'),
    ('XPDUSDT', 'Paladium (Palladium)', 'commodity', 'binance_futures'),
    ('COPPERUSDT', 'Tembaga (Copper)', 'commodity', 'binance_futures'),
    ('CLUSDT', 'Minyak Mentah WTI (Crude Oil)', 'commodity', 'binance_futures'),
    ('BZUSDT', 'Minyak Brent (Brent Oil)', 'commodity', 'binance_futures'),
    ('NATGASUSDT', 'Gas Alam (Natural Gas)', 'commodity', 'binance_futures')
ON CONFLICT (symbol) DO NOTHING;

-- TONUSDT sempat ditambahkan lalu dihapus (perdagangan spot dihentikan); ohlcv ikut terhapus (CASCADE).
DELETE FROM instruments WHERE symbol = 'TONUSDT';

package models

import "time"

// MarketRow: satu baris instrument untuk tabel Home dan Search (tabel instruments + ohlcv + ai_summaries).
type MarketRow struct {
	Symbol        string     `json:"symbol"`         // dipakai untuk URL, mis. "BTCUSDT", "BBCA.JK"
	DisplaySymbol string     `json:"display_symbol"` // untuk tampilan, mis. "BTC/USDT", "BBCA"
	Name          string     `json:"name"`
	AssetType     string     `json:"asset_type"` // stock | crypto | commodity
	Source        string     `json:"source"`     // yahoo | binance | binance_futures (UI memilih stream live dari sini)
	Currency      string     `json:"currency"`   // IDR | USD
	Price         float64    `json:"price"`
	PriceTS       time.Time  `json:"price_ts"`         // waktu buka candle 1h terakhir yang dipakai sebagai harga
	FetchedAt     time.Time  `json:"fetched_at"`       // kapan data harga itu diambil (untuk "diperbarui X menit lalu")
	ChangePct     *float64   `json:"change_pct"`       // terhadap penutupan harian sebelumnya
	Volume24h     *float64   `json:"volume_24h"`       // satuan asli: lembar saham / koin
	Turnover24h   *float64   `json:"turnover_24h"`     // nilai transaksi = sum(volume x close), dalam Currency
	TurnoverUSD   *float64   `json:"turnover_24h_usd"` // dikonversi ke USD (kurs USDIDR_RATE), untuk mengurutkan saham dan kripto bersama
	Signal        *string    `json:"signal"`           // bias gabungan analisis AI terakhir tiap metode: BUY | SELL | HOLD | MIXED (bisa null)
	SignalAt      *time.Time `json:"signal_at"`        // kapan analisis AI terakhir dibuat
}

// DetailStats: kartu statistik di Detail Page. Dihitung dari candle HARIAN yang sudah tutup.
type DetailStats struct {
	RSI14      *float64 `json:"rsi14"`
	Support    *float64 `json:"support"`    // low terendah 20 hari terakhir
	Resistance *float64 `json:"resistance"` // high tertinggi 20 hari terakhir
	Volume24h  *float64 `json:"volume_24h"`
	Basis      string   `json:"basis"`
}

// InstrumentCandidate: calon baris tabel instruments, hasil pengecekan simbol ke sumber datanya.
type InstrumentCandidate struct {
	ID        int      `json:"id,omitempty"`
	Symbol    string   `json:"symbol"`
	Name      string   `json:"name"`
	AssetType string   `json:"asset_type"`
	Source    string   `json:"source"`
	Currency  string   `json:"currency"`
	Exchange  string   `json:"exchange"`
	Price     *float64 `json:"price"`  // harga terakhir di sumber, sebagai bukti data mengalir
	Exists    bool     `json:"exists"` // sudah ada di tabel instruments
}

// InstrumentJob: eksekusi terakhir satu job ingestion untuk satu instrumen (tabel job_runs).
type InstrumentJob struct {
	Status  string    `json:"status"` // ok | fail
	At      time.Time `json:"at"`
	Rows    int       `json:"rows"`
	Message *string   `json:"message"`
}

// AdminInstrument: satu instrumen di halaman admin (instruments + ringkasan ohlcv + job_runs).
type AdminInstrument struct {
	ID            int                       `json:"id"`
	Symbol        string                    `json:"symbol"`
	DisplaySymbol string                    `json:"display_symbol"`
	Name          string                    `json:"name"`
	AssetType     string                    `json:"asset_type"`
	Source        string                    `json:"source"`
	Candles1h     int                       `json:"candles_1h"`
	Last1h        *time.Time                `json:"last_1h"`
	Candles1d     int                       `json:"candles_1d"`
	Last1d        *time.Time                `json:"last_1d"`
	Jobs          map[string]*InstrumentJob `json:"jobs"` // ingest_1h | ingest_1d | fundamentals: eksekusi terakhir
	Ingesting     bool                      `json:"ingesting"`
	LivePrice     *bool                     `json:"live_price"` // nil = tidak relevan (saham tanpa feed live)
	SigmaRef      *bool                     `json:"sigma_ref"`  // nil = kondisi ini tidak memakai TTL dinamis
}

// JobRun: satu baris tabel job_runs.
type JobRun struct {
	Job        string    `json:"job"`
	Target     *string   `json:"target"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Status     string    `json:"status"`
	Rows       int       `json:"rows"`
	Message    *string   `json:"message"`
}

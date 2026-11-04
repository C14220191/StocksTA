# API untuk UI (Home, Search, Detail)

Base URL: `http://127.0.0.1:8080`. Semua endpoint `GET`, JSON.

| Halaman | Endpoint | Memanggil AI? | Dicatat di `inference_log`? |
|---|---|---|---|
| Home | `/api/market/overview?type=all\|stock\|crypto\|commodity&limit=10&movers=3` | Tidak | Tidak |
| Search | `/api/search?q=<teks>&type=all\|stock\|crypto\|commodity` | Tidak | Tidak |
| Detail (header + kartu statistik) | `/api/instruments/:symbol` | Tidak | Tidak |
| Detail (grafik + indikator) | `/api/instruments/:symbol/candles?range=1D\|1W\|1M\|3M\|6M\|1Y\|ALL&tf=1h\|2h\|4h\|6h\|1d` | Tidak | Tidak |
| Detail ("Analisis AI", 3 kartu) | `/api/analyze/:symbol?method=debate\|mtf\|grounded` (satu panggilan per metode) | Ya, kalau MISS | **Ya** |

`type` juga menerima `semua`, `saham`, `kripto`, `komoditas`. `:symbol` memakai `symbol` dari respons (mis. `BTCUSDT`, `BBCA.JK`), bukan `display_symbol`.

## Satu baris instrument (dipakai Home dan Search)

```json
{
  "symbol": "BTCUSDT",
  "display_symbol": "BTC/USDT",
  "name": "Bitcoin",
  "asset_type": "crypto",
  "source": "binance",
  "currency": "USD",
  "price": 82267.17,
  "price_ts": "2026-10-29T09:00:00Z",
  "fetched_at": "2026-10-29T09:10:27Z",
  "change_pct": 2.69,
  "volume_24h": 1278,
  "turnover_24h": 105165354.95,
  "turnover_24h_usd": 105165354.95,
  "signal": "BUY",
  "signal_at": "2026-10-29T09:10:27Z"
}
```

| Kolom mockup | Field |
|---|---|
| Instrumen (judul / subjudul) | `display_symbol` / `name` |
| Kategori | `asset_type`: `stock` / `crypto` / `commodity` |
| Sumber data live | `source`: `binance` (spot WebSocket), `binance_futures` (futures WebSocket, mis. XAUUSDT), `yahoo` (tidak live) |
| Harga | `price` + `currency` (format dengan `formatPrice`) |
| Perubahan | `change_pct` (terhadap penutupan harian sebelumnya; `null` kalau belum ada) |
| Volume | `volume_24h` (lembar / koin) atau `turnover_24h` (nilai transaksi) |
| Bias AI | `signal` = bias gabungan analisis valid terakhir tiap metode: `BUY`/`SELL`/`HOLD` (mayoritas) atau `MIXED` (berbeda). Tampilkan sebagai Bullish / Bearish / Netral / Beragam, bukan Buy/Sell. `null` = belum pernah dianalisis, tampilkan "-" |
| "Diperbarui X menit lalu" (opsional) | `fetched_at` |

## Home: `/api/market/overview`

```json
{ "type": "all", "generated_at": "...", "top_volume": [ ... ], "top_gainers": [ ... ], "top_losers": [ ... ] }
```

- `top_volume` diurutkan berdasarkan `turnover_24h_usd` (nilai transaksi dalam USD, kurs `USDIDR_RATE` di `run.env`, default 16000), supaya saham dan kripto bisa dibandingkan di tab Semua.
- `top_gainers` hanya `change_pct > 0`, `top_losers` hanya `< 0`. Bisa kosong.
- Di mockup, judul kartu kedua tertulis "Penguatan Terbesar"; untuk `top_losers` seharusnya "Pelemahan Terbesar".

## Search: `/api/search`

```json
{ "query": "bank", "count": 2, "results": [ ... ] }
```

Cocok ke `symbol` atau `name` (tidak peka huruf besar). `/` dan spasi diabaikan, jadi "BTC/USDT" menemukan `BTCUSDT`. "Pencarian terakhir" disimpan di sisi UI (`localStorage`).

## Detail: `/api/instruments/:symbol`

```json
{
  "instrument": { ...satu baris instrument... },
  "stats": {
    "rsi14": 50.8, "support": 76055.9, "resistance": 84043.6, "volume_24h": 1278,
    "basis": "candle harian yang sudah tutup; support/resistance = low/high 20 hari"
  },
  "methods": ["debate", "mtf", "grounded"],
  "ai_latest": {
    "debate": {
      "method": "debate", "signal": "HOLD", "confidence": 55, "summary": "...", "detail": { ... },
      "model": "upstage/solar-mini4:free", "generated_at": "...", "source_data_ts": "...",
      "price_at_generation": 78140, "support": 77035, "resistance": 80133
    },
    "mtf": { ... }, "grounded": { ... }
  }
}
```

`ai_latest` = analisis valid (JSON terbaca) terakhir per metode, tanpa memanggil AI. Metode yang belum pernah dianalisis tidak ada di objek. Isi `detail` per metode lihat bagian Analisis AI di bawah.

Ada dua pasang support/resistance:

| Field | Sumber | Catatan |
|---|---|---|
| `stats.support` / `stats.resistance` | dihitung backend | low/high 20 candle harian, selalu ada |
| `ai_latest.<metode>.support` / `.resistance` | dari AI (Hermes) | bisa `null` kalau AI tidak memberi angka valid |

## Grafik: `/api/instruments/:symbol/candles`

`range` menentukan rentang waktu, `tf` menentukan ukuran tiap candle. `tf` boleh dikosongkan (pakai bawaan).

| range | rentang | tf bawaan | jumlah candle bawaan |
|---|---|---|---|
| 1D | 24 jam | 1h | 24 |
| 1W | 7 hari | 1h | 168 |
| 1M | 30 hari | 4h | 180 |
| 3M | 90 hari | 1d | 90 |
| 6M | 182 hari | 1d | 182 |
| 1Y | 365 hari | 1d | 365 |
| ALL | seluruh data di database | 1d | semua (sekitar 365 sekarang) |

- `tf` = `1h`, `2h`, `4h`, `6h`, `1d`. Jumlah candle = rentang / tf (maksimal 10.000; kombinasi dengan kurang dari 6 candle ditolak, mis. 1D + 1d).
- `ALL` mengambil semua candle yang ada, jadi makin panjang seiring ingestion berjalan. `ALL` + `tf=1h` untuk 1 tahun kripto = sekitar 8.760 candle (respons sekitar 3 MB); pakai `1d` atau `4h` untuk tampilan normal.
- `2h`, `4h`, `6h` digabung dari candle 1h saat request (tidak disimpan, tanpa ingestion tambahan). Kripto dihitung dari 00:00 UTC (sama dengan Binance), saham dari 00:00 WIB, jadi candle 4h saham = sesi 09:00-12:00 dan 12:00-16:00.
- Timeframe ini untuk grafik. Analisis AI memakai 14 candle 1h + indikator 1h; metode `mtf` juga menerima 12 candle 1D, 4H, dan 1H tertutup beserta EMA20/50/200 dan RSI14 (4H digabung dengan aturan yang sama).

```json
{
  "symbol": "BTCUSDT", "range": "1W", "timeframe": "4h",
  "candles": [ { "time": 1793246400, "open": 0, "high": 0, "low": 0, "close": 0, "volume": 0, "closed": true } ],
  "indicators": {
    "rsi14": [ ... ],
    "sma20": [ ... ], "sma50": [ ... ],
    "ema20": [ ... ], "ema50": [ ... ],
    "macd": { "line": [ ... ], "signal": [ ... ], "hist": [ ... ] },
    "bollinger": { "upper": [ ... ], "middle": [ ... ], "lower": [ ... ] }
  }
}
```

- `time` dalam detik UTC, langsung cocok untuk TradingView `lightweight-charts`.
- Setiap deret indikator sejajar dengan `candles` (indeks sama). `null` = data belum cukup.
- Parameter indikator: RSI 14, SMA/EMA 20 dan 50, MACD (12, 26, 9), Bollinger (20, 2).
- Candle terakhir bisa `closed: false` (masih berjalan).

## Analisis AI: `/api/analyze/:symbol?method=`

`method` wajib: `debate` (Multi-Agent Debate, Bull vs Bear), `mtf` (Structured Multi-Timeframe Technical Analysis), atau `grounded` (Grounded Research Brief + Devil's Advocate). Setiap metode = Hermes skill di `.hermes/skills/trading-analysis/` dengan entri cache, invalidation, dan baris `inference_log` sendiri. Satu kunjungan Detail = tiga panggilan (UI mengirim bersamaan; AI service mengantre proses Hermes, jadi kartu selesai satu per satu, masing-masing 10 sampai 60 detik kalau MISS).

Respons: `method`, `cache_status`, `origin`, `signal` (`BUY`/`SELL`/`HOLD` = bias bullish/bearish/netral), `confidence` (0-100), `summary`, `detail`, `model`, `support`, `resistance`, `latency_ms`, `tokens_in`, `data_age_seconds`, dan lainnya. `format_ok: false` (hanya saat AI dipanggil) = output AI bukan JSON: `detail` `null`, `summary` berisi teks mentah, dan hasilnya **tidak di-cache**.

Analisis bersifat deskriptif; UI wajib menampilkan pemberitahuan bahwa analisis dibuat AI dan bukan ajakan berinvestasi. Skill melarang entry, stop loss, take profit, risk/reward, dan ukuran posisi; kalau istilah itu tetap muncul, `detail.advice_terms` berisi daftarnya.

`detail` per metode (sudah dinormalisasi AI service):

| Metode | Kunci `detail` |
|---|---|
| `debate` | `analysts[]` (`role` fundamental/technical/sentiment, `signal` bullish/bearish/neutral, `confidence`, `reason`), `debate[]` (`round`, `bull`, `bear`), `risk` (`worst_case`, `volatility`), `change_conditions[]`, `missing_data[]` |
| `mtf` | `timeframes[]` (`tf` 1D/4H/1H, `trend` naik/turun/sideways, `structure`, `aligned`, `note`), `confluence` (`score` 0-5 = jumlah faktor true, `trend`, `structure`, `level`, `rsi`, `volume`), `setup` (`jelas` hanya kalau score >= 4, selain itu `tidak_jelas`), `scenarios` (`bullish`, `bearish`, `invalidation`), `invalidation_level` |
| `grounded` | `thesis` (pandangan dominan yang diuji, dibuat otomatis dari indikator), `brief`, `metrics[]` (`name`, `value`, `trend`, `source` mis. `[D3]`), `red_flags[]`, `weaknesses[]` (5, paling fatal dulu), `falsifiers[]`, `thesis_holds` |

Semua metode juga membawa `detail.bias`. `detail.json_repaired: true` = JSON dari model rusak tapi berhasil diperbaiki parser.

Field freshness (semua opsional untuk UI):

| Field | Arti |
|---|---|
| `generated_at` | Kapan summary dibuat AI |
| `cache_age_seconds` | Lama summary tinggal di cache (`0` saat MISS/BYPASS) |
| `price_at_generation` | Harga yang dilihat AI saat summary dibuat |
| `price_at_request` | Harga saat request ini dilayani (live untuk kripto, close 1h terakhir untuk saham) |
| `price_deviation_pct` | `(price_at_request / price_at_generation - 1) * 100`, bisa negatif; `null` kalau harga tidak diketahui |

Header opsional `X-TA-Trigger: ui | k6 | manual | pilot` (default `ui`) menandai sumber request di `inference_log`. UI tidak perlu mengirimnya; k6 wajib mengirim `k6`.

## Error

`404 {"error":"instrument tidak ditemukan"}`, `400` untuk `type`/`range`/`method` tidak valid, `422` kalau data harga belum ada.

## CORS

Origin yang diizinkan diatur `CORS_ORIGINS` di `run.env` (default `http://localhost:5173,http://127.0.0.1:5173,http://localhost:3000`).

## Admin (`/admin` di UI)

Tanpa autentikasi: backend hanya listen di `127.0.0.1`, akses dari mesin yang sama atau lewat SSH tunnel.

| Halaman | Endpoint | Isi |
|---|---|---|
| Dashboard | `GET /api/admin/overview?period=24h\|7d\|30d&trigger=all\|ui\|k6\|scheduled\|manual` | Biaya, token, penghematan cache (estimasi HIT × rata-rata token per panggilan AI), latensi p50/p95, kunjungan, request per jam/hari (WIB), instrumen teratas |
| Prompt | `GET /api/admin/prompt?symbol=BTCUSDT&method=debate\|mtf\|grounded` | Prompt data satu metode dirender dengan data terkini lewat `POST /prompt/preview` di AI service (tanpa memanggil model) + isi skill Hermes-nya, riwayat versi prompt per metode + akurasi backtest |
| Cache & Job | `GET /api/admin/cache` | Status cache per instrumen (Redis + sisa TTL + deviasi harga), invalidasi terakhir, job terjadwal dari `job_runs`, antrean Hermes |
| Cache & Job | `POST /api/admin/cache/:symbol/invalidate?method=` | Buang cache satu instrumen (satu metode, atau semua kalau `method` kosong), dicatat di `cache_events` dengan reason `manual` |
| Evaluasi | `GET /api/admin/evaluation?days=30&trigger=all\|k6\|ui\|pilot\|manual&prompt=current\|stub\|all&method=all\|debate\|mtf\|grounded` | Metrik Bab 2.7 per kondisi × θ × pregen: H, latency p50/p95/p99, N_gen, N_pre, S, W, D_serve, SSR (θ_s 1/2/5/10), SFR, L_inv + akurasi backtest |
| API Health | `GET /api/admin/health` | PostgreSQL, Redis, AI service, price feed spot/futures, ingestion per sumber, log kesalahan 7 hari |

`job_runs` (migrasi 006) diisi oleh `ingest.py` dan `backtest.py`, satu baris per job per instrumen.

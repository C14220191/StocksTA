# API untuk UI (Home, Search, Detail)

Base URL: `http://127.0.0.1:8080`. Semua endpoint `GET`, JSON.

| Halaman | Endpoint | Memanggil AI? | Dicatat di `inference_log`? |
|---|---|---|---|
| Home | `/api/market/overview?type=all\|stock\|crypto&limit=10&movers=3` | Tidak | Tidak |
| Search | `/api/search?q=<teks>&type=all\|stock\|crypto` | Tidak | Tidak |
| Detail (header + kartu statistik) | `/api/instruments/:symbol` | Tidak | Tidak |
| Detail (grafik + indikator) | `/api/instruments/:symbol/candles?range=1D\|1W\|1M\|3M\|6M\|1Y\|ALL&tf=1h\|2h\|4h\|6h\|1d` | Tidak | Tidak |
| Detail ("Ringkasan analisis AI") | `/api/analyze/:symbol` | Ya, kalau MISS | **Ya** |

`type` juga menerima `semua`, `saham`, `kripto`. `:symbol` memakai `symbol` dari respons (mis. `BTCUSDT`, `BBCA.JK`), bukan `display_symbol`.

## Satu baris instrument (dipakai Home dan Search)

```json
{
  "symbol": "BTCUSDT",
  "display_symbol": "BTC/USDT",
  "name": "Bitcoin",
  "asset_type": "crypto",
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
| Harga | `price` + `currency` (format dengan `formatPrice`) |
| Perubahan | `change_pct` (terhadap penutupan harian sebelumnya; `null` kalau belum ada) |
| Volume | `volume_24h` (lembar / koin) atau `turnover_24h` (nilai transaksi) |
| Buy / Hold / Sell | `signal` dari ringkasan AI terakhir yang tersimpan; `null` = belum pernah dianalisis, tampilkan "-" |
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
  "ai_latest": {
    "signal": "BUY", "summary": "...", "model": "upstage/solar-mini4:free",
    "generated_at": "...", "source_data_ts": "...",
    "support": 79500, "resistance": 81200
  }
}
```

`ai_latest` adalah ringkasan terakhir yang tersimpan, tanpa memanggil AI (`null` kalau belum ada).

Ada dua pasang support/resistance:

| Field | Sumber | Catatan |
|---|---|---|
| `stats.support` / `stats.resistance` | dihitung backend | low/high 20 candle harian, selalu ada |
| `ai_latest.support` / `ai_latest.resistance` | dari AI (Hermes) | bisa `null` kalau AI tidak memberi angka valid |

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
- Timeframe ini **hanya untuk grafik**. Analisis AI tetap memakai 14 candle 1h, supaya prompt tidak berubah.

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

## Ringkasan AI: `/api/analyze/:symbol`

Respons berisi `cache_status`, `signal`, `summary`, `support`, `resistance`, `latency_ms`, `tokens_in`, `data_age_seconds`, dan lainnya. `support`/`resistance` dari AI, `null` kalau tidak valid. Bisa makan 5 sampai 15 detik kalau MISS, jadi tampilkan `ai_latest` dulu sambil menunggu, atau loading state.

Field freshness (semua opsional untuk UI):

| Field | Arti |
|---|---|
| `generated_at` | Kapan summary dibuat AI |
| `cache_age_seconds` | Lama summary tinggal di cache (`0` saat MISS/BYPASS) |
| `price_at_generation` | Harga yang dilihat AI saat summary dibuat |
| `price_at_request` | Harga saat request ini dilayani (live untuk kripto, close 1h terakhir untuk saham) |
| `price_deviation_pct` | `(price_at_request / price_at_generation - 1) * 100`, bisa negatif; `null` kalau harga tidak diketahui |

Header opsional `X-TA-Trigger: ui | k6 | scheduled | manual` (default `ui`) menandai sumber request di `inference_log`. UI tidak perlu mengirimnya; k6 wajib mengirim `k6`.

## Error

`404 {"error":"instrument tidak ditemukan"}`, `400` untuk `type`/`range` tidak valid, `422` kalau data harga belum ada.

## CORS

Origin yang diizinkan diatur `CORS_ORIGINS` di `run.env` (default `http://localhost:5173,http://127.0.0.1:5173,http://localhost:3000`).

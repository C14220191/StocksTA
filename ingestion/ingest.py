"""Ingestion OHLCV ke PostgreSQL (tabel ohlcv).

Pemakaian:
    python ingestion/ingest.py --timeframe 1h              # update rutin incremental (tiap jam, menit ke-5)
    python ingestion/ingest.py --timeframe 1d
    python ingestion/ingest.py --timeframe 1h --backfill   # isi ulang sekitar 1 tahun (jalankan sekali)
    python ingestion/ingest.py --timeframe 1d --backfill

Instrument yang diambil = isi tabel instruments (kolom source: yahoo | binance | binance_futures),
jadi menambah instrument cukup lewat migrasi/INSERT, tanpa mengubah file ini.

Perlu environment: DATABASE_URL, dan opsional BINANCE_URL, BINANCE_FUTURES_URL.
    pip install yfinance psycopg2-binary requests
"""
import argparse
import os
import sys
from datetime import datetime, timedelta, timezone

import psycopg2
import requests
import yfinance as yf
from psycopg2.extras import execute_values
from requests.adapters import HTTPAdapter
from urllib3.util.retry import Retry

DATABASE_URL = os.environ.get("DATABASE_URL")
BINANCE_URL = os.environ.get("BINANCE_URL", "https://api.binance.com/api/v3/klines")
# Kalau api.binance.com diblokir (HTTP 451), set BINANCE_URL=https://data-api.binance.vision/api/v3/klines
# USDⓈ-M Futures (mis. XAUUSDT). Tidak punya mirror data-api; kalau diblokir di lokasi server, instrument ini gagal.
BINANCE_FUTURES_URL = os.environ.get("BINANCE_FUTURES_URL", "https://fapi.binance.com/fapi/v1/klines")

SESSION = requests.Session()
SESSION.mount("https://", HTTPAdapter(max_retries=Retry(
    total=3, backoff_factor=1, status_forcelist=[429, 500, 502, 503, 504])))

INTERVAL_MS = {"1h": 3_600_000, "1d": 86_400_000}
YAHOO_PERIOD_BACKFILL = {"1h": "1y", "1d": "1y"}

INTERVAL_SQL = {"1h": "1 hour", "1d": "1 day"}

# fetched_at hanya diperbarui kalau candle sebelumnya masih berjalan atau nilainya berubah.
# Tanpa syarat ini, setiap run menimpa fetched_at candle lama dengan now(), sehingga
# fetched_at tidak lagi berarti "kapan candle ini pertama kali diambil dalam keadaan final".
UPSERT_SQL = """
INSERT INTO ohlcv (instrument_id, timeframe, ts, open, high, low, close, volume, fetched_at)
VALUES %s
ON CONFLICT (instrument_id, timeframe, ts) DO UPDATE SET
    open = EXCLUDED.open,
    high = EXCLUDED.high,
    low = EXCLUDED.low,
    close = EXCLUDED.close,
    volume = EXCLUDED.volume,
    fetched_at = now()
WHERE ohlcv.fetched_at < ohlcv.ts + interval '{interval}'
   OR (ohlcv.open, ohlcv.high, ohlcv.low, ohlcv.close, ohlcv.volume)
      IS DISTINCT FROM (EXCLUDED.open, EXCLUDED.high, EXCLUDED.low, EXCLUDED.close, EXCLUDED.volume);
"""


def _binance_page(url, symbol, timeframe, limit, start_ms=None):
    params = {"symbol": symbol, "interval": timeframe, "limit": limit}
    if start_ms is not None:
        params["startTime"] = start_ms
    r = SESSION.get(url, params=params, timeout=20)
    r.raise_for_status()
    return r.json()


def fetch_binance(symbol, timeframe, since=None, url=BINANCE_URL):
    """Kembalikan list (ts_utc, open, high, low, close, volume). ts = waktu BUKA candle.
    since = ts candle terakhir di DB (ikut diambil ulang karena mungkin masih berjalan);
    None = backfill sekitar 1 tahun."""
    pages = []
    step = INTERVAL_MS[timeframe]
    if since is None:
        since = datetime.now(timezone.utc) - timedelta(days=365)
    start = int(since.timestamp() * 1000)
    now_ms = int(datetime.now(timezone.utc).timestamp() * 1000)
    while start <= now_ms:
        page = _binance_page(url, symbol, timeframe, 1000, start)
        if not page:
            break
        pages.append(page)
        next_start = int(page[-1][0]) + step
        if next_start <= start:
            break
        start = next_start
    rows = []
    for page in pages:
        for k in page:
            rows.append((
                datetime.fromtimestamp(int(k[0]) / 1000, tz=timezone.utc),
                float(k[1]), float(k[2]), float(k[3]), float(k[4]), float(k[5]),
            ))
    return rows


def fetch_yahoo(symbol, timeframe, since=None):
    t = yf.Ticker(symbol)
    if since is None:
        df = t.history(period=YAHOO_PERIOD_BACKFILL[timeframe], interval=timeframe, auto_adjust=False)
    else:
        # Yahoo memakai granularitas tanggal; mundur 2 hari supaya candle terakhir pasti ikut.
        start = (since - timedelta(days=2)).date()
        df = t.history(start=start, interval=timeframe, auto_adjust=False)
    if df is None or df.empty:
        raise RuntimeError("data kosong dari Yahoo")
    df = df.dropna(subset=["Open", "High", "Low", "Close"])
    rows = []
    for ts, r in df.iterrows():
        rows.append((
            ts.to_pydatetime().astimezone(timezone.utc),
            float(r["Open"]), float(r["High"]), float(r["Low"]), float(r["Close"]),
            float(r["Volume"]) if r["Volume"] == r["Volume"] else 0.0,
        ))
    return rows


def log_job(conn, job, target, started_at, status, rows=0, message=None):
    """Catat ke job_runs (dibaca halaman admin). Gagal mencatat tidak menggagalkan ingestion."""
    try:
        with conn, conn.cursor() as cur:
            cur.execute(
                "INSERT INTO job_runs (job, target, started_at, status, rows, message) "
                "VALUES (%s, %s, %s, %s, %s, %s)",
                (job, target, started_at, status, rows, (message or "")[:500] or None))
    except Exception as e:  # mis. migrasi 006 belum dijalankan
        print(f"[warn] gagal mencatat job_runs: {e}")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--timeframe", choices=["1h", "1d"], required=True)
    ap.add_argument("--backfill", action="store_true", help="isi ulang sekitar 1 tahun")
    ap.add_argument("--symbols", help="daftar simbol dipisah koma (default: semua instrument). "
                                      "Dipakai backend saat instrumen ditambahkan lewat halaman admin")
    args = ap.parse_args()

    if not DATABASE_URL:
        sys.exit("DATABASE_URL belum di-set")
    conn = psycopg2.connect(DATABASE_URL)
    with conn, conn.cursor() as cur:
        cur.execute("SELECT symbol, source, id FROM instruments ORDER BY id")
        instruments = cur.fetchall()
        cur.execute("SELECT instrument_id, max(ts) FROM ohlcv WHERE timeframe = %s GROUP BY 1",
                    (args.timeframe,))
        last_ts = dict(cur.fetchall())
    if args.symbols:
        wanted = {s.strip().upper() for s in args.symbols.split(",") if s.strip()}
        instruments = [row for row in instruments if row[0] in wanted]
    upsert = UPSERT_SQL.format(interval=INTERVAL_SQL[args.timeframe])

    fetchers = {
        "yahoo": fetch_yahoo,
        "binance": fetch_binance,
        "binance_futures": lambda *a: fetch_binance(*a, url=BINANCE_FUTURES_URL),
    }
    job = f"ingest_{args.timeframe}"
    for symbol, source, inst_id in instruments:
        started = datetime.now(timezone.utc)
        try:
            if source not in fetchers:
                raise RuntimeError(f"source {source!r} tidak dikenal")
            since = None if args.backfill else last_ts.get(inst_id)
            rows = fetchers[source](symbol, args.timeframe, since)
            values = [(inst_id, args.timeframe, *r) for r in rows]
            with conn, conn.cursor() as cur:
                execute_values(cur, upsert, values,
                               template="(%s, %s, %s, %s, %s, %s, %s, %s, now())", page_size=1000)
            mode = "backfill" if since is None else f"sejak {since:%Y-%m-%d %H:%M}"
            print(f"[ok]   {symbol} ({source}) {args.timeframe}: {len(rows)} candle ({mode})")
            log_job(conn, job, symbol, started, "ok", len(rows), mode)
        except Exception as e:  # satu instrument gagal tidak menghentikan yang lain
            print(f"[fail] {symbol}: {e}")
            log_job(conn, job, symbol, started, "fail", 0, str(e))
    conn.close()


if __name__ == "__main__":
    main()

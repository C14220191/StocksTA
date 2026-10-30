"""Ingestion OHLCV ke PostgreSQL (tabel ohlcv).

Pemakaian:
    python ingestion/ingest.py --timeframe 1h              # update rutin (dijadwalkan tiap jam, menit ke-5)
    python ingestion/ingest.py --timeframe 1d
    python ingestion/ingest.py --timeframe 1h --backfill   # isi ulang sekitar 1 tahun (jalankan sekali)
    python ingestion/ingest.py --timeframe 1d --backfill

Perlu environment: DATABASE_URL, dan opsional BINANCE_URL.
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

SESSION = requests.Session()
SESSION.mount("https://", HTTPAdapter(max_retries=Retry(
    total=3, backoff_factor=1, status_forcelist=[429, 500, 502, 503, 504])))

INSTRUMENTS = [
    ("BBCA.JK", "yahoo"),
    ("BBRI.JK", "yahoo"),
    ("TLKM.JK", "yahoo"),
    ("BTCUSDT", "binance"),
    ("ETHUSDT", "binance"),
]

INTERVAL_MS = {"1h": 3_600_000, "1d": 86_400_000}
YAHOO_PERIOD = {"1h": "60d", "1d": "1y"}
YAHOO_PERIOD_BACKFILL = {"1h": "1y", "1d": "1y"}

UPSERT_SQL = """
INSERT INTO ohlcv (instrument_id, timeframe, ts, open, high, low, close, volume, fetched_at)
VALUES %s
ON CONFLICT (instrument_id, timeframe, ts) DO UPDATE SET
    open = EXCLUDED.open,
    high = EXCLUDED.high,
    low = EXCLUDED.low,
    close = EXCLUDED.close,
    volume = EXCLUDED.volume,
    fetched_at = now();
"""


def _binance_page(symbol, timeframe, limit, start_ms=None):
    params = {"symbol": symbol, "interval": timeframe, "limit": limit}
    if start_ms is not None:
        params["startTime"] = start_ms
    r = SESSION.get(BINANCE_URL, params=params, timeout=20)
    r.raise_for_status()
    return r.json()


def fetch_binance(symbol, timeframe, backfill=False):
    """Kembalikan list (ts_utc, open, high, low, close, volume). ts = waktu BUKA candle."""
    if not backfill:
        pages = [_binance_page(symbol, timeframe, 500)]
    else:
        pages = []
        step = INTERVAL_MS[timeframe]
        start = int((datetime.now(timezone.utc) - timedelta(days=365)).timestamp() * 1000)
        now_ms = int(datetime.now(timezone.utc).timestamp() * 1000)
        while start < now_ms:
            page = _binance_page(symbol, timeframe, 1000, start)
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


def fetch_yahoo(symbol, timeframe, backfill=False):
    period = (YAHOO_PERIOD_BACKFILL if backfill else YAHOO_PERIOD)[timeframe]
    df = yf.Ticker(symbol).history(period=period, interval=timeframe, auto_adjust=False)
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


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--timeframe", choices=["1h", "1d"], required=True)
    ap.add_argument("--backfill", action="store_true", help="isi ulang sekitar 1 tahun")
    args = ap.parse_args()

    if not DATABASE_URL:
        sys.exit("DATABASE_URL belum di-set")
    conn = psycopg2.connect(DATABASE_URL)
    with conn, conn.cursor() as cur:
        cur.execute("SELECT symbol, id FROM instruments")
        ids = dict(cur.fetchall())

    for symbol, source in INSTRUMENTS:
        try:
            if symbol not in ids:
                raise RuntimeError("instrument belum ada di tabel instruments")
            rows = (fetch_binance if source == "binance" else fetch_yahoo)(symbol, args.timeframe, args.backfill)
            values = [(ids[symbol], args.timeframe, *r) for r in rows]
            with conn, conn.cursor() as cur:
                execute_values(cur, UPSERT_SQL, values,
                               template="(%s, %s, %s, %s, %s, %s, %s, %s, now())", page_size=1000)
            print(f"[ok]   {symbol} ({source}) {args.timeframe}: {len(rows)} candle")
        except Exception as e:  # satu instrument gagal tidak menghentikan yang lain
            print(f"[fail] {symbol}: {e}")
    conn.close()


if __name__ == "__main__":
    main()

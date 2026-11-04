"""Ingestion data fundamental saham IDX dari Yahoo Finance (yfinance) ke tabel fundamentals.

Dipakai sebagai input prompt metode Multi-Agent Debate (analis fundamental) dan Grounded Research
Brief. Hanya instrument dengan source = yahoo; kripto dan komoditas tidak punya laporan keuangan.
Laporan tahunan jarang berubah, jadi cukup dijalankan sekali sehari.

Pemakaian:
    python ingestion/fundamentals.py                    # semua saham
    python ingestion/fundamentals.py --symbols BBCA.JK,TLKM.JK

Perlu environment: DATABASE_URL.
"""
import argparse
import json
import math
import os
import sys
from datetime import datetime, timezone

import psycopg2
import yfinance as yf

from ingest import log_job

DATABASE_URL = os.environ.get("DATABASE_URL")
YEARS = 3  # laporan tahunan terbaru yang disimpan (prompt metode meminta 3 tahun)

# Nama baris di laporan Yahoo; diambil yang pertama tersedia.
ROWS = {
    "revenue": (("income_stmt", "Total Revenue"), ("income_stmt", "Operating Revenue")),
    "net_income": (("income_stmt", "Net Income"), ("income_stmt", "Net Income Common Stockholders")),
    "free_cash_flow": (("cashflow", "Free Cash Flow"),),
    "operating_cash_flow": (("cashflow", "Operating Cash Flow"),),
    "total_debt": (("balance_sheet", "Total Debt"),),
    "equity": (("balance_sheet", "Stockholders Equity"), ("balance_sheet", "Common Stock Equity")),
}


def _num(v):
    try:
        x = float(v)
    except (TypeError, ValueError):
        return None
    return None if math.isnan(x) or math.isinf(x) else x


def fetch(symbol):
    t = yf.Ticker(symbol)
    info = t.info or {}
    sheets = {"income_stmt": t.income_stmt, "cashflow": t.cashflow, "balance_sheet": t.balance_sheet}

    years = {}
    for field, candidates in ROWS.items():
        for sheet, row in candidates:
            df = sheets[sheet]
            if df is None or df.empty or row not in df.index:
                continue
            for col, v in df.loc[row].items():
                x = _num(v)
                if x is not None:
                    years.setdefault(col.year, {})[field] = x
            break
    annual = []
    for year in sorted(years, reverse=True)[:YEARS]:
        row = {"year": year, **years[year]}
        if row.get("total_debt") is not None and row.get("equity"):
            row["der"] = row["total_debt"] / row["equity"]
        annual.append(row)

    if not annual and not info:
        raise RuntimeError("data fundamental kosong dari Yahoo")
    return {
        "currency": info.get("financialCurrency") or info.get("currency"),
        "sector": info.get("sector"),
        "industry": info.get("industry"),
        "business_summary": (info.get("longBusinessSummary") or "")[:600] or None,
        # Valuasi dari Yahoo, dihitung dengan harga Yahoo saat diambil (lihat fetched_at).
        "valuation": {
            "per": _num(info.get("trailingPE")),
            "pbv": _num(info.get("priceToBook")),
            "der": annual[0].get("der") if annual else None,  # total utang / ekuitas tahun terakhir
            "market_cap": _num(info.get("marketCap")),
            "eps_ttm": _num(info.get("trailingEps")),
            "book_value_per_share": _num(info.get("bookValue")),
        },
        "annual": annual,
    }


def main():
    sys.stdout.reconfigure(encoding="utf-8")
    ap = argparse.ArgumentParser()
    ap.add_argument("--symbols", help="daftar simbol dipisah koma (default: semua instrument source yahoo)")
    args = ap.parse_args()
    if not DATABASE_URL:
        sys.exit("DATABASE_URL belum di-set")

    conn = psycopg2.connect(DATABASE_URL)
    with conn, conn.cursor() as cur:
        cur.execute("SELECT id, symbol FROM instruments WHERE source = 'yahoo' ORDER BY id")
        instruments = cur.fetchall()
    if args.symbols:
        wanted = {s.strip().upper() for s in args.symbols.split(",")}
        instruments = [(i, s) for i, s in instruments if s in wanted]

    for inst_id, symbol in instruments:
        started = datetime.now(timezone.utc)
        try:
            data = fetch(symbol)
            with conn, conn.cursor() as cur:
                cur.execute("""
                    INSERT INTO fundamentals (instrument_id, fetched_at, source, data)
                    VALUES (%s, now(), 'yahoo', %s)
                    ON CONFLICT (instrument_id) DO UPDATE
                    SET fetched_at = now(), source = EXCLUDED.source, data = EXCLUDED.data""",
                            (inst_id, json.dumps(data)))
            years = ", ".join(str(a["year"]) for a in data["annual"]) or "tanpa laporan tahunan"
            print(f"[ok]   {symbol}: {years}")
            log_job(conn, "fundamentals", symbol, started, "ok", len(data["annual"]), years)
        except Exception as e:  # satu saham gagal tidak menghentikan yang lain
            print(f"[fail] {symbol}: {e}")
            log_job(conn, "fundamentals", symbol, started, "fail", 0, str(e))
    conn.close()


if __name__ == "__main__":
    main()

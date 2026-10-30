"""Backtest sinyal AI 24 jam: isi ai_summaries.price_after_24h dan signal_correct.

Pemakaian:
    python ingestion/backtest.py                 # proses summary yang sudah berumur >= 24 jam
    python ingestion/backtest.py --hold-band 1.0 # lebar zona HOLD (persen), default 1.0

Definisi (catat di Metodologi):
    price_after_24h = close candle 1h PERTAMA yang tutup pada/setelah generated_at + 24 jam
                      (saham: kalau jatuh di akhir pekan/libur, candle berikutnya saat bursa buka)
    return          = (price_after_24h / price_at_generation - 1) * 100
    signal_correct  = BUY  -> return >  hold_band
                      SELL -> return < -hold_band
                      HOLD -> |return| <= hold_band
Summary tanpa signal hanya diisi price_after_24h (signal_correct tetap NULL).

Perlu environment: DATABASE_URL. Aman dijalankan berulang (hanya baris yang masih NULL).
"""
import argparse
import os
import sys

import psycopg2

DATABASE_URL = os.environ.get("DATABASE_URL")

UPDATE_SQL = """
WITH due AS (
    SELECT s.id, s.signal, s.price_at_generation AS p0, x.close AS p1
    FROM ai_summaries s
    CROSS JOIN LATERAL (
        SELECT o.close
        FROM ohlcv o
        WHERE o.instrument_id = s.instrument_id
          AND o.timeframe = '1h'
          AND o.ts + interval '1 hour' >= s.generated_at + interval '24 hours'
          AND o.fetched_at >= o.ts + interval '1 hour'   -- hanya candle yang sudah tutup
        ORDER BY o.ts
        LIMIT 1
    ) x
    WHERE s.price_after_24h IS NULL
      AND s.price_at_generation > 0
      AND s.generated_at <= now() - interval '24 hours'
)
UPDATE ai_summaries s
SET price_after_24h = due.p1,
    signal_correct = CASE due.signal
        WHEN 'BUY'  THEN (due.p1 / due.p0 - 1) * 100 >  %(band)s
        WHEN 'SELL' THEN (due.p1 / due.p0 - 1) * 100 < -%(band)s
        WHEN 'HOLD' THEN abs((due.p1 / due.p0 - 1) * 100) <= %(band)s
    END
FROM due
WHERE s.id = due.id
"""

SUMMARY_SQL = """
SELECT i.symbol, s.signal, count(*) AS n,
       count(*) FILTER (WHERE s.signal_correct) AS benar,
       round(avg((s.price_after_24h / s.price_at_generation - 1) * 100), 3) AS avg_return_pct
FROM ai_summaries s JOIN instruments i ON i.id = s.instrument_id
WHERE s.price_after_24h IS NOT NULL AND s.signal IS NOT NULL
GROUP BY 1, 2 ORDER BY 1, 2
"""


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--hold-band", type=float, default=1.0, help="lebar zona HOLD dalam persen")
    args = ap.parse_args()
    if not DATABASE_URL:
        sys.exit("DATABASE_URL belum di-set")

    conn = psycopg2.connect(DATABASE_URL)
    with conn, conn.cursor() as cur:
        cur.execute(UPDATE_SQL, {"band": args.hold_band})
        print(f"[ok] {cur.rowcount} summary diisi (hold band {args.hold_band}%)")
        cur.execute(SUMMARY_SQL)
        rows = cur.fetchall()
    conn.close()

    if rows:
        print(f"{'symbol':<9} {'signal':<6} {'n':>4} {'benar':>6} {'akurasi':>8} {'avg ret %':>10}")
        for sym, sig, n, ok, ret in rows:
            print(f"{sym:<9} {sig:<6} {n:>4} {ok:>6} {ok / n * 100:>7.1f}% {ret:>10}")


if __name__ == "__main__":
    main()

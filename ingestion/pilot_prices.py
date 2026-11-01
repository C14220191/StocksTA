"""Studi pendahuluan (Bab 3.4) bagian 2: distribusi pergerakan harga dan volatilitas.

Dari candle 1h historis yang sudah tutup, untuk setiap instrument dihitung:
  1. Volatilitas rolling (stddev log return, jendela n candle): rata-rata, median, p90
     -> kandidat sigma_ref (SIGMA_REF_STAT di run.env).
  2. Peluang drift harga melewati theta (1, 2, 5, 10%) dalam jendela w jam setelah analisis dibuat.
     Drift diukur dari ekskursi maksimum (high/low) relatif terhadap close awal, karena entri cache
     sudah basi begitu harga menyentuh ambang di tengah jendela.
     -> menunjukkan risiko TTL statis dan membantu memilih TTL_min / TTL_max.
  3. Median waktu sampai drift pertama kali >= theta.

Pemakaian:
    python ingestion/pilot_prices.py                       # semua instrument, 365 hari
    python ingestion/pilot_prices.py --days 90 --csv pilot_prices.csv
Perlu environment: DATABASE_URL.
"""
import argparse
import csv
import math
import os
import statistics
import sys

import psycopg2

DATABASE_URL = os.environ.get("DATABASE_URL")
THETAS = [1, 2, 5, 10]
WINDOWS_H = [1, 4, 12, 24, 72]
MAX_SCAN_H = 24 * 14  # batas pencarian waktu-sampai-melewati-theta


def load(conn, days):
    with conn.cursor() as cur:
        cur.execute("""
            SELECT i.symbol, i.asset_type, o.ts, o.high::float8, o.low::float8, o.close::float8
            FROM ohlcv o JOIN instruments i ON i.id = o.instrument_id
            WHERE o.timeframe = '1h' AND o.ts >= now() - make_interval(days => %s)
              AND o.fetched_at >= o.ts + interval '1 hour'
            ORDER BY i.id, o.ts""", (days,))
        data = {}
        for sym, at, ts, hi, lo, cl in cur.fetchall():
            data.setdefault((sym, at), []).append((ts, hi, lo, cl))
    return data


def rolling_vol(closes, n):
    rets = [math.log(closes[i] / closes[i - 1]) for i in range(1, len(closes)) if closes[i - 1] > 0 and closes[i] > 0]
    out = []
    for i in range(n - 1, len(rets) + 1):
        w = rets[i - (n - 1):i]
        if len(w) >= 2:
            out.append(statistics.stdev(w))
    return out


def pct(sorted_v, q):
    if not sorted_v:
        return float("nan")
    pos = q * (len(sorted_v) - 1)
    lo, hi = math.floor(pos), math.ceil(pos)
    return sorted_v[lo] + (sorted_v[hi] - sorted_v[lo]) * (pos - lo)


def analyse(rows, n):
    closes = [r[3] for r in rows]
    vols = sorted(rolling_vol(closes, n))
    res = {
        "candles": len(rows),
        "sigma_mean": statistics.fmean(vols) if vols else float("nan"),
        "sigma_p50": pct(vols, 0.5),
        "sigma_p90": pct(vols, 0.9),
    }
    # drift maksimum dalam w jam setelah setiap candle (posisi indeks; saham punya celah malam/akhir pekan,
    # jadi "w jam" untuk saham = w candle bursa)
    for w in WINDOWS_H:
        hits = {t: 0 for t in THETAS}
        total = 0
        for i in range(len(rows) - w):
            p0 = closes[i]
            if p0 <= 0:
                continue
            seg = rows[i + 1:i + 1 + w]
            up = max(r[1] for r in seg) / p0 - 1
            down = 1 - min(r[2] for r in seg) / p0
            drift = max(up, down) * 100
            total += 1
            for t in THETAS:
                if drift >= t:
                    hits[t] += 1
        for t in THETAS:
            res[f"p_cross_{t}pct_{w}h"] = hits[t] / total * 100 if total else float("nan")
    # median waktu sampai drift pertama kali >= theta
    step = 6  # sampel tiap 6 candle supaya cepat
    for t in THETAS:
        times = []
        for i in range(0, len(rows) - 1, step):
            p0 = closes[i]
            for j in range(i + 1, min(len(rows), i + 1 + MAX_SCAN_H)):
                if max(rows[j][1] / p0 - 1, 1 - rows[j][2] / p0) * 100 >= t:
                    times.append(j - i)
                    break
            else:
                times.append(None)
        crossed = sorted(x for x in times if x is not None)
        res[f"median_h_to_{t}pct"] = pct(crossed, 0.5) if crossed else float("nan")
        res[f"never_{t}pct_14d_share"] = (len(times) - len(crossed)) / len(times) * 100 if times else float("nan")
    return res


def main():
    sys.stdout.reconfigure(encoding="utf-8")  # konsol Windows (cp1252) tidak bisa mencetak σ / c̄
    ap = argparse.ArgumentParser()
    ap.add_argument("--days", type=int, default=365)
    ap.add_argument("--window", type=int, default=14, help="n candle untuk volatilitas (sama dengan backend)")
    ap.add_argument("--csv", help="simpan hasil lengkap ke file CSV")
    args = ap.parse_args()
    if not DATABASE_URL:
        sys.exit("DATABASE_URL belum di-set")

    conn = psycopg2.connect(DATABASE_URL)
    data = load(conn, args.days)
    conn.close()

    results = []
    for (sym, at), rows in data.items():
        r = analyse(rows, args.window)
        r.update(symbol=sym, asset_type=at)
        results.append(r)

    print(f"Pilot: distribusi pergerakan harga, {args.days} hari, candle 1h tertutup\n")
    print(f"{'symbol':<9} {'candle':>6} {'σ mean':>9} {'σ p50':>9} {'σ p90':>9}")
    for r in results:
        print(f"{r['symbol']:<9} {r['candles']:>6} {r['sigma_mean']:>9.5f} {r['sigma_p50']:>9.5f} {r['sigma_p90']:>9.5f}")

    for w in WINDOWS_H:
        print(f"\nPeluang drift >= θ dalam {w} jam setelah analisis (%)")
        print(f"{'symbol':<9} " + " ".join(f"{'θ=' + str(t) + '%':>8}" for t in THETAS))
        for r in results:
            print(f"{r['symbol']:<9} " + " ".join(f"{r[f'p_cross_{t}pct_{w}h']:>8.1f}" for t in THETAS))

    print("\nMedian waktu (jam) sampai drift pertama kali >= θ  [porsi tidak pernah dalam 14 hari]")
    print(f"{'symbol':<9} " + " ".join(f"{'θ=' + str(t) + '%':>14}" for t in THETAS))
    for r in results:
        cells = [f"{r[f'median_h_to_{t}pct']:>6.1f} [{r[f'never_{t}pct_14d_share']:>4.0f}%]" for t in THETAS]
        print(f"{r['symbol']:<9} " + " ".join(f"{c:>14}" for c in cells))

    if args.csv:
        with open(args.csv, "w", newline="", encoding="utf-8") as f:
            w = csv.DictWriter(f, fieldnames=list(results[0].keys()))
            w.writeheader()
            w.writerows(results)
        print(f"\n[ok] disimpan ke {args.csv}")


if __name__ == "__main__":
    main()

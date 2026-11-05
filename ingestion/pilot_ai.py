"""Studi pendahuluan (Bab 3.4) bagian 1: karakteristik inference Hermes Agent tanpa cache.

Memanggil /api/analyze backend sebanyak N kali per instrument per metode analisis (berurutan, ada
jeda), dengan backend di CACHE_MODE=no_cache sehingga setiap panggilan benar-benar menjalankan
Hermes. Setiap panggilan tercatat di inference_log dengan trigger = pilot. Yang dilaporkan per jenis
aset dan metode: latency, T_in, T_out, cost per analisis (rata-rata, simpangan baku, p50, p95),
tingkat JSON valid, serta ilustrasi C_nc = N x c̄ (cost tanpa cache naik linier terhadap request).

Pemakaian:
    python ingestion/pilot_ai.py --n 10                               # semua instrument, 3 metode
    python ingestion/pilot_ai.py --n 20 --symbols BTCUSDT,BBCA.JK --methods mtf --csv pilot_ai.csv
Opsional: --url http://127.0.0.1:8080 --pause 2
"""
import argparse
import csv
import statistics
import sys
import time

import requests


def summary(values):
    v = sorted(x for x in values if x is not None)
    if not v:
        return "-"
    p = lambda q: v[min(len(v) - 1, round(q * (len(v) - 1)))]
    sd = statistics.stdev(v) if len(v) > 1 else 0
    return f"mean {statistics.fmean(v):,.1f} · sd {sd:,.1f} · p50 {p(0.5):,.1f} · p95 {p(0.95):,.1f}"


def main():
    # konsol Windows (cp1252) tidak bisa mencetak σ / c̄; line_buffering supaya progres langsung terlihat
    # walau output diarahkan ke file (nohup ... > logs/pilot_ai.out)
    sys.stdout.reconfigure(encoding="utf-8", line_buffering=True)
    ap = argparse.ArgumentParser()
    ap.add_argument("--n", type=int, default=10, help="jumlah panggilan per instrument")
    ap.add_argument("--symbols", help="daftar simbol dipisah koma (default: semua instrument)")
    ap.add_argument("--url", default="http://127.0.0.1:8080")
    ap.add_argument("--pause", type=float, default=2.0, help="jeda antar panggilan (detik)")
    ap.add_argument("--methods", default="debate,mtf,grounded", help="metode analisis dipisah koma")
    ap.add_argument("--csv", help="simpan setiap panggilan ke file CSV")
    args = ap.parse_args()
    methods = [m.strip() for m in args.methods.split(",") if m.strip()]

    health = requests.get(f"{args.url}/health", timeout=5).json()
    if health.get("cache_mode") != "no_cache":
        sys.exit(f"Backend harus CACHE_MODE=no_cache untuk pilot (sekarang {health.get('cache_mode')}). "
                 r"Jalankan: .\ta-up.ps1 restart no_cache http")

    if args.symbols:
        symbols = [(s.strip().upper(), "?") for s in args.symbols.split(",")]
    else:
        ov = requests.get(f"{args.url}/api/market/overview", params={"limit": 50, "movers": 1}, timeout=10).json()
        symbols = [(r["symbol"], r["asset_type"]) for r in ov["top_volume"]]

    rows = []
    total = len(symbols) * len(methods) * args.n
    for k in range(args.n):
        for sym, at in symbols:
            for method in methods:
                t0 = time.monotonic()
                try:
                    r = requests.get(f"{args.url}/api/analyze/{sym}", params={"method": method},
                                     headers={"X-TA-Trigger": "pilot"}, timeout=400)
                    d = r.json()
                    ok = r.status_code == 200
                except Exception as e:  # catat dan lanjut
                    d, ok = {"error": str(e)}, False
                wall = (time.monotonic() - t0) * 1000
                row = {"run": k + 1, "symbol": sym, "asset_type": at, "method": method, "ok": ok,
                       "format_ok": d.get("format_ok"), "wall_ms": round(wall),
                       "latency_ms": d.get("latency_ms"), "tokens_in": d.get("tokens_in"),
                       "tokens_out": d.get("tokens_out"), "cache_read_tokens": d.get("cache_read_tokens"),
                       "cost_usd": d.get("cost_usd"), "error": d.get("error", "")}
                rows.append(row)
                print(f"[{len(rows):>3}/{total}] {sym:<9} {method:<8} {'ok ' if ok else 'ERR'} "
                      f"{row['latency_ms'] or 0:>6} ms  in {row['tokens_in'] or 0:>5}  out {row['tokens_out'] or 0:>4}"
                      f"{'' if row['format_ok'] is not False else '  (JSON tidak valid)'}")
                time.sleep(args.pause)

    print("\nRingkasan per jenis aset dan metode (hanya panggilan berhasil)")
    for at, method in sorted({(r["asset_type"], r["method"]) for r in rows}):
        g = [r for r in rows if r["asset_type"] == at and r["method"] == method and r["ok"]]
        if not g:
            continue
        valid = sum(1 for r in g if r["format_ok"] is not False)
        print(f"\n{at} · {method} (n={len(g)}, JSON valid {valid}/{len(g)})")
        print(f"  latency ms : {summary([r['latency_ms'] for r in g])}")
        print(f"  T_in       : {summary([r['tokens_in'] for r in g])}")
        print(f"  T_out      : {summary([r['tokens_out'] for r in g])}")
        costs = [r["cost_usd"] or 0 for r in g]
        cbar = statistics.fmean(costs)
        print(f"  cost/analisis c̄ = ${cbar:.6f}  ->  C_nc = N x c̄ : "
              + ", ".join(f"N={n:,}: ${n * cbar:,.4f}" for n in (100, 1_000, 10_000)))
    fails = [r for r in rows if not r["ok"]]
    if fails:
        print(f"\n{len(fails)} panggilan gagal, contoh: {fails[0]['error']}")

    if args.csv:
        with open(args.csv, "w", newline="", encoding="utf-8") as f:
            w = csv.DictWriter(f, fieldnames=list(rows[0].keys()))
            w.writeheader()
            w.writerows(rows)
        print(f"\n[ok] disimpan ke {args.csv}")


if __name__ == "__main__":
    main()

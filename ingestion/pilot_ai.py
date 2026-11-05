"""Studi pendahuluan (Bab 3.4) bagian 1: karakteristik inference Hermes Agent tanpa cache.

Memanggil /api/analyze backend sebanyak N kali per instrument per metode analisis (berurutan, ada
jeda), dengan backend di CACHE_MODE=no_cache sehingga setiap panggilan benar-benar menjalankan
Hermes. Setiap panggilan tercatat di inference_log dengan trigger = pilot. Yang dilaporkan per jenis
aset dan metode: latency, T_in, T_out, cost per analisis (rata-rata, simpangan baku, p50, p95),
tingkat JSON valid, serta ilustrasi C_nc = N x c̄ (cost tanpa cache naik linier terhadap request).

T_in dilaporkan sebagai input total = tokens_in + cache_read_tokens: provider menyimpan prefix prompt
(system prompt Hermes + skill) di prompt cache-nya sendiri, dan tokens_in dari Hermes tidak menghitung
bagian itu. Tanpa dijumlahkan, T_in tampak berfluktuasi ~1600 token padahal prompt-nya sama.

Hanya boleh satu pilot berjalan: dua pilot bersamaan saling antre di Hermes (HERMES_MAX_CONCURRENCY=1)
sehingga latency yang terukur ikut memuat waktu antre proses lain.

Pemakaian:
    python ingestion/pilot_ai.py --n 10                               # semua instrument, 3 metode
    python ingestion/pilot_ai.py --n 20 --symbols BTCUSDT,BBCA.JK --methods mtf --csv pilot_ai.csv
    python ingestion/pilot_ai.py --from-csv logs/pilot_ai.csv --price-in 0.10 --price-out 0.50
                                       # hitung ulang ringkasan dari CSV dengan harga API acuan (USD / 1M token)
Opsional: --url http://127.0.0.1:8080 --pause 2
"""
import argparse
import csv
import os
import statistics
import sys
import time
from pathlib import Path

import requests

LOG_DIR = Path(__file__).resolve().parent.parent / "logs"
FIELDS = ["run", "symbol", "asset_type", "method", "ok", "format_ok", "wall_ms", "latency_ms", "tokens_in",
          "tokens_out", "cache_read_tokens", "cost_usd", "error"]
HEADERS = {"X-TA-Trigger": "pilot"}


def summary(values):
    v = sorted(x for x in values if x is not None)
    if not v:
        return "-"
    p = lambda q: v[min(len(v) - 1, round(q * (len(v) - 1)))]
    sd = statistics.stdev(v) if len(v) > 1 else 0
    return f"mean {statistics.fmean(v):,.1f} · sd {sd:,.1f} · p50 {p(0.5):,.1f} · p95 {p(0.95):,.1f}"


def single_instance():
    """Keluar kalau pilot lain masih memegang kunci. Kunci lepas otomatis saat proses berhenti
    (termasuk kalau di-kill), jadi tidak ada file kunci basi yang perlu dihapus."""
    LOG_DIR.mkdir(exist_ok=True)
    f = open(LOG_DIR / "pilot_ai.lock", "a+")
    try:
        if os.name == "nt":
            import msvcrt
            f.seek(0)
            msvcrt.locking(f.fileno(), msvcrt.LK_NBLCK, 1)
        else:
            import fcntl
            fcntl.flock(f, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except OSError:
        sys.exit("Pilot lain masih berjalan (cek: ps aux | grep [p]ilot_ai). Tunggu selesai atau hentikan dulu "
                 "dengan: pkill -f pilot_ai.py")
    return f  # harus tetap terbuka selama proses berjalan


def asset_type(url, symbol):
    try:
        r = requests.get(f"{url}/api/instruments/{symbol}", headers=HEADERS, timeout=10)
        return r.json()["instrument"]["asset_type"]
    except Exception:
        return "?"


def load_csv(path):
    def num(x):
        return None if x in ("", None) else float(x)

    def flag(x):
        return {"True": True, "False": False}.get(x)

    rows = []
    with open(path, newline="", encoding="utf-8") as f:
        for r in csv.DictReader(f):
            for k in ("wall_ms", "latency_ms", "tokens_in", "tokens_out", "cache_read_tokens", "cost_usd"):
                r[k] = num(r.get(k))
            r["ok"], r["format_ok"] = flag(r["ok"]), flag(r.get("format_ok"))
            rows.append(r)
    return rows


def cost_of(row, args):
    """Cost per panggilan: dari backend (PRICE_*_PER_M di run.env), atau dari harga acuan --price-*."""
    if args.price_in is None and args.price_out is None:
        return row["cost_usd"] or 0
    p_in, p_out = args.price_in or 0, args.price_out or 0
    p_cache = p_in if args.price_cache is None else args.price_cache
    return ((row["tokens_in"] or 0) * p_in + (row["tokens_out"] or 0) * p_out
            + (row["cache_read_tokens"] or 0) * p_cache) / 1e6


def report(rows, args):
    if args.price_in is not None or args.price_out is not None:
        p_cache = args.price_in if args.price_cache is None else args.price_cache
        basis = f"harga acuan in ${args.price_in or 0}/1M, out ${args.price_out or 0}/1M, cache_read ${p_cache or 0}/1M"
    else:
        basis = "cost_usd dari backend (PRICE_*_PER_M); 0 untuk model gratis/langganan"
    print(f"\nRingkasan per jenis aset dan metode (hanya panggilan berhasil). Cost: {basis}")
    methods = sorted({r["method"] for r in rows})
    groups = sorted({(r["asset_type"], r["method"]) for r in rows})
    if len({at for at, _ in groups}) > 1:
        groups += [("semua", m) for m in methods]
    for at, method in groups:
        g = [r for r in rows if r["method"] == method and r["ok"] and at in ("semua", r["asset_type"])]
        if not g:
            continue
        valid = sum(1 for r in g if r["format_ok"] is not False)
        print(f"\n{at} · {method} (n={len(g)}, JSON valid {valid}/{len(g)} = {valid / len(g):.0%})")
        print(f"  latency ms : {summary([r['latency_ms'] for r in g])}")
        print(f"  T_in total : {summary([(r['tokens_in'] or 0) + (r['cache_read_tokens'] or 0) for r in g])}")
        print(f"   cache_read: {summary([r['cache_read_tokens'] or 0 for r in g])}")
        print(f"  T_out      : {summary([r['tokens_out'] for r in g])}")
        cbar = statistics.fmean(cost_of(r, args) for r in g)
        print(f"  cost/analisis c̄ = ${cbar:.6f}  ->  C_nc = N x c̄ : "
              + ", ".join(f"N={n:,}: ${n * cbar:,.4f}" for n in (100, 1_000, 10_000)))
    fails = [r for r in rows if not r["ok"]]
    if fails:
        print(f"\n{len(fails)} panggilan gagal (pesan lengkap ada di inference_log.error):")
        for r in fails[:10]:
            print(f"  run {r['run']} {r['symbol']} {r['method']}: {r['error']}")


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
    ap.add_argument("--csv", help="simpan setiap panggilan ke file CSV (ditulis per baris)")
    ap.add_argument("--from-csv", help="tidak memanggil backend; hanya hitung ringkasan dari CSV pilot sebelumnya")
    ap.add_argument("--price-in", type=float, help="harga acuan input, USD per 1M token")
    ap.add_argument("--price-out", type=float, help="harga acuan output, USD per 1M token")
    ap.add_argument("--price-cache", type=float, help="harga acuan cache_read per 1M token (default = --price-in)")
    args = ap.parse_args()

    if args.from_csv:
        report(load_csv(args.from_csv), args)
        return

    lock = single_instance()  # sebelum CSV dibuka, supaya tidak menimpa CSV pilot yang sedang berjalan
    methods = [m.strip() for m in args.methods.split(",") if m.strip()]
    health = requests.get(f"{args.url}/health", timeout=5).json()
    if health.get("cache_mode") != "no_cache":
        sys.exit(f"Backend harus CACHE_MODE=no_cache untuk pilot (sekarang {health.get('cache_mode')}). "
                 "Jalankan: ./ta-up.sh restart no_cache http   (Windows: .\\ta-up.ps1 restart no_cache http)")

    if args.symbols:
        symbols = [(s, asset_type(args.url, s)) for s in (x.strip().upper() for x in args.symbols.split(",")) if s]
    else:
        ov = requests.get(f"{args.url}/api/market/overview", params={"limit": 50, "movers": 1}, timeout=10).json()
        symbols = [(r["symbol"], r["asset_type"]) for r in ov["top_volume"]]

    out = writer = None
    if args.csv:
        out = open(args.csv, "w", newline="", encoding="utf-8")
        writer = csv.DictWriter(out, fieldnames=FIELDS)
        writer.writeheader()

    rows = []
    total = len(symbols) * len(methods) * args.n
    for k in range(args.n):
        for sym, at in symbols:
            for method in methods:
                t0 = time.monotonic()
                try:
                    r = requests.get(f"{args.url}/api/analyze/{sym}", params={"method": method},
                                     headers=HEADERS, timeout=400)
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
                if writer:
                    writer.writerow(row)
                    out.flush()  # pilot yang terhenti di tengah tetap menyisakan data
                print(f"[{len(rows):>3}/{total}] {sym:<9} {method:<8} {'ok ' if ok else 'ERR'} "
                      f"{row['latency_ms'] or round(wall):>6} ms  in {row['tokens_in'] or 0:>5}"
                      f" +cache {row['cache_read_tokens'] or 0:>5}  out {row['tokens_out'] or 0:>4}"
                      f"{'' if row['format_ok'] is not False else '  (JSON tidak valid)'}"
                      f"{'' if ok else '  ' + str(row['error'])[:80]}")
                time.sleep(args.pause)

    report(rows, args)
    if out:
        out.close()
        print(f"\n[ok] disimpan ke {args.csv}")
    lock.close()


if __name__ == "__main__":
    main()

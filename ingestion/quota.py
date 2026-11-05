"""Snapshot kuota langganan AI ke tabel quota_snapshots (migrasi 010).

Memakai `hermes usage --json` (Codex 5h/weekly, Anthropic OAuth, OpenRouter credits), jadi kredensial
sama dengan yang dipakai Hermes untuk analisis. Jalankan tiap 10 menit selama eksperimen (cron /
Task Scheduler), dan sekali tepat sebelum serta sesudah satu sesi uji. Makin rapat snapshot, makin
kecil pemakaian yang hilang saat jendela kuota reset di antara dua snapshot.

Pemakaian:
    python ingestion/quota.py                      # provider = HERMES_PROVIDER, default openai-codex
    python ingestion/quota.py --provider openai-codex

Perlu environment: DATABASE_URL, opsional HERMES_BIN, HERMES_PROVIDER.
"""
import argparse
import json
import os
import subprocess
import sys
from datetime import datetime, timezone

import psycopg2

from ingest import log_job

DATABASE_URL = os.environ.get("DATABASE_URL")
HERMES_BIN = os.environ.get("HERMES_BIN", "hermes")


def fetch(provider):
    p = subprocess.run([HERMES_BIN, "usage", "--provider", provider, "--json"], capture_output=True,
                       text=True, encoding="utf-8", errors="replace", timeout=60)
    try:
        doc = json.loads(p.stdout)
    except ValueError:
        raise RuntimeError((p.stdout or p.stderr).strip()[:300] or f"hermes usage keluar dengan kode {p.returncode}")
    if doc.get("unavailable_reason"):
        raise RuntimeError(doc["unavailable_reason"])
    windows = [w for w in doc.get("windows") or [] if w.get("used_percent") is not None]
    if not windows:
        raise RuntimeError("tidak ada jendela kuota di respons hermes usage")
    return doc, windows


def main():
    sys.stdout.reconfigure(encoding="utf-8")
    ap = argparse.ArgumentParser()
    ap.add_argument("--provider", default=os.environ.get("HERMES_PROVIDER") or "openai-codex")
    args = ap.parse_args()
    if not DATABASE_URL:
        sys.exit("DATABASE_URL belum di-set")

    conn = psycopg2.connect(DATABASE_URL)
    started = datetime.now(timezone.utc)
    try:
        doc, windows = fetch(args.provider)
        with conn, conn.cursor() as cur:
            for w in windows:
                cur.execute(
                    "INSERT INTO quota_snapshots (fetched_at, provider, plan, window_label, used_percent, resets_at) "
                    "VALUES (%s, %s, %s, %s, %s, %s)",
                    (doc.get("fetched_at") or started, args.provider, doc.get("plan"), w.get("label"),
                     w.get("used_percent"), w.get("resets_at")))
        text = ", ".join(f"{w['label']} {w['used_percent']:.1f}%" for w in windows)
        print(f"[ok]   {args.provider} ({doc.get('plan') or '-'}): {text}")
        log_job(conn, "quota", args.provider, started, "ok", len(windows), text)
    except Exception as e:
        print(f"[fail] {args.provider}: {e}")
        log_job(conn, "quota", args.provider, started, "fail", 0, str(e))
    conn.close()


if __name__ == "__main__":
    main()

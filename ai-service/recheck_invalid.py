"""Uji ulang parser terhadap output Hermes yang dulu gagal format (ai_format_ok = false), tanpa memanggil
Hermes lagi. Dipakai setelah parser diperbaiki untuk melihat berapa kegagalan yang kini terbaca, dan
pola mana yang masih gagal.

Input: JSONL, satu baris per output, dari Postgres:
    docker exec ta_postgres psql -U ta_user -d ta_db -At -c "SELECT json_build_object('id', l.id, 'm', l.method, 'c', s.content) FROM inference_log l JOIN ai_summaries s ON s.id = l.summary_id WHERE l.trigger = 'pilot' AND l.ai_format_ok IS FALSE ORDER BY l.id" > logs/invalid_pilot.jsonl
Pemakaian (dari folder ai-service):
    python recheck_invalid.py ../logs/invalid_pilot.jsonl
Output yang masih gagal ditulis ke ../logs/invalid_still.txt (format sama dengan testdata/).
"""
import collections
import json
import logging
import sys
from pathlib import Path

import app

logging.disable(logging.WARNING)  # peringatan per output dari parse_output tidak perlu di sini


def main():
    src = Path(sys.argv[1] if len(sys.argv) > 1 else "../logs/invalid_pilot.jsonl")
    count, still = collections.Counter(), []
    for line in src.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        r = json.loads(line)
        ok = app.parse_output(r["c"], 1.0, r["m"])["format_ok"]
        count[r["m"], ok] += 1
        if not ok:
            still.append(f"=== log {r['id']} {r['m']} ({len(r['c'])} karakter)\n{r['c']}\n")
    for m in sorted({m for m, _ in count}):
        total = count[m, True] + count[m, False]
        print(f"{m:<9} dulu gagal {total:>3} -> kini terbaca {count[m, True]:>3}, masih gagal {count[m, False]:>3}")
    out = src.parent / "invalid_still.txt"
    out.write_text("".join(still), encoding="utf-8")
    print(f"\n{len(still)} output masih gagal -> {out}" if still else "\nsemua output yang dulu gagal kini terbaca")


if __name__ == "__main__":
    main()

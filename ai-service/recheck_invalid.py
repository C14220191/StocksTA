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
STORED_LIMIT = 6000  # parse_output menyimpan teks gagal maksimal 6000 karakter


def diagnose(text: str) -> str:
    """Alasan singkat kenapa output tetap gagal, untuk taksonomi kegagalan format di Bab 4."""
    data, _ = app._load_json(text)
    if data is not None:  # JSON terbaca dengan kunci kesimpulan, tetapi isinya tidak memenuhi skill
        if not app._bias(data.get("bias")):
            return f"bias tidak dikenal: {str(data.get('bias'))[:40]!r}"
        return "summary kosong"
    start = text.find("{")
    try:
        d = app.json_repair.loads(app._close_arrays(text[start:])) if start >= 0 else None
    except Exception:  # noqa: BLE001
        d = None
    if isinstance(d, dict) and d:
        why = f"tanpa bias/summary; kunci tingkat atas: {', '.join(list(d)[:8])}"
    else:
        why = "bukan JSON"
    if len(text) >= STORED_LIMIT:
        why += " (teks tersimpan terpotong 6000 karakter; output asli lebih panjang)"
    return why


def main():
    src = Path(sys.argv[1] if len(sys.argv) > 1 else "../logs/invalid_pilot.jsonl")
    count, still, reasons = collections.Counter(), [], []
    for line in src.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        r = json.loads(line)
        ok = app.parse_output(r["c"], 1.0, r["m"])["format_ok"]
        count[r["m"], ok] += 1
        if not ok:
            still.append(f"=== log {r['id']} {r['m']} ({len(r['c'])} karakter)\n{r['c']}\n")
            tail = " ".join(r["c"][-140:].split())
            reasons.append(f"log {r['id']:>4} {r['m']:<8} {len(r['c']):>5} kar: {diagnose(r['c'])}\n"
                           f"          akhir teks: ...{tail}")
    for m in sorted({m for m, _ in count}):
        total = count[m, True] + count[m, False]
        print(f"{m:<9} dulu gagal {total:>3} -> kini terbaca {count[m, True]:>3}, masih gagal {count[m, False]:>3}")
    if reasons:
        print("\nOutput yang masih gagal:")
        print("\n".join(reasons))
    out = src.parent / "invalid_still.txt"
    out.write_text("".join(still), encoding="utf-8")
    print(f"\n{len(still)} output masih gagal -> {out}" if still else "\nsemua output yang dulu gagal kini terbaca")


if __name__ == "__main__":
    main()

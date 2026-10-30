"""TA AI service: FastAPI yang membungkus Hermes Agent (subprocess `hermes -z`).

Konfigurasi lewat environment (diisi dari run.env):
    HERMES_BIN              default "hermes"
    HERMES_MODEL            default "" (pakai model default Hermes)
    HERMES_PROVIDER         default "" (pakai provider default Hermes)
    HERMES_TOOLSETS         default "clarify"  (toolset terkecil, memangkas overhead token)
    HERMES_REASONING        default "none"
    HERMES_TIMEOUT          default 120 (detik, per proses Hermes)
    HERMES_MAX_CONCURRENCY  default 1 (jumlah proses Hermes yang boleh jalan bersamaan)
    PROMPT_VERSION          default "hermes-v2" (naikkan setiap prompt diubah)
    PRICE_IN_PER_M, PRICE_OUT_PER_M, PRICE_CACHE_READ_PER_M
                            harga resmi model (USD per juta token). Kalau salah satunya > 0,
                            cost_usd dihitung sendiri (biaya setara); kalau semuanya 0,
                            dipakai angka yang dilaporkan Hermes.
"""
import asyncio
import json
import logging
import os
import re
import subprocess
import tempfile
import threading

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

HERMES_BIN = os.environ.get("HERMES_BIN", "hermes")
HERMES_MODEL = os.environ.get("HERMES_MODEL", "")
HERMES_PROVIDER = os.environ.get("HERMES_PROVIDER", "")
HERMES_TOOLSETS = os.environ.get("HERMES_TOOLSETS", "clarify")
HERMES_REASONING = os.environ.get("HERMES_REASONING", "none")
HERMES_TIMEOUT = int(os.environ.get("HERMES_TIMEOUT", "120"))
MAX_CONCURRENCY = int(os.environ.get("HERMES_MAX_CONCURRENCY", "1"))
PROMPT_VERSION = os.environ.get("PROMPT_VERSION", "hermes-v2")
PRICE_IN = float(os.environ.get("PRICE_IN_PER_M", "0") or 0)
PRICE_OUT = float(os.environ.get("PRICE_OUT_PER_M", "0") or 0)
PRICE_CACHE = float(os.environ.get("PRICE_CACHE_READ_PER_M", "0") or 0)

log = logging.getLogger("ta-ai")
logging.basicConfig(level=logging.INFO)

app = FastAPI(title="TA AI service", version="0.1.0")
_sem = threading.BoundedSemaphore(MAX_CONCURRENCY)


class CandleIn(BaseModel):
    ts: str
    open: float
    high: float
    low: float
    close: float
    volume: float


class AnalyzeRequest(BaseModel):
    symbol: str
    timeframe: str = "1h"
    candles: list[CandleIn]


def build_prompt(req: AnalyzeRequest) -> str:
    # Satu paragraf tanpa baris baru, supaya aman sebagai argumen command line di Windows.
    rows = " | ".join(
        f"{c.ts} O={c.open:g} H={c.high:g} L={c.low:g} C={c.close:g} V={c.volume:g}"
        for c in req.candles
    )
    # Sengaja tanpa tanda kutip ganda: lebih aman sebagai argumen command line di Windows.
    return (
        f"Kamu analis pasar. Instrument: {req.symbol}, timeframe {req.timeframe}, "
        f"{len(req.candles)} candle terakhir (waktu UTC, format O/H/L/C/V): {rows}. "
        "Analisis hanya berdasarkan data tersebut. "
        "Balas HANYA dengan satu objek JSON valid, tanpa teks lain dan tanpa markdown, "
        "dengan kunci: summary (ringkasan analisis teknikal Bahasa Indonesia, maksimal 80 kata), "
        "signal (salah satu dari BUY, SELL, HOLD), "
        "support (angka, level support terdekat), resistance (angka, level resistance terdekat). "
        "Angka support dan resistance ditulis polos tanpa pemisah ribuan, titik untuk desimal."
    )


def _num(v, lo: float, hi: float):
    """Ubah nilai JSON (angka, atau teks seperti 'Rp 4.350' / '81,200.5') menjadi float.
    Teks dengan format ribuan yang ambigu dicoba beberapa tafsiran, lalu dipilih yang masuk
    rentang [lo, hi] di sekitar harga terakhir. None kalau tidak ada yang masuk akal."""
    if isinstance(v, bool) or v is None:
        return None
    if isinstance(v, (int, float)):
        return float(v)
    if not isinstance(v, str):
        return None
    s = re.sub(r"[^\d.,]", "", v)
    tries = [
        s.replace(",", ""),                     # 81,200.5 -> 81200.5 (gaya Inggris)
        s.replace(".", "").replace(",", "."),   # 4.350,5  -> 4350.5  (gaya Indonesia)
    ]
    vals = []
    for t in tries:
        try:
            vals.append(float(t))
        except ValueError:
            pass
    for x in vals:
        if lo <= x <= hi:
            return x
    return vals[0] if vals else None


def parse_output(text: str, last_close: float) -> dict:
    """Pisahkan output Hermes menjadi summary, signal, support, resistance.
    Kalau JSON gagal dibaca, pakai cadangan: teks apa adanya + pola SIGNAL: lama."""
    out = {"content": text, "signal": "", "support": None, "resistance": None, "format_ok": False}
    lo, hi = last_close * 0.5, last_close * 1.5
    m = re.search(r"\{.*\}", text, flags=re.DOTALL)
    if m:
        try:
            data = json.loads(m.group(0))
            out["format_ok"] = True
            out["content"] = str(data.get("summary") or "").strip() or text
            sig = str(data.get("signal") or "").strip().upper()
            out["signal"] = sig if sig in ("BUY", "SELL", "HOLD") else ""
            out["support"] = _num(data.get("support"), lo, hi)
            out["resistance"] = _num(data.get("resistance"), lo, hi)
        except ValueError:
            pass
    if not out["signal"]:
        signals = re.findall(r"SIGNAL:\s*(BUY|SELL|HOLD)", text, flags=re.IGNORECASE)
        out["signal"] = signals[-1].upper() if signals else ""
    if not out["format_ok"]:
        out["content"] = re.sub(r"\n?\s*SIGNAL:\s*(BUY|SELL|HOLD)\s*$", "", text, flags=re.IGNORECASE).strip()
        log.warning("output Hermes bukan JSON, support/resistance kosong")

    # Level yang tidak masuk akal dibuang (bukan diperbaiki), supaya UI tidak menampilkan angka salah.
    s, r = out["support"], out["resistance"]
    if s is not None and not (0 < s and lo <= s <= hi):
        out["support"] = None
    if r is not None and not (0 < r and lo <= r <= hi):
        out["resistance"] = None
    if out["support"] is not None and out["resistance"] is not None and out["support"] >= out["resistance"]:
        out["support"] = out["resistance"] = None
    if (s, r) != (out["support"], out["resistance"]):
        log.warning("support/resistance dibuang karena tidak valid: %s / %s (close %s)", s, r, last_close)
    return out


def _pick(d: dict, *names: str) -> int:
    for n in names:
        v = d.get(n)
        if isinstance(v, (int, float)):
            return int(v)
    return 0


def summarize_usage(usage: dict):
    """Kembalikan (tokens_in, tokens_out, cache_read_tokens, cost_usd, model).
    Token = panggilan utama + panggilan auxiliary (mis. title_generation) yang ikut ditagih.
    Biaya diambil dari total_including_auxiliary."""
    aux = usage.get("auxiliary") or {}
    total = usage.get("total_including_auxiliary") or {}
    tokens_in = _pick(usage, "input_tokens") + _pick(aux, "input_tokens")
    tokens_out = _pick(usage, "output_tokens") + _pick(aux, "output_tokens")
    cache_read = _pick(usage, "cache_read_tokens") + _pick(aux, "cache_read_tokens")
    cost = total.get("estimated_cost_usd")
    if cost is None:
        cost = usage.get("estimated_cost_usd")
    model = usage.get("model") or HERMES_MODEL or "(default hermes)"
    if tokens_in == 0:
        log.warning("tokens_in = 0, isi usage file: %s", json.dumps(usage)[:1000])
    log.info("api_calls=%s total_tokens=%s", total.get("api_calls"), total.get("total_tokens"))
    return tokens_in, tokens_out, cache_read, float(cost or 0), str(model)


def equivalent_cost(tokens_in: int, tokens_out: int, cache_read: int, reported: float) -> float:
    """Biaya setara = token x harga resmi per juta token. Asumsi: tokens_in TIDAK termasuk
    cache_read (dilaporkan terpisah); verifikasi dengan data mentah sebelum baseline resmi."""
    if PRICE_IN > 0 or PRICE_OUT > 0 or PRICE_CACHE > 0:
        return (tokens_in * PRICE_IN + tokens_out * PRICE_OUT + cache_read * PRICE_CACHE) / 1e6
    return reported


def run_hermes(prompt: str, last_close: float) -> dict:
    with tempfile.TemporaryDirectory() as td:
        usage_path = os.path.join(td, "usage.json")
        cmd = [
            HERMES_BIN, "-z", prompt,
            "-t", HERMES_TOOLSETS,
            "--reasoning", HERMES_REASONING,
            "--usage-file", usage_path,
        ]
        if HERMES_MODEL:
            cmd += ["-m", HERMES_MODEL]
        if HERMES_PROVIDER:
            cmd += ["--provider", HERMES_PROVIDER]
        try:
            with _sem:  # batasi jumlah proses Hermes yang jalan bersamaan
                proc = subprocess.run(
                    cmd, capture_output=True, text=True, encoding="utf-8",
                    errors="replace", timeout=HERMES_TIMEOUT,
                )
        except FileNotFoundError:
            raise HTTPException(500, f"HERMES_BIN tidak ditemukan: {HERMES_BIN}")
        except subprocess.TimeoutExpired:
            raise HTTPException(504, f"Hermes melewati timeout {HERMES_TIMEOUT} detik")
        if proc.returncode != 0:
            raise HTTPException(502, f"Hermes gagal ({proc.returncode}): {proc.stderr[-300:]}")

        usage = {}
        if os.path.exists(usage_path):
            try:
                with open(usage_path, encoding="utf-8") as f:
                    usage = json.load(f)
            except (OSError, ValueError):
                log.warning("usage file tidak bisa dibaca")

    parsed = parse_output(proc.stdout.strip(), last_close)

    tokens_in, tokens_out, cache_read, reported_cost, model = summarize_usage(usage)
    return {
        "content": parsed["content"],
        "signal": parsed["signal"],
        "support": parsed["support"],
        "resistance": parsed["resistance"],
        "format_ok": parsed["format_ok"],
        "model": model,
        "tokens_in": tokens_in,
        "tokens_out": tokens_out,
        "cache_read_tokens": cache_read,
        "cost_usd": equivalent_cost(tokens_in, tokens_out, cache_read, reported_cost),
        "prompt_version": PROMPT_VERSION,
    }


@app.get("/health")
def health():
    return {
        "status": "ok",
        "prompt_version": PROMPT_VERSION,
        "model": HERMES_MODEL or "(default hermes)",
        "provider": HERMES_PROVIDER or "(default hermes)",
        "toolsets": HERMES_TOOLSETS,
        "reasoning": HERMES_REASONING,
        "max_concurrency": MAX_CONCURRENCY,
    }


@app.post("/analyze")
async def analyze(req: AnalyzeRequest):
    if len(req.candles) < 3:
        raise HTTPException(422, "minimal 3 candle")
    # subprocess blocking dijalankan di thread, supaya event loop tetap hidup dan portable di Windows
    return await asyncio.to_thread(run_hermes, build_prompt(req), req.candles[-1].close)

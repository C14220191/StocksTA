"""TA AI service: FastAPI yang membungkus Hermes Agent (subprocess `hermes -z`).

Satu instrument dianalisis dengan tiga metode. Setiap metode adalah Hermes skill di
.hermes/skills/trading-analysis (repo ini) dan dipanggil terpisah:
    debate   -> ta-bull-bear-debate   Multi-Agent Debate (Bull vs Bear)
    mtf      -> ta-multi-timeframe    Structured Multi-Timeframe Technical Analysis
    grounded -> ta-grounded-brief     Grounded Research Brief + Devil's Advocate
Skill berisi proses dan format output; prompt dari service ini hanya berisi data, dibagi per
bagian [D1]..[D10] supaya model bisa menyebut sumber setiap angka.

Konfigurasi lewat environment (diisi dari run.env):
    HERMES_BIN              default "hermes"
    HERMES_MODEL            default "" (pakai model default Hermes)
    HERMES_PROVIDER         default "" (pakai provider default Hermes)
    HERMES_TOOLSETS         default "clarify"  (toolset terkecil, memangkas overhead token)
    HERMES_REASONING        default "none"
    HERMES_TIMEOUT          default 120 (detik, per proses Hermes)
    HERMES_MAX_CONCURRENCY  default 1 (jumlah proses Hermes yang boleh jalan bersamaan)
    HERMES_QUEUE_TIMEOUT    default 180 (detik maksimal menunggu giliran; lewat -> 503)
    HERMES_SKILL_MODE       default "preload": skill dimuat Hermes lewat -s (repo harus di-trust:
                            hermes skills trust <repo>); "inline": isi SKILL.md disisipkan ke prompt
    HERMES_MAX_TURNS        default 2 (batas iterasi tool per analisis; mencegah loop clarify)
    PROMPT_VERSION          default "hermes-v5" (naikkan setiap prompt atau skill diubah)
    PRICE_IN_PER_M, PRICE_OUT_PER_M, PRICE_CACHE_READ_PER_M
                            harga resmi model (USD per juta token) untuk biaya setara;
                            semuanya 0 = cost_usd 0 (model gratis).
"""
import asyncio
import json
import logging
import os
import re
import shutil
import subprocess
import tempfile
import threading
import time
from pathlib import Path

import json_repair
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

HERMES_BIN = os.environ.get("HERMES_BIN", "hermes")
HERMES_MODEL = os.environ.get("HERMES_MODEL", "")
HERMES_PROVIDER = os.environ.get("HERMES_PROVIDER", "")
HERMES_TOOLSETS = os.environ.get("HERMES_TOOLSETS", "clarify")
HERMES_REASONING = os.environ.get("HERMES_REASONING", "none")
HERMES_TIMEOUT = int(os.environ.get("HERMES_TIMEOUT", "120"))
MAX_CONCURRENCY = int(os.environ.get("HERMES_MAX_CONCURRENCY", "1"))
QUEUE_TIMEOUT = int(os.environ.get("HERMES_QUEUE_TIMEOUT", "180"))
SKILL_MODE = os.environ.get("HERMES_SKILL_MODE", "preload")
MAX_TURNS = int(os.environ.get("HERMES_MAX_TURNS", "2"))
PROMPT_VERSION = os.environ.get("PROMPT_VERSION", "hermes-v5")
PRICE_IN = float(os.environ.get("PRICE_IN_PER_M", "0") or 0)
PRICE_OUT = float(os.environ.get("PRICE_OUT_PER_M", "0") or 0)
PRICE_CACHE = float(os.environ.get("PRICE_CACHE_READ_PER_M", "0") or 0)

SKILLS_DIR = Path(__file__).resolve().parent.parent / ".hermes" / "skills" / "trading-analysis"

log = logging.getLogger("ta-ai")
logging.basicConfig(level=logging.INFO)

app = FastAPI(title="TA AI service", version="0.2.0")
_sem = threading.BoundedSemaphore(MAX_CONCURRENCY)
# Statistik antrean untuk /health (halaman admin): berapa proses Hermes sedang jalan / menunggu.
_stats_lock = threading.Lock()
_stats = {"running": 0, "waiting": 0, "completed": 0, "failed": 0, "rejected": 0}


def _bump(**delta):
    with _stats_lock:
        for k, v in delta.items():
            _stats[k] += v


# ---- metode (Hermes skills) ----

METHODS = {
    "debate": {"skill": "ta-bull-bear-debate", "name": "Multi-Agent Debate (Bull vs Bear)"},
    "mtf": {"skill": "ta-multi-timeframe", "name": "Structured Multi-Timeframe Technical Analysis"},
    "grounded": {"skill": "ta-grounded-brief", "name": "Grounded Research Brief + Devil's Advocate"},
}


def _skill_file(method: str) -> Path:
    return SKILLS_DIR / METHODS[method]["skill"] / "SKILL.md"


def skill_parts(method: str) -> tuple[dict, str]:
    """Frontmatter (hanya kunci tingkat atas yang sederhana) dan isi SKILL.md."""
    text = _skill_file(method).read_text(encoding="utf-8").replace("\r\n", "\n")  # checkout Windows: CRLF
    meta, body = {}, text
    m = re.match(r"^---\n(.*?)\n---\n(.*)$", text, flags=re.DOTALL)
    if m:
        body = m.group(2)
        for line in m.group(1).splitlines():
            k, sep, v = line.partition(":")
            if sep and not line.startswith(" "):
                meta[k.strip()] = v.strip().strip('"')
    return meta, body.strip()


# ---- request ----

class CandleIn(BaseModel):
    ts: str
    open: float
    high: float
    low: float
    close: float
    volume: float


class TimeframeIn(BaseModel):
    tf: str                      # 1D | 4H | 1H
    candles: list[CandleIn] = []  # candle tertutup terakhir (lama -> baru)
    n: int = 0                   # jumlah candle yang dipakai menghitung indikator
    ema20: float | None = None
    ema50: float | None = None
    ema200: float | None = None
    rsi14: float | None = None
    volume_avg20: float | None = None


class StatsIn(BaseModel):
    days: int = 0                # jumlah candle 1D tertutup
    last_close: float | None = None
    last_close_ts: str | None = None
    high_52w: float | None = None
    low_52w: float | None = None
    high_20d: float | None = None
    low_20d: float | None = None
    return_30d_pct: float | None = None
    return_1y_pct: float | None = None
    volatility_1h: float | None = None  # σ log return 14 candle 1h (sama dengan dynamic TTL)


class ContextIn(BaseModel):
    name: str = ""
    asset_type: str = ""         # stock | crypto | commodity
    timeframes: list[TimeframeIn] = []
    stats: StatsIn | None = None
    fundamentals: dict | None = None
    fundamentals_at: str | None = None


class AnalyzeRequest(BaseModel):
    symbol: str
    method: str
    timeframe: str = "1h"
    candles: list[CandleIn]
    # Harga terkini (price feed / close terbaru) saat request; None = tidak dikirim backend.
    last_price: float | None = None
    last_price_ts: str | None = None
    # Indikator teknikal yang dihitung backend dari candle 1h tertutup (Bab 3.3.1).
    indicators: dict[str, float | None] | None = None
    context: ContextIn | None = None


# ---- prompt data ----

NO_DATA = "TIDAK ADA DATA"
ASSET_LABEL = {"stock": "saham IDX", "crypto": "kripto", "commodity": "komoditas (perpetual futures)"}

# Urutan dan label indikator di prompt. Nilai None (data belum cukup) dilewati.
INDICATOR_LABELS = [
    ("rsi14", "RSI(14)"), ("ema20", "EMA(20)"), ("ema50", "EMA(50)"),
    ("macd", "MACD(12,26)"), ("macd_signal", "sinyal MACD(9)"), ("macd_hist", "histogram MACD"),
    ("bb_lower", "Bollinger(20,2) bawah"), ("bb_middle", "Bollinger tengah"), ("bb_upper", "Bollinger atas"),
    ("stoch_k", "Stochastic %K(14)"), ("stoch_d", "Stochastic %D(3)"),
]


def _n(v) -> str:
    return NO_DATA if v is None else f"{v:.6g}"


def _big(v) -> str:
    """Angka laporan keuangan dalam satuan yang mudah dibaca (bukan 6.05e+14)."""
    if v is None:
        return NO_DATA
    for div, unit in ((1e12, "triliun"), (1e9, "miliar"), (1e6, "juta")):
        if abs(v) >= div:
            return f"{v / div:.2f} {unit}"
    return f"{v:.6g}"


def _pct(v) -> str:
    return NO_DATA if v is None else f"{v:+.2f}%"


def _clean(s: str | None) -> str:
    """Teks dari sumber luar: satu baris, tanpa tanda kutip ganda (aman sebagai argumen command line)."""
    return re.sub(r"\s+", " ", (s or "").replace('"', "'")).strip()


def _tf(ctx: ContextIn | None, tf: str) -> TimeframeIn | None:
    for t in (ctx.timeframes if ctx else []):
        if t.tf.upper() == tf:
            return t
    return None


def format_indicators(ind: dict | None) -> str:
    if not ind:
        return NO_DATA
    parts = [f"{label}={ind[k]:.6g}" for k, label in INDICATOR_LABELS if ind.get(k) is not None]
    return ", ".join(parts) if parts else NO_DATA


def d1_profile(req: AnalyzeRequest) -> str:
    ctx = req.context
    f = (ctx.fundamentals if ctx else None) or {}
    if ctx and ctx.asset_type == "stock" and (f.get("sector") or f.get("business_summary")):
        return (f"[D1] Profil (Yahoo Finance): sektor {_clean(f.get('sector')) or NO_DATA}, "
                f"industri {_clean(f.get('industry')) or NO_DATA}. Ringkasan bisnis (bahasa Inggris): "
                f"{_clean(f.get('business_summary')) or NO_DATA}")
    kind = ASSET_LABEL.get(ctx.asset_type if ctx else "", "instrumen")
    return f"[D1] Profil: {NO_DATA} profil bisnis ({kind}, tanpa laporan keuangan)"


def d2_valuation(req: AnalyzeRequest) -> str:
    ctx = req.context
    v = ((ctx.fundamentals if ctx else None) or {}).get("valuation")
    if not v:
        return f"[D2] Valuasi: {NO_DATA}"
    cur = (ctx.fundamentals or {}).get("currency") or ""
    at = f", diambil {ctx.fundamentals_at[:10]}" if ctx.fundamentals_at else ""
    return (f"[D2] Valuasi (Yahoo Finance{at}): PER {_n(v.get('per'))}, PBV {_n(v.get('pbv'))}, "
            f"DER total utang/ekuitas {_n(v.get('der'))}, kapitalisasi pasar {_big(v.get('market_cap'))} {cur}, "
            f"EPS TTM {_n(v.get('eps_ttm'))}, nilai buku per saham {_n(v.get('book_value_per_share'))}")


def d3_financials(req: AnalyzeRequest) -> str:
    ctx = req.context
    f = (ctx.fundamentals if ctx else None) or {}
    annual = f.get("annual") or []
    if not annual:
        return f"[D3] Laporan keuangan tahunan: {NO_DATA}"
    rows = "; ".join(
        f"{a.get('year')}: revenue {_big(a.get('revenue'))}, laba bersih {_big(a.get('net_income'))}, "
        f"free cash flow {_big(a.get('free_cash_flow'))}, total utang {_big(a.get('total_debt'))}, "
        f"ekuitas {_big(a.get('equity'))}"
        for a in annual)
    return f"[D3] Laporan keuangan tahunan (Yahoo Finance, {f.get('currency') or ''}): {rows}"


def d4_daily(req: AnalyzeRequest) -> str:
    ctx = req.context
    s = ctx.stats if ctx else None
    if not s or not s.days:
        return f"[D4] Statistik harian: {NO_DATA}"
    d = _tf(ctx, "1D")
    vol = NO_DATA
    if d and d.candles and d.volume_avg20:
        vol = f"{d.candles[-1].volume / d.volume_avg20:.2f}x rata-rata 20 hari"
    return (f"[D4] Statistik harian ({s.days} candle 1D tertutup): close terakhir {_n(s.last_close)} "
            f"({(s.last_close_ts or '')[:10]}), tertinggi/terendah 52 minggu {_n(s.high_52w)}/{_n(s.low_52w)}, "
            f"tertinggi/terendah 20 hari {_n(s.high_20d)}/{_n(s.low_20d)}, perubahan 30 hari {_pct(s.return_30d_pct)}, "
            f"perubahan 1 tahun {_pct(s.return_1y_pct)}, EMA50 1D {_n(d.ema50 if d else None)}, "
            f"EMA200 1D {_n(d.ema200 if d else None)}, RSI14 1D {_n(d.rsi14 if d else None)}, "
            f"volume hari terakhir {vol}, volatilitas σ log return 14 candle 1H {_n(s.volatility_1h)}")


def d5_price(req: AnalyzeRequest) -> str:
    if req.last_price:
        return f"[D5] Harga terkini {req.last_price:g} pada {req.last_price_ts or 'saat ini'}"
    return f"[D5] Harga terkini {req.candles[-1].close:g} (close candle 1H terakhir)"


def d6_hourly(req: AnalyzeRequest) -> str:
    return f"[D6] Indikator 1H pada candle tertutup terakhir: {format_indicators(req.indicators)}"


def d_timeframe(req: AnalyzeRequest, tf: str, code: str) -> str:
    t = _tf(req.context, tf)
    if not t or not t.candles:
        return f"[{code}] Candle {tf}: {NO_DATA}"
    rows = " | ".join(f"{c.ts[:16]} {c.open:g}/{c.high:g}/{c.low:g}/{c.close:g}/{c.volume:g}" for c in t.candles)
    ema200 = _n(t.ema200) if t.ema200 is not None else f"{NO_DATA} (hanya {t.n} candle)"
    return (f"[{code}] Candle {tf} {len(t.candles)} terakhir tertutup (O/H/L/C/V): {rows}; "
            f"EMA20 {_n(t.ema20)}, EMA50 {_n(t.ema50)}, EMA200 {ema200}, RSI14 {_n(t.rsi14)}, "
            f"volume rata-rata 20 {_n(t.volume_avg20)}")


D10 = f"[D10] Berita dan sentimen: {NO_DATA} (sistem tidak mengambil berita)"


def dominant_view(req: AnalyzeRequest) -> str:
    """Pandangan yang diuji metode grounded: arah yang tersirat dari data teknikal (aturan tetap,
    bukan dari model), menggantikan thesis yang di metode aslinya ditulis pengguna."""
    price = req.last_price or req.candles[-1].close
    d = _tf(req.context, "1D")
    ind = req.indicators or {}
    checks = []
    if d and d.ema50:
        checks.append((price > d.ema50, "harga di atas EMA50 harian", "harga di bawah EMA50 harian"))
    if d and d.ema50 and d.ema200:
        checks.append((d.ema50 > d.ema200, "EMA50 harian di atas EMA200", "EMA50 harian di bawah EMA200"))
    if ind.get("macd_hist") is not None:
        checks.append((ind["macd_hist"] > 0, "histogram MACD 1H positif", "histogram MACD 1H negatif"))
    if ind.get("rsi14") is not None:
        checks.append((ind["rsi14"] > 50, "RSI 1H di atas 50", "RSI 1H di bawah 50"))
    if not checks:
        return "PANDANGAN DOMINAN: NETRAL (data teknikal tidak cukup)"
    up = sum(1 for ok, _, _ in checks if ok)
    pro = [a for ok, a, _ in checks if ok]
    con = [b for ok, _, b in checks if not ok]
    if up * 4 >= len(checks) * 3:  # >= 75% pemeriksaan searah
        label, why, against = "BULLISH, harga cenderung naik", pro, con
    elif up * 4 <= len(checks):
        label, why, against = "BEARISH, harga cenderung turun", con, pro
    else:
        label, why, against = "NETRAL, sinyal campuran", pro + con, []
    text = f"PANDANGAN DOMINAN (dibuat otomatis dari data teknikal, bukan saran): {label}, karena {', '.join(why)}"
    return text + (f"; yang berlawanan: {', '.join(against)}" if against else "")


SECTIONS = {
    "debate": [d1_profile, d2_valuation, d3_financials, d4_daily, d5_price, d6_hourly, lambda r: D10],
    "mtf": [d5_price,
            lambda r: d_timeframe(r, "1D", "D7"), lambda r: d_timeframe(r, "4H", "D8"),
            lambda r: d_timeframe(r, "1H", "D9")],
    "grounded": [d1_profile, d2_valuation, d3_financials, d4_daily, d5_price, d6_hourly, lambda r: D10,
                 dominant_view],
}


def build_prompt(req: AnalyzeRequest) -> str:
    m = METHODS[req.method]
    ctx = req.context
    name = _clean(ctx.name) if ctx and ctx.name else req.symbol
    kind = ASSET_LABEL.get(ctx.asset_type if ctx else "", "instrumen")
    parts = [f"Gunakan skill {m['skill']} untuk instrumen {req.symbol} ({name}, {kind}). Semua waktu dalam UTC."]
    if SKILL_MODE == "inline":
        parts.insert(0, "INSTRUKSI METODE: " + re.sub(r"\s+", " ", skill_parts(req.method)[1]))
    parts += [f(req) for f in SECTIONS[req.method]]
    # Bahasa ditegaskan di akhir: ringkasan bisnis Yahoo berbahasa Inggris membuat model ikut menjawab
    # dalam bahasa Inggris (uji 4 Nov, BBCA).
    parts.append("Jangan memanggil tool apa pun dan jangan bertanya balik; kalau data kurang, tulis "
                 "TIDAK ADA DATA dan tetap jawab. Semua teks di dalam JSON WAJIB berbahasa Indonesia, "
                 "walaupun sebagian data berbahasa Inggris. Balas HANYA satu objek JSON sesuai format output skill.")
    return " || ".join(parts)


# ---- parsing output ----

BIAS = {"bullish": "bullish", "bearish": "bearish", "neutral": "neutral", "netral": "neutral",
        "naik": "bullish", "turun": "bearish"}
SIGNAL_OF_BIAS = {"bullish": "BUY", "bearish": "SELL", "neutral": "HOLD"}
# Istilah ajakan transaksi yang dilarang skill; kemunculannya dicatat di detail.advice_terms.
ADVICE_RE = re.compile(r"stop[ -]?loss|take[ -]?profit|risk ?/ ?reward|\bentry\b|beli sekarang|jual sekarang|"
                       r"segera (?:beli|jual)|(?:buka|masuk|tambah) posisi|ukuran posisi", re.IGNORECASE)


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


def _level(v, lo: float, hi: float):
    x = _num(v, lo, hi)
    return x if x is not None and x > 0 and lo <= x <= hi else None


def _bias(v) -> str:
    return BIAS.get(str(v or "").strip().lower(), "")


def _conf(v):
    try:
        x = int(round(float(v)))
    except (TypeError, ValueError):
        return None
    return min(max(x, 0), 100)


def _text(v, n: int = 600) -> str:
    return "" if v is None else str(v).strip()[:n]


def _texts(v, n: int, each: int = 400) -> list[str]:
    if not isinstance(v, list):
        return []
    return [t for t in (_text(x, each) for x in v) if t][:n]


def _bool(v):
    if isinstance(v, bool):
        return v
    if isinstance(v, str) and v.strip().lower() in ("true", "ya", "yes"):
        return True
    if isinstance(v, str) and v.strip().lower() in ("false", "tidak", "no"):
        return False
    return None


def normalize_debate(d: dict) -> dict:
    analysts = []
    by_role = {str(a.get("role", "")).lower(): a for a in d.get("analysts") or [] if isinstance(a, dict)}
    for role in ("fundamental", "technical", "sentiment"):
        a = by_role.get(role) or {}
        analysts.append({"role": role, "signal": _bias(a.get("signal")) or "neutral",
                         "confidence": _conf(a.get("confidence")), "reason": _text(a.get("reason"), 300)})
    rounds = []
    for i, r in enumerate(x for x in d.get("debate") or [] if isinstance(x, dict)):
        rounds.append({"round": _conf(r.get("round")) or i + 1,
                       "bull": _text(r.get("bull"), 400), "bear": _text(r.get("bear"), 400)})
    risk = d.get("risk")
    if not isinstance(risk, dict):
        risk = {"worst_case": risk}
    # model kecil kadang menaruh worst_case / volatility di luar objek risk
    worst = risk.get("worst_case") or d.get("worst_case")
    volatility = risk.get("volatility") or d.get("volatility")
    return {
        "analysts": analysts,
        "debate": rounds[:3],
        "risk": {"worst_case": _text(worst, 400), "volatility": _text(volatility, 300)},
        "change_conditions": _texts(d.get("change_conditions"), 4, 250),
        "missing_data": _texts(d.get("missing_data"), 6, 200),
    }


TREND = {"naik": "naik", "turun": "turun", "sideways": "sideways", "up": "naik", "down": "turun"}
FACTORS = ("trend", "structure", "level", "rsi", "volume")


def normalize_mtf(d: dict, lo: float, hi: float) -> dict:
    tfs, seen = [], set()
    for t in d.get("timeframes") or []:
        if not isinstance(t, dict):
            continue
        tf = str(t.get("tf", "")).upper()
        # timeframe karangan model (60M, 15M, ...) dibuang; tf yang berulang hanya diambil yang pertama
        if tf not in ("1D", "4H", "1H") or tf in seen:
            continue
        seen.add(tf)
        tfs.append({"tf": tf, "trend": TREND.get(str(t.get("trend", "")).lower(), _text(t.get("trend"), 20)),
                    "structure": _text(t.get("structure"), 20),
                    "aligned": True if tf == "1D" else _bool(t.get("aligned")),
                    "note": _text(t.get("note"), 250)})
    c = d.get("confluence") if isinstance(d.get("confluence"), dict) else {}
    factors = {k: _bool(c.get(k)) for k in FACTORS}
    if all(v is not None for v in factors.values()):
        score = sum(1 for v in factors.values() if v)  # konsisten dengan faktor yang dicentang
    else:
        score = min(max(_conf(c.get("score")) or 0, 0), 5)
    sc = d.get("scenarios") if isinstance(d.get("scenarios"), dict) else {}
    return {
        "timeframes": tfs,
        "confluence": {"score": score, **factors},
        # Aturan metode ditegakkan di sini, bukan dipercayakan ke model: setup jelas hanya kalau score >= 4.
        "setup": "jelas" if score >= 4 else "tidak_jelas",
        "scenarios": {k: _text(sc.get(k), 300) for k in ("bullish", "bearish", "invalidation")},
        "invalidation_level": _level(d.get("invalidation_level"), lo, hi),
    }


TREND_METRIC = {"naik": "naik", "turun": "turun", "stabil": "stabil"}


def normalize_grounded(d: dict) -> dict:
    metrics = []
    for m in d.get("metrics") or []:
        if isinstance(m, dict) and _text(m.get("name")):
            trend = str(m.get("trend", "")).strip().lower()
            metrics.append({"name": _text(m.get("name"), 80), "value": _text(m.get("value"), 80),
                            "trend": TREND_METRIC.get(trend, NO_DATA), "source": _text(m.get("source"), 20)})
    return {
        "thesis": _text(d.get("thesis"), 400),
        "brief": _text(d.get("brief"), 700),
        "metrics": metrics[:8],
        "red_flags": _texts(d.get("red_flags"), 4, 300),
        "weaknesses": _texts(d.get("weaknesses"), 5, 300),
        "falsifiers": _texts(d.get("falsifiers"), 3, 300),
        "thesis_holds": _bool(d.get("thesis_holds")),
    }


def _comma_after_close(out: list[str]) -> int:
    """Indeks '}' di out kalau out berakhir dengan '}' lalu ',' (spasi diabaikan), selain itu -1."""
    j = len(out) - 1
    while j >= 0 and out[j].isspace():
        j -= 1
    if j < 0 or out[j] != ",":
        return -1
    j -= 1
    while j >= 0 and out[j].isspace():
        j -= 1
    return j if j >= 0 and out[j] == "}" else -1


def _close_arrays(s: str) -> str:
    """Perbaiki dua kesalahan struktur yang sering dibuat model kecil:
    1. Lupa menutup array sebelum kunci berikutnya, mis. {"timeframes":[{...},{...},"confluence":{...}}.
       Kunci objek yang muncul langsung di dalam array berarti array itu seharusnya sudah ditutup:
       sisipkan ']' sebelum kunci tersebut.
    2. Memecah satu objek menjadi beberapa, mis. "scenarios":{"bullish":".."},{"bearish":".."}
       (pilot 5 Nov, mtf ANTM). '{' di posisi kunci objek tidak sah: '},{' digabung kembali menjadi ','."""
    out, stack, i, n = [], [], 0, len(s)
    while i < n:
        ch = s[i]
        if ch == '"':
            j, esc = i + 1, False
            while j < n and (esc or s[j] != '"'):
                esc = not esc and s[j] == "\\"
                j += 1
            k = j + 1
            while k < n and s[k] in " \t\r\n":
                k += 1
            if stack and stack[-1] == "[" and k < n and s[k] == ":":
                while out and out[-1] in " \t\r\n,":
                    out.pop()
                out.append("],")
                stack.pop()
            out.append(s[i:j + 1])
            i = j + 1
            continue
        if ch == "{" and stack and stack[-1] == "{" and (k := _comma_after_close(out)) >= 0:
            del out[k]           # buang '}' yang menutup objek sebelumnya, koma dipertahankan
            stack.append("{")    # objek itu dibuka lagi; '{' ini sendiri tidak ditulis
            i += 1
            continue
        if ch in "[{":
            stack.append(ch)
        elif ch in "]}" and stack:
            stack.pop()
        out.append(ch)
        i += 1
    return "".join(out)


def _has_conclusion(d) -> bool:
    return isinstance(d, dict) and ("summary" in d or "bias" in d)


def _hoist_conclusion(data: dict) -> bool:
    """Model kecil kadang menulis '},{"confluence":...' alih-alih '}],"confluence":...', sehingga seluruh
    bagian penutup jawaban (confluence ... bias, summary) menjadi elemen terakhir array timeframes
    (pilot 5 Nov, mtf). Elemen array yang membawa kesimpulan dipindah kembali ke tingkat atas.
    True kalau ada yang dipindah."""
    if _has_conclusion(data):
        return False
    for key, val in list(data.items()):
        if not isinstance(val, list):
            continue
        for i, el in enumerate(val):
            if _has_conclusion(el):
                data[key] = val[:i] + val[i + 1:]
                for k, v in el.items():
                    data.setdefault(k, v)
                return True
    return False


def _load_json(text: str) -> tuple[dict | None, bool]:
    """Objek JSON dari output model. Kalau json.loads gagal (model kecil sering menulis kutip ganda
    berlebih, lupa menutup array, memecah objek, atau berhenti di tengah), dicoba perbaikan:
    _close_arrays lalu json_repair. Hasil hanya diterima kalau berupa objek dengan kunci format skill
    (summary / bias), setelah kesimpulan yang tersesat ke dalam array dipindah ke tingkat atas.
    Kembalian: (data, diperbaiki?)."""
    start = text.find("{")
    if start < 0:
        return None, False
    end = text.rfind("}")
    strict = text[start:end + 1] if end > start else text[start:]
    try:
        data = json.loads(strict)
        if not isinstance(data, dict):
            return None, False
        return data, _hoist_conclusion(data)
    except ValueError:
        pass
    # Perbaikan dicoba dulu pada seluruh teks sejak '{': model kecil kadang berhenti tanpa '}' penutup,
    # sehingga memotong di '}' terakhir justru membuang kunci kesimpulan (bias, summary) di ekor jawaban
    # (uji 5 Nov, debate BBCA).
    for candidate in (_close_arrays(text[start:].strip()), _close_arrays(strict)):
        try:
            data = json.loads(candidate)
        except ValueError:
            try:
                data = json_repair.loads(candidate)
            except Exception:  # noqa: BLE001 - library pihak ketiga, apa pun gagalnya dianggap bukan JSON
                continue
        if isinstance(data, list):  # objek terpotong jadi beberapa bagian: ambil yang membawa kesimpulan
            data = next((d for d in data if _has_conclusion(d)), None)
        if isinstance(data, dict):
            _hoist_conclusion(data)
            if _has_conclusion(data):
                return data, True
    return None, False


def parse_output(text: str, ref_price: float, method: str) -> dict:
    """Pisahkan output Hermes menjadi kolom umum (summary, bias, confidence, support, resistance)
    dan detail terstruktur per metode. Kalau JSON gagal dibaca, teks apa adanya disimpan."""
    out = {"content": text[:6000], "signal": "", "confidence": None, "support": None, "resistance": None,
           "detail": None, "format_ok": False}
    lo, hi = ref_price * 0.5, ref_price * 1.5
    data, repaired = _load_json(text)
    if data is None:
        log.warning("output Hermes (%s) bukan JSON: %s", method, text[:200])
        return out

    bias = _bias(data.get("bias"))
    summary = _text(data.get("summary"), 800)
    if not bias or not summary:
        # JSON terbaca tapi tanpa kesimpulan: tidak memenuhi format skill, diperlakukan sebagai gagal format
        log.warning("output Hermes (%s) tanpa bias/summary: %s", method, text[:200])
        return out
    detail = {"debate": normalize_debate, "grounded": normalize_grounded}.get(method)
    detail = detail(data) if detail else normalize_mtf(data, lo, hi)
    detail["bias"] = bias or None
    if repaired:
        detail["json_repaired"] = True  # dihitung terpisah dari JSON yang valid sejak awal
    terms = sorted({t.lower() for t in ADVICE_RE.findall(json.dumps(data, ensure_ascii=False))})
    if terms:
        detail["advice_terms"] = terms
        log.warning("output %s memuat istilah ajakan transaksi: %s", method, terms)

    out.update(format_ok=True, detail=detail, signal=SIGNAL_OF_BIAS[bias],
               confidence=_conf(data.get("confidence")), content=summary,
               support=_level(data.get("support"), lo, hi), resistance=_level(data.get("resistance"), lo, hi))
    # Level yang tidak masuk akal dibuang (bukan diperbaiki), supaya UI tidak menampilkan angka salah.
    if out["support"] is not None and out["resistance"] is not None and out["support"] >= out["resistance"]:
        out["support"] = out["resistance"] = None
    return out


# ---- usage dan biaya ----

def parse_stream(stdout: str) -> dict:
    """Event JSONL dari `hermes chat --format stream-json`: system/init (model), text (jawaban),
    event tool (model memanggil tool), dan result (token, exit code). Contoh result:
    {"type": "result", "exit_code": 0, "text": "...", "tokens": {"input": 1116, "output": 2,
     "total": 2718, "cache_read": 1600, "cache_write": 0}, "duration_ms": 3865}
    tokens.input TIDAK termasuk cache_read (input + output + cache_read = total)."""
    ev = {"model": None, "session_id": None, "texts": [], "result": None, "tool_events": 0}
    for line in stdout.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            e = json.loads(line)
        except ValueError:
            continue
        kind = str(e.get("type") or "")
        if kind == "system":
            ev["model"], ev["session_id"] = e.get("model"), e.get("session_id")
        elif kind == "text":
            ev["texts"].append(e.get("text") or "")
        elif kind == "result":
            ev["result"] = e
        elif "tool" in kind:
            ev["tool_events"] += 1
    return ev


def equivalent_cost(tokens_in: int, tokens_out: int, cache_read: int) -> float:
    """Biaya setara = token x harga resmi per juta token (stream-json Hermes tidak membawa biaya;
    model gratis = 0). tokens_in tidak termasuk cache_read, yang dihargai terpisah."""
    return (tokens_in * PRICE_IN + tokens_out * PRICE_OUT + cache_read * PRICE_CACHE) / 1e6


# ---- Hermes ----

def run_hermes(prompt: str, ref_price: float, method: str) -> dict:
    # Antrean dibatasi waktunya supaya saat load test (k6) request tidak menumpuk tanpa batas.
    t0 = time.monotonic()
    _bump(waiting=1)
    got = _sem.acquire(timeout=QUEUE_TIMEOUT)
    _bump(waiting=-1)
    if not got:
        _bump(rejected=1)
        raise HTTPException(503, f"antrean Hermes penuh lebih dari {QUEUE_TIMEOUT} detik")
    queue_ms = int((time.monotonic() - t0) * 1000)
    _bump(running=1)
    ok = False
    try:
        t1 = time.monotonic()
        out = _run_hermes(prompt, ref_price, method)
        out["exec_ms"] = int((time.monotonic() - t1) * 1000)
        out["queue_ms"] = queue_ms
        ok = True
        return out
    finally:
        _sem.release()
        _bump(running=-1, completed=1 if ok else 0, failed=0 if ok else 1)


def hermes_command(prompt_path: str, method: str) -> list[str]:
    cmd = [HERMES_BIN, "-t", HERMES_TOOLSETS, "--reasoning", HERMES_REASONING]
    if SKILL_MODE == "preload":
        cmd += ["-s", METHODS[method]["skill"]]
    if HERMES_MODEL:
        cmd += ["-m", HERMES_MODEL]
    if HERMES_PROVIDER:
        cmd += ["--provider", HERMES_PROVIDER]
    # `chat --oneshot`, bukan `-z`: di jalur -z batas iterasi tool tidak terbatas, sehingga model kecil
    # bisa memanggil clarify puluhan kali (uji 4 Nov: 47 panggilan, 211 ribu token dalam 150 detik).
    # Prompt dibaca dari file (bebas batas panjang dan aturan kutip command line), token dibaca dari
    # event result stream-json.
    return cmd + ["chat", "--query-file", prompt_path, "--oneshot", "--format", "stream-json",
                  "--max-turns", str(MAX_TURNS), "--run-budget", str(max(HERMES_TIMEOUT - 10, 10))]


def _run_hermes(prompt: str, ref_price: float, method: str) -> dict:
    with tempfile.TemporaryDirectory() as td:
        prompt_path = os.path.join(td, "prompt.txt")
        with open(prompt_path, "w", encoding="utf-8") as f:
            f.write(prompt)
        try:
            # cwd = root repo ini, supaya Hermes menemukan project skills (.hermes/skills)
            proc = subprocess.run(
                hermes_command(prompt_path, method), capture_output=True, text=True, encoding="utf-8",
                errors="replace", timeout=HERMES_TIMEOUT, cwd=SKILLS_DIR.parent.parent.parent,
            )
        except FileNotFoundError:
            raise HTTPException(500, f"HERMES_BIN tidak ditemukan: {HERMES_BIN}")
        except subprocess.TimeoutExpired:
            raise HTTPException(504, f"Hermes melewati timeout {HERMES_TIMEOUT} detik")

    ev = parse_stream(proc.stdout)
    res = ev["result"] or {}
    if proc.returncode != 0 or res.get("exit_code") not in (0, None) or not res:
        raise HTTPException(502, f"Hermes gagal ({proc.returncode}): {(proc.stderr or proc.stdout)[-300:]}")
    if ev["tool_events"]:
        log.warning("%s: model memanggil tool (%s event) walau dilarang skill", method, ev["tool_events"])

    tokens = res.get("tokens") or {}
    tokens_in, tokens_out = int(tokens.get("input") or 0), int(tokens.get("output") or 0)
    cache_read = int(tokens.get("cache_read") or 0)
    text = (res.get("text") or "".join(ev["texts"])).strip()
    return {
        **parse_output(text, ref_price, method),
        "method": method,
        "skill": METHODS[method]["skill"],
        "tool_events": ev["tool_events"],
        "model": ev["model"] or HERMES_MODEL or "(default hermes)",
        "tokens_in": tokens_in,
        "tokens_out": tokens_out,
        "cache_read_tokens": cache_read,
        "cost_usd": equivalent_cost(tokens_in, tokens_out, cache_read),
        "prompt_version": PROMPT_VERSION,
    }


# ---- endpoints ----

def _skills_status() -> list[dict]:
    out = []
    for method, m in METHODS.items():
        f = _skill_file(method)
        meta = skill_parts(method)[0] if f.exists() else {}
        out.append({"method": method, "skill": m["skill"], "name": m["name"],
                    "version": meta.get("version"), "file_ok": f.exists()})
    return out


@app.get("/health")
def health():
    hermes_found = shutil.which(HERMES_BIN) is not None
    skills = _skills_status()
    ok = hermes_found and all(s["file_ok"] for s in skills)
    return {
        "status": "ok" if ok else "degraded",
        "hermes_bin": HERMES_BIN if hermes_found else f"TIDAK DITEMUKAN: {HERMES_BIN}",
        "prompt_version": PROMPT_VERSION,
        "model": HERMES_MODEL or "(default hermes)",
        "provider": HERMES_PROVIDER or "(default hermes)",
        "toolsets": HERMES_TOOLSETS,
        "reasoning": HERMES_REASONING,
        "skill_mode": SKILL_MODE,
        "skills": skills,
        "max_concurrency": MAX_CONCURRENCY,
        "queue_timeout": QUEUE_TIMEOUT,
        "timeout": HERMES_TIMEOUT,
        "queue": dict(_stats),  # sejak service start
    }


# Bagian data yang diisi ke prompt (untuk ditampilkan di halaman admin Prompt).
PROMPT_VARIABLES = [
    {"name": "[D1] profil", "desc": "Sektor, industri, ringkasan bisnis (saham, Yahoo Finance)"},
    {"name": "[D2] valuasi", "desc": "PER, PBV, DER, kapitalisasi pasar, EPS (saham)"},
    {"name": "[D3] laporan keuangan", "desc": "Revenue, laba bersih, FCF, utang, ekuitas 3 tahun (saham)"},
    {"name": "[D4] statistik harian", "desc": "52 minggu, 20 hari, perubahan 30 hari/1 tahun, EMA50/200 dan RSI 1D, volatilitas σ 1H"},
    {"name": "[D5] harga terkini", "desc": "Price feed WebSocket, atau close 1H terbaru untuk saham"},
    {"name": "[D6] indikator 1H", "desc": "RSI(14), EMA(20/50), MACD(12,26,9), Bollinger(20,2), Stochastic(14,3)"},
    {"name": "[D7]-[D9] candle 1D/4H/1H", "desc": "12 candle tertutup + EMA20/50/200, RSI14, volume rata-rata 20 (metode mtf)"},
    {"name": "[D10] berita", "desc": "Selalu TIDAK ADA DATA (sistem tidak mengambil berita)"},
    {"name": "pandangan dominan", "desc": "Arah dari aturan teknikal tetap, diuji oleh metode grounded"},
]


def _check_method(method: str):
    if method not in METHODS:
        raise HTTPException(422, f"method harus salah satu dari {', '.join(METHODS)}")


@app.post("/prompt/preview")
def prompt_preview(req: AnalyzeRequest):
    """Render prompt persis seperti yang akan dikirim ke Hermes, TANPA memanggil model (tanpa token)."""
    _check_method(req.method)
    prompt = build_prompt(req)
    meta, body = skill_parts(req.method)
    return {
        "prompt_version": PROMPT_VERSION,
        "method": req.method,
        "skill": METHODS[req.method]["skill"],
        "skill_version": meta.get("version"),
        "skill_mode": SKILL_MODE,
        "skill_text": body,
        "prompt": prompt,
        "chars": len(prompt),
        "variables": PROMPT_VARIABLES,
    }


@app.post("/analyze")
async def analyze(req: AnalyzeRequest):
    _check_method(req.method)
    if len(req.candles) < 3:
        raise HTTPException(422, "minimal 3 candle")
    # subprocess blocking dijalankan di thread, supaya event loop tetap hidup dan portable di Windows
    ref_price = req.last_price or req.candles[-1].close  # rentang wajar support/resistance
    return await asyncio.to_thread(run_hermes, build_prompt(req), ref_price, req.method)

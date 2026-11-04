// Client untuk backend Go. Endpoint mengikuti dokumentasi "API untuk UI".
// URL backend dari .env (VITE_API_URL), lihat .env.example.
const BASE = (import.meta.env.VITE_API_URL || "http://127.0.0.1:8080").replace(/\/$/, "");

export class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.name = "ApiError";
    this.status = status; // 0 = backend tidak terjangkau
  }
}

const STATUS_MESSAGE = {
  404: "Instrumen tidak ditemukan.",
  422: "Data harga untuk instrumen ini belum tersedia.",
};

async function request(method, path, params = {}, signal) {
  const url = new URL(BASE + path);
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== "") url.searchParams.set(k, v);
  }

  let res;
  try {
    res = await fetch(url, { method, signal });
  } catch (e) {
    if (e.name === "AbortError") throw e;
    throw new ApiError(
      0,
      `Tidak bisa terhubung ke backend (${BASE}). Pastikan backend menyala dan origin UI ada di CORS_ORIGINS.`
    );
  }

  if (!res.ok) {
    let detail;
    try {
      detail = (await res.json()).error;
    } catch {
      /* body bukan JSON */
    }
    throw new ApiError(res.status, detail || STATUS_MESSAGE[res.status] || `Permintaan gagal (HTTP ${res.status}).`);
  }
  return res.json();
}

const get = (path, params, signal) => request("GET", path, params, signal);
const sym = (s) => encodeURIComponent(s);

// type: "all" | "stock" | "crypto"
export const getOverview = ({ type = "all", limit = 10, movers = 3 } = {}, signal) =>
  get("/api/market/overview", { type, limit, movers }, signal);

export const searchInstruments = (q, type = "all", signal) => get("/api/search", { q, type }, signal);

export const getInstrument = (symbol, signal) => get(`/api/instruments/${sym(symbol)}`, {}, signal);

// range: 1D|1W|1M|3M, tf: 1h|2h|4h|6h|1d (kosong = bawaan backend)
export const getCandles = (symbol, range, tf, signal) =>
  get(`/api/instruments/${sym(symbol)}/candles`, { range, tf }, signal);

// /api/analyze memanggil AI kalau cache MISS dan SETIAP panggilan dicatat di inference_log.
// Membatalkan fetch di browser tidak menghentikan proses di backend, jadi:
//  - tidak memakai AbortSignal,
//  - panggilan yang sedang berjalan untuk symbol yang sama dipakai bersama (dedupe).
// Dedupe ini juga mencegah double call dari React.StrictMode saat development,
// yang kalau tidak, akan menggandakan baris inference_log dan token cost.
// method: debate | mtf | grounded (lib/methods.js); satu kunjungan Detail = satu panggilan per metode.
const inflight = new Map();
export function analyzeSymbol(symbol, method) {
  const key = `${symbol}|${method}`;
  if (inflight.has(key)) return inflight.get(key);
  const p = get(`/api/analyze/${sym(symbol)}`, { method }).finally(() => inflight.delete(key));
  inflight.set(key, p);
  return p;
}

// ---- Admin (/admin) ----
// period: 24h|7d|30d, trigger: all|ui|k6|scheduled|manual
export const getAdminOverview = (params, signal) => get("/api/admin/overview", params, signal);
export const getAdminCache = (signal) => get("/api/admin/cache", {}, signal);
export const getAdminPrompt = (symbol, method, signal) => get("/api/admin/prompt", { symbol, method }, signal);
// days, trigger, prompt: current|all, method: all|debate|mtf|grounded
export const getAdminEvaluation = (params, signal) => get("/api/admin/evaluation", params, signal);
export const getAdminHealth = (signal) => get("/api/admin/health", {}, signal);
// method kosong = semua metode instrument tersebut
export const invalidateCache = (symbol, method) =>
  request("POST", `/api/admin/cache/${sym(symbol)}/invalidate`, { method });

// Client untuk backend Go. Endpoint mengikuti dokumentasi "API untuk UI".
// URL backend dari .env (VITE_API_URL), lihat .env.example.
import { clearAdminSession, getAdminToken } from "./adminAuth.js";

const BASE = (import.meta.env.VITE_API_URL || "http://127.0.0.1:8080").replace(/\/$/, "");
const LOGIN_PATH = "/api/admin/login";

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

// body (opsional) dikirim sebagai JSON. Endpoint admin selain login membawa token sesi; 401 dari
// endpoint admin = sesi habis atau tidak sah, jadi sesi dihapus dan AdminLayout kembali ke login.
async function request(method, path, params = {}, signal, body) {
  const url = new URL(BASE + path);
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== "") url.searchParams.set(k, v);
  }
  const admin = path.startsWith("/api/admin/") && path !== LOGIN_PATH;
  const headers = {};
  if (admin) {
    const token = getAdminToken();
    if (token) headers.Authorization = `Bearer ${token}`;
  }
  if (body !== undefined) headers["Content-Type"] = "application/json";

  let res;
  try {
    res = await fetch(url, { method, signal, headers, body: body === undefined ? undefined : JSON.stringify(body) });
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
    if (admin && res.status === 401) clearAdminSession("expired");
    const fallback = admin ? undefined : STATUS_MESSAGE[res.status]; // pesan 404/422 khusus halaman pengguna
    throw new ApiError(res.status, detail || fallback || `Permintaan gagal (HTTP ${res.status}).`);
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

// Login admin -> { token, expires_at }; simpan dengan saveAdminSession (lib/adminAuth.js).
export const adminLogin = (password) => request("POST", LOGIN_PATH, {}, undefined, { password });
export const getAdminSession = (signal) => get("/api/admin/session", {}, signal);

// Instrumen. asset_type: stock | crypto | commodity
export const getAdminInstruments = (signal) => get("/api/admin/instruments", {}, signal);
export const lookupInstrument = (asset_type, symbol, signal) =>
  get("/api/admin/instruments/lookup", { asset_type, symbol }, signal);
export const addInstrument = ({ asset_type, symbol, name }) =>
  request("POST", "/api/admin/instruments", {}, undefined, { asset_type, symbol, name });
export const reingestInstrument = (symbol) => request("POST", `/api/admin/instruments/${sym(symbol)}/ingest`);

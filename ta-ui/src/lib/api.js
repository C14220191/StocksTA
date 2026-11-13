// Client untuk backend Go. Endpoint mengikuti dokumentasi "API untuk UI".
// URL backend dari .env (VITE_API_URL), lihat .env.example.
import { clearSession, getToken } from "./auth.js";

const BASE = (import.meta.env.VITE_API_URL || "http://127.0.0.1:8080").replace(/\/$/, "");

// fields: kesalahan per isian form ({ email: "...", password: "..." }); code: jenis kesalahan dari backend
// (mis. email_unverified, token_expired); retryAfter: detik sebelum boleh mencoba lagi.
class ApiError extends Error {
  constructor(status, message, { code, fields, retryAfter } = {}) {
    super(message);
    this.name = "ApiError";
    this.status = status; // 0 = backend tidak terjangkau
    this.code = code ?? null;
    this.fields = fields ?? {};
    this.retryAfter = retryAfter ?? null;
  }
}

// Semua endpoint wajib sesi login (data pasar, analisis, akun sendiri, admin) kecuali endpoint akun
// publik (login, daftar, verifikasi, reset, Google). 401 dari endpoint bersesi = sesi habis atau dicabut.
const SESSION_AUTH_PATHS = new Set(["/api/auth/me", "/api/auth/password", "/api/auth/logout", "/api/auth/logout-all"]);
const needsSession = (path) => !path.startsWith("/api/auth/") || SESSION_AUTH_PATHS.has(path);

const STATUS_MESSAGE = {
  404: "Instrumen tidak ditemukan.",
  422: "Data harga untuk instrumen ini belum tersedia.",
};

// body (opsional) dikirim sebagai JSON. Endpoint bersesi membawa token; 401 dari endpoint itu = sesi
// habis atau tidak sah, jadi sesi dihapus (halaman yang butuh login kembali ke /login).
async function request(method, path, params = {}, signal, body) {
  const url = new URL(BASE + path);
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== "") url.searchParams.set(k, v);
  }
  const authed = needsSession(path);
  const headers = {};
  if (authed) {
    const token = getToken();
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
    let detail = {};
    try {
      detail = (await res.json()) ?? {};
    } catch {
      /* body bukan JSON */
    }
    if (authed && res.status === 401) clearSession("expired");
    // pesan 404/422 khusus halaman pengguna (bukan admin dan bukan akun)
    const userData = !path.startsWith("/api/admin/") && !path.startsWith("/api/auth/");
    const fallback = userData ? STATUS_MESSAGE[res.status] : undefined;
    throw new ApiError(res.status, detail.error || fallback || `Permintaan gagal (HTTP ${res.status}).`, {
      code: detail.code,
      fields: detail.fields,
      retryAfter: detail.retry_after,
    });
  }
  return res.json();
}

const get = (path, params, signal) => request("GET", path, params, signal);
const post = (path, body) => request("POST", path, {}, undefined, body);
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


// Instrumen. asset_type: stock | crypto | commodity
export const getAdminInstruments = (signal) => get("/api/admin/instruments", {}, signal);
export const lookupInstrument = (asset_type, symbol, signal) =>
  get("/api/admin/instruments/lookup", { asset_type, symbol }, signal);
export const addInstrument = ({ asset_type, symbol, name }) =>
  request("POST", "/api/admin/instruments", {}, undefined, { asset_type, symbol, name });
export const reingestInstrument = (symbol) => request("POST", `/api/admin/instruments/${sym(symbol)}/ingest`);

// ---- Akun pengguna ----
// Login / daftar / Google -> { token, expires_at, user }; simpan dengan saveSession (lib/auth.js).
export const getAuthConfig = (signal) => get("/api/auth/config", {}, signal);
export const register = ({ name, email, password, password_confirm }) =>
  post("/api/auth/register", { name, email, password, password_confirm });
export const login = (email, password) => post("/api/auth/login", { email, password });
export const verifyEmail = (token) => post("/api/auth/verify-email", { token });
export const resendVerification = (email) => post("/api/auth/resend-verification", { email });
export const forgotPassword = (email) => post("/api/auth/forgot-password", { email });
export const checkResetToken = (token) => post("/api/auth/reset-password/check", { token });
export const resetPassword = ({ token, password, password_confirm }) =>
  post("/api/auth/reset-password", { token, password, password_confirm });
export const exchangeGoogleCode = (code) => post("/api/auth/google/exchange", { code });
// Login Google = navigasi penuh ke backend (bukan fetch): backend redirect ke Google lalu kembali ke /auth/google.
export const googleStartUrl = (next) => `${BASE}/api/auth/google/start?next=${encodeURIComponent(next || "/")}`;

export const getMe = (signal) => get("/api/auth/me", {}, signal);
export const updateMe = (name) => request("PATCH", "/api/auth/me", {}, undefined, { name });
export const changePassword = ({ current_password, password, password_confirm }) =>
  post("/api/auth/password", { current_password, password, password_confirm });
export const logoutAll = () => post("/api/auth/logout-all");

// Keluar: cabut sesi di backend (kalau gagal, misalnya backend mati, sesi tetap dihapus dari browser).
export async function signOut() {
  try {
    await post("/api/auth/logout");
  } catch {
    /* sesi di browser tetap dihapus */
  }
  clearSession("logout");
}

// ---- Admin: pengguna ----
// q, role: admin|user, limit, offset
export const getAdminUsers = (params, signal) => get("/api/admin/users", params, signal);
export const createUser = ({ name, email, password, role }) => post("/api/admin/users", { name, email, password, role });
// fields: { name?, role?, disabled?, email_verified: true? }
export const updateUser = (id, fields) => request("PATCH", `/api/admin/users/${id}`, {}, undefined, fields);
export const sendUserPasswordReset = (id) => post(`/api/admin/users/${id}/password-reset`);
export const deleteUser = (id) => request("DELETE", `/api/admin/users/${id}`);

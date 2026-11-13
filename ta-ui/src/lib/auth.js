// Sesi login di browser: token dari POST /api/auth/login (atau login Google) + profil akun, disimpan
// di localStorage sampai kedaluwarsa (SESSION_HOURS di backend, default 24 jam). Token hanya dikirim
// ke endpoint akun dan /api/admin/* (lib/api.js). Penyimpanan bisa diblokir browser (mode privat),
// jadi semua akses dibungkus try/catch: tanpa penyimpanan, sesi hanya bertahan selama tab terbuka.

const KEY = "ta-session";
export const AUTH_EVENT = "ta-auth"; // detail: "login" | "logout" | "expired" | "user"

let memory = null; // cadangan kalau localStorage tidak tersedia

try {
  localStorage.removeItem("ta-admin-session"); // sisa login admin lama (sebelum ada akun pengguna)
} catch {
  /* penyimpanan diblokir */
}

function read() {
  try {
    const raw = localStorage.getItem(KEY);
    if (raw) return JSON.parse(raw);
  } catch {
    /* penyimpanan diblokir atau isi rusak */
  }
  return memory;
}

function write(s) {
  memory = s;
  try {
    localStorage.setItem(KEY, JSON.stringify(s));
  } catch {
    /* tetap tersimpan di memori untuk tab ini */
  }
}

const emit = (detail) => window.dispatchEvent(new CustomEvent(AUTH_EVENT, { detail }));

// Sesi yang masih berlaku: { token, expires_at, user } atau null.
export function getSession() {
  const s = read();
  if (!s?.token || !(Date.parse(s.expires_at) > Date.now())) return null;
  return s;
}

export const getToken = () => getSession()?.token ?? null;

export function saveSession({ token, expires_at, user }) {
  write({ token, expires_at, user });
  emit("login");
}

// Profil terbaru dari backend (nama diganti, peran diubah admin, dsb.).
export function updateSessionUser(user) {
  const s = getSession();
  if (!s) return;
  write({ ...s, user });
  emit("user");
}

export function clearSession(reason = "logout") {
  memory = null;
  try {
    localStorage.removeItem(KEY);
  } catch {
    /* tidak ada yang perlu dihapus */
  }
  emit(reason);
}

const AUTH_PAGES = ["/login", "/register", "/forgot-password", "/reset-password", "/verify-email"];

// Tujuan setelah login: hanya path di aplikasi ini (bukan URL luar) dan bukan halaman akun itu sendiri.
export function safeNext(next, fallback = "/") {
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/\\")) return fallback;
  const path = next.split(/[?#]/)[0];
  if (AUTH_PAGES.includes(path) || path.startsWith("/auth/")) return fallback;
  return next;
}

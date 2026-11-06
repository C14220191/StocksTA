// Sesi admin di browser: token dari POST /api/admin/login disimpan di localStorage sampai
// kedaluwarsa (default 12 jam, diatur backend lewat ADMIN_SESSION_HOURS). Token hanya dikirim ke
// /api/admin/* (lib/api.js). Penyimpanan bisa diblokir browser (mode privat), jadi semua akses
// dibungkus try/catch: tanpa penyimpanan, admin cukup login ulang setiap memuat halaman.

const KEY = "ta-admin-session";
export const ADMIN_SESSION_EVENT = "ta-admin-session"; // detail: "login" | "logout" | "expired"

let memory = null; // cadangan kalau localStorage tidak tersedia

function read() {
  try {
    const raw = localStorage.getItem(KEY);
    if (raw) return JSON.parse(raw);
  } catch {
    /* penyimpanan diblokir atau isi rusak */
  }
  return memory;
}

export function getAdminToken() {
  const s = read();
  if (!s?.token || !(Date.parse(s.expires_at) > Date.now())) return null;
  return s.token;
}

export function getAdminExpiry() {
  return getAdminToken() ? read().expires_at : null;
}

export function saveAdminSession(token, expiresAt) {
  memory = { token, expires_at: expiresAt };
  try {
    localStorage.setItem(KEY, JSON.stringify(memory));
  } catch {
    /* tetap tersimpan di memori untuk tab ini */
  }
  window.dispatchEvent(new CustomEvent(ADMIN_SESSION_EVENT, { detail: "login" }));
}

export function clearAdminSession(reason = "logout") {
  memory = null;
  try {
    localStorage.removeItem(KEY);
  } catch {
    /* tidak ada yang perlu dihapus */
  }
  window.dispatchEvent(new CustomEvent(ADMIN_SESSION_EVENT, { detail: reason }));
}

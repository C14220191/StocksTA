export function formatPrice(price, currency, { short = false } = {}) {
  if (price == null) return "-";
  if (currency === "IDR") {
    const n = price.toLocaleString("id-ID", { maximumFractionDigits: 2 });
    return short ? `Rp ${n}` : `Rp. ${n}`;
  }
  const big = Math.abs(price) >= 1;
  return (
    "$" +
    price.toLocaleString("en-US", {
      minimumFractionDigits: big ? 2 : 0,
      maximumFractionDigits: big ? 2 : 6,
    })
  );
}

// Angka harga tanpa simbol mata uang (untuk readout chart)
export function formatPlain(v, currency) {
  if (v == null || Number.isNaN(v)) return "-";
  if (currency === "IDR") return v.toLocaleString("id-ID", { maximumFractionDigits: 2 });
  const big = Math.abs(v) >= 1;
  return v.toLocaleString("en-US", {
    minimumFractionDigits: big ? 2 : 0,
    maximumFractionDigits: big ? 2 : 6,
  });
}

// unit: "price" (satuan harga), "vol" (jumlah besar, dipadatkan), "num" (osilator, 2 desimal)
export function formatIndicator(v, unit, currency) {
  if (v == null || Number.isNaN(v)) return "-";
  if (unit === "price") return formatPlain(v, currency);
  if (unit === "vol") return formatCompact(v);
  return v.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

// Label zona waktu lokal browser: WIB/WITA/WIT, selain itu "UTC+x".
function tzLabel(d) {
  const off = -d.getTimezoneOffset();
  const named = { 420: "WIB", 480: "WITA", 540: "WIT" }[off];
  if (named) return named;
  const h = off / 60;
  return `UTC${h >= 0 ? "+" : ""}${Number.isInteger(h) ? h : h.toFixed(1)}`;
}

const pad2 = (n) => String(n).padStart(2, "0");
const localHM = (d) => `${pad2(d.getHours())}:${pad2(d.getMinutes())}`;

// Ringkasan AI menyebut waktu dalam UTC karena candle di prompt berformat UTC
// (mis. "2026-10-29T22:00Z" atau "01:00Z"). Untuk tampilan, ubah ke waktu lokal:
// "30 Okt 05:00 WIB" / "08:00 WIB". Teks asli di database tidak diubah.
// refIso = waktu analisis dibuat, untuk menentukan tanggal dari jam tanpa tanggal.
export function localizeUtcTimes(text, refIso) {
  if (!text) return text;
  const full = /\b(\d{4}-\d{2}-\d{2})T(\d{2}):(\d{2})(?::\d{2}(?:\.\d+)?)?(?:Z|\+00:00|\s?UTC)/g;
  let out = text.replace(full, (m, date, hh, mm) => {
    const d = new Date(`${date}T${hh}:${mm}:00Z`);
    if (Number.isNaN(d.getTime())) return m;
    const day = d.toLocaleDateString("id-ID", { day: "numeric", month: "short" });
    return `${day} ${localHM(d)} ${tzLabel(d)}`;
  });

  const ref = refIso ? new Date(refIso) : new Date();
  const timeOnly = /\b(\d{1,2})[:.](\d{2})\s?(?:Z|UTC)\b/g;
  out = out.replace(timeOnly, (m, hh, mm) => {
    if (+hh > 23 || +mm > 59) return m;
    // Kejadian terakhir jam UTC itu, pada atau sebelum waktu analisis.
    const d = new Date(Date.UTC(ref.getUTCFullYear(), ref.getUTCMonth(), ref.getUTCDate(), +hh, +mm));
    if (d > ref) d.setUTCDate(d.getUTCDate() - 1);
    return `${localHM(d)} ${tzLabel(d)}`;
  });
  return out;
}

// detik UTC -> waktu lokal browser
export function formatCandleTime(sec, dateOnly = false) {
  const d = new Date(sec * 1000);
  const date = d.toLocaleDateString("id-ID", { day: "2-digit", month: "short", year: "numeric" });
  if (dateOnly) return date;
  return `${date} ${d.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit", hour12: false })}`;
}

export function formatChange(pct) {
  if (pct == null) return "-";
  return `${pct > 0 ? "+" : ""}${pct.toFixed(2)}%`;
}

export function changeClass(pct) {
  if (pct == null || pct === 0) return "text-muted";
  return pct > 0 ? "text-up" : "text-down";
}

// 1278 -> "1,278", 1_800_000 -> "1.8 M", 2_100_000_000 -> "2.1 B"
export function formatCompact(v) {
  if (v == null) return "-";
  const abs = Math.abs(v);
  const units = [
    [1e12, "T"],
    [1e9, "B"],
    [1e6, "M"],
    [1e3, "K"],
  ];
  for (const [div, suffix] of units) {
    if (abs >= div) return `${(v / div).toLocaleString("en-US", { maximumFractionDigits: 1 })} ${suffix}`;
  }
  return v.toLocaleString("en-US", { maximumFractionDigits: 2 });
}

export const formatVolume = formatCompact;

export function formatTurnoverUsd(v) {
  return v == null ? "-" : `$${formatCompact(v)}`;
}

export function formatAgeSeconds(sec) {
  if (sec == null || Number.isNaN(sec)) return "-";
  if (sec < 60) return "baru saja";
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} menit lalu`;
  const h = Math.floor(min / 60);
  if (h < 48) return `${h} jam lalu`;
  return `${Math.floor(h / 24)} hari lalu`;
}

export function ageSecondsFromIso(iso) {
  if (!iso) return null;
  const t = new Date(iso).getTime();
  return Number.isNaN(t) ? null : (Date.now() - t) / 1000;
}

export const assetLabel = (type) => (type === "stock" ? "Saham IDX" : type === "crypto" ? "Kripto" : "");

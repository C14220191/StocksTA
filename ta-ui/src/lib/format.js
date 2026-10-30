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

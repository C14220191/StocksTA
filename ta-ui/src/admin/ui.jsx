import { useEffect } from "react";
import { formatCompact } from "../lib/format.js";

// Komponen dan formatter kecil yang dipakai bersama oleh halaman admin.

export function PageHeader({ title, subtitle, children }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-4">
      <div>
        <h1 className="text-xl font-semibold">{title}</h1>
        {subtitle && <p className="mt-1 text-xs text-muted">{subtitle}</p>}
      </div>
      {children && <div className="flex flex-wrap items-center gap-3">{children}</div>}
    </div>
  );
}

export function Panel({ title, aside, children, className = "" }) {
  return (
    <section className={`rounded-xl border border-line bg-panel p-5 ${className}`}>
      {(title || aside) && (
        <div className="mb-4 flex flex-wrap items-baseline justify-between gap-2">
          {title && <h2 className="text-sm font-semibold">{title}</h2>}
          {aside && <div className="text-xs text-muted">{aside}</div>}
        </div>
      )}
      {children}
    </section>
  );
}

// Stat tile: satu angka utama + konteks kecil di bawahnya.
export function Kpi({ label, value, sub, loading }) {
  return (
    <div className="rounded-xl bg-panel-2 px-5 py-4">
      <div className="text-xs text-muted">{label}</div>
      <div className="mt-2 min-h-8 text-2xl font-semibold tabular-nums">
        {loading ? <span className="inline-block h-6 w-20 animate-pulse rounded bg-panel" /> : value}
      </div>
      {sub && <div className="mt-1 text-xs text-muted">{sub}</div>}
    </div>
  );
}

// Status selalu ikon + label, tidak hanya warna.
const STATUS = {
  ok: { color: "bg-status-good", icon: "✓", label: "Normal" },
  warn: { color: "bg-status-warning", icon: "!", label: "Perlu dicek" },
  down: { color: "bg-status-critical", icon: "✕", label: "Bermasalah" },
  idle: { color: "bg-line-strong", icon: "–", label: "Tidak aktif" }, // keadaan wajar, bukan masalah
};
export function StatusDot({ status, label }) {
  const s = STATUS[status] ?? STATUS.warn;
  return (
    <span className="inline-flex items-center gap-2 text-xs font-semibold">
      <span
        aria-hidden="true"
        className={`inline-flex h-4 w-4 items-center justify-center rounded-full text-[10px] font-bold text-canvas ${s.color}`}
      >
        {s.icon}
      </span>
      {label ?? s.label}
    </span>
  );
}

export function Segmented({ value, options, onChange, label }) {
  return (
    <div role="tablist" aria-label={label} className="inline-flex rounded-lg border border-line bg-panel p-1">
      {options.map((o) => (
        <button
          key={o.value}
          role="tab"
          aria-selected={value === o.value}
          onClick={() => onChange(o.value)}
          className={`rounded-md px-3 py-1.5 text-xs font-semibold transition-colors ${
            value === o.value ? "bg-panel-2 text-ink" : "text-muted hover:text-ink"
          }`}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function Select({ value, options, onChange, label }) {
  return (
    <label className="inline-flex items-center gap-2 text-xs text-muted">
      {label}
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="rounded-lg border border-line bg-panel px-3 py-2 text-xs font-semibold text-ink outline-none focus:border-line-strong"
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </label>
  );
}

// Tabel di dalam container scroll sendiri, supaya halaman tidak ikut melebar di layar sempit.
export function Table({ head, children, empty, colSpan }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-max border-collapse text-left text-xs">
        <thead>
          <tr className="border-b border-line-strong text-muted">
            {head.map((h, i) => (
              <th key={i} className={`px-3 py-2 font-semibold ${h.right ? "text-right" : ""}`}>
                {h.label ?? h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {children}
          {empty && (
            <tr>
              <td colSpan={colSpan ?? head.length} className="px-3 py-6 text-center text-muted">
                {empty}
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

export const Td = ({ children, right, className = "" }) => (
  <td className={`border-b border-line px-3 py-2.5 ${right ? "text-right tabular-nums" : ""} ${className}`}>
    {children}
  </td>
);

export function ErrorBox({ error, onRetry }) {
  if (!error) return null;
  return (
    <div className="rounded-xl border border-line bg-panel p-5 text-sm">
      <p className="text-down">{error.message}</p>
      {onRetry && (
        <button onClick={onRetry} className="mt-3 rounded-lg bg-panel-2 px-4 py-2 text-xs font-semibold hover:text-hold">
          Coba lagi
        </button>
      )}
    </div>
  );
}

export function ModeChip({ mode, threshold }) {
  if (!mode) return null;
  return (
    <span className="rounded-full border border-line px-3 py-1 text-xs text-muted">
      Mode cache <span className="font-semibold text-ink">{mode}</span>
      {threshold != null && <> · ambang {threshold}%</>}
    </span>
  );
}

// Muat ulang data berkala (halaman monitor). reload dari useApi.
export function usePolling(reload, ms) {
  useEffect(() => {
    const t = setInterval(reload, ms);
    return () => clearInterval(t);
  }, [reload, ms]);
}

// ---- formatter ----

export const fmtInt = (v) => (v == null ? "-" : Math.round(v).toLocaleString("id-ID"));
export const fmtPct = (v, digits = 0) => (v == null ? "-" : `${(v * 100).toFixed(digits)}%`);
export const fmtSignedPct = (v, digits = 2) => (v == null ? "-" : `${v > 0 ? "+" : ""}${v.toFixed(digits)}%`);
export const fmtTokens = (v) => (v == null ? "-" : formatCompact(Math.round(v)));

export function fmtMs(v) {
  if (v == null) return "-";
  if (v >= 1000) return `${(v / 1000).toLocaleString("id-ID", { maximumFractionDigits: 1 })} dtk`;
  return `${Math.round(v)} ms`;
}

export function fmtUsd(v) {
  if (v == null) return "-";
  if (v === 0) return "$0";
  const digits = Math.abs(v) >= 1 ? 2 : Math.abs(v) >= 0.01 ? 4 : 6;
  return `$${v.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: digits })}`;
}

// 45 -> "45 dtk", 95 -> "1 mnt 35 dtk", 300 -> "5 mnt", 2700 -> "45 mnt", 3725 -> "1 jam 2 mnt"
export function fmtDuration(sec) {
  if (sec == null) return "-";
  const s = Math.max(0, Math.round(sec));
  if (s < 60) return `${s} dtk`;
  const m = Math.floor(s / 60);
  if (m < 10) return s % 60 ? `${m} mnt ${s % 60} dtk` : `${m} mnt`; // detik hanya relevan untuk durasi pendek
  if (m < 60) return `${m} mnt`;
  const h = Math.floor(m / 60);
  if (h < 48) return m % 60 ? `${h} jam ${m % 60} mnt` : `${h} jam`;
  return `${Math.floor(h / 24)} hari`;
}

export function fmtDateTime(iso) {
  if (!iso) return "-";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "-";
  return d.toLocaleString("id-ID", { day: "2-digit", month: "short", hour: "2-digit", minute: "2-digit" });
}

export const TRIGGERS = [
  { value: "all", label: "Semua sumber" },
  { value: "ui", label: "UI (pengunjung)" },
  { value: "k6", label: "k6 (load test)" },
  { value: "scheduled", label: "Terjadwal" },
  { value: "manual", label: "Manual" },
];

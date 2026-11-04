import { BIAS } from "../lib/methods.js";

// signal dari backend: "BUY" | "HOLD" | "SELL" | "MIXED" | null (null = belum pernah dianalisis, tampil "-").
// Ditampilkan sebagai bias pasar menurut AI (Bullish/Netral/Bearish/Beragam), bukan perintah transaksi.
export default function SignalBadge({ signal, className = "" }) {
  const b = BIAS[signal?.toUpperCase()];
  return (
    <span
      className={`text-sm font-semibold ${b?.cls ?? "text-muted"} ${className}`}
      title={b ? "Bias menurut analisis AI, bukan ajakan berinvestasi" : undefined}
    >
      {b?.label ?? "-"}
    </span>
  );
}

const COLOR = { BUY: "text-up", HOLD: "text-hold", SELL: "text-down" };
const LABEL = { BUY: "Buy", HOLD: "Hold", SELL: "Sell" };

// signal dari backend: "BUY" | "HOLD" | "SELL" | null (null = belum pernah dianalisis, tampil "-")
export default function SignalBadge({ signal, className = "" }) {
  const key = signal?.toUpperCase();
  return (
    <span className={`text-sm font-semibold ${COLOR[key] ?? "text-muted"} ${className}`}>
      {LABEL[key] ?? "-"}
    </span>
  );
}

const STYLE = {
  live: { dot: "bg-up animate-pulse", label: "Live" },
  connecting: { dot: "bg-hold", label: "Menyambung..." },
  offline: { dot: "bg-down", label: "Terputus, mencoba lagi" },
};

export default function LiveBadge({ status, className = "" }) {
  const s = STYLE[status];
  if (!s) return null;
  return (
    <span className={`inline-flex items-center gap-1.5 font-sans text-xs font-normal text-muted ${className}`}>
      <span className={`h-1.5 w-1.5 rounded-full ${s.dot}`} />
      {s.label}
    </span>
  );
}

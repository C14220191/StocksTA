import { OVERLAYS, OSCILLATORS } from "../lib/indicators.js";

function Chip({ spec, on, reason, onToggle }) {
  const disabled = !!reason;
  return (
    <button
      aria-pressed={on && !disabled}
      disabled={disabled}
      title={reason ?? (spec.params ? `${spec.label} (${spec.params})` : spec.label)}
      onClick={() => onToggle(spec.id)}
      className={`rounded-full border px-3 py-1 text-xs font-bold transition-colors disabled:cursor-not-allowed disabled:opacity-30 ${
        on && !disabled ? "border-hold bg-panel-2 text-ink" : "border-transparent bg-panel-2 text-muted hover:text-ink"
      }`}
    >
      {spec.label}
    </button>
  );
}

function Row({ title, specs, active, ctx, onToggle }) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className="w-16 shrink-0 font-sans text-xs text-muted">{title}</span>
      {specs.map((s) => (
        <Chip key={s.id} spec={s} on={active.has(s.id)} reason={s.unavailable?.(ctx)} onToggle={onToggle} />
      ))}
    </div>
  );
}

// ctx = { timeframe, assetType }; indikator yang tidak bisa dipakai pada ctx tsb dinonaktifkan.
export default function IndicatorPicker({ active, ctx, onToggle, onAll, onNone }) {
  const usable = [...OVERLAYS, ...OSCILLATORS].filter((s) => !s.unavailable?.(ctx));
  const activeCount = usable.filter((s) => active.has(s.id)).length;

  return (
    <div className="mt-4 space-y-2">
      <Row title="Overlay" specs={OVERLAYS} active={active} ctx={ctx} onToggle={onToggle} />
      <Row title="Osilator" specs={OSCILLATORS} active={active} ctx={ctx} onToggle={onToggle} />
      <div className="flex flex-wrap items-center gap-2 pt-1">
        <span className="w-16 shrink-0 font-sans text-xs text-muted">{activeCount} aktif</span>
        <button
          onClick={() => onAll(usable.map((s) => s.id))}
          disabled={activeCount === usable.length}
          className="rounded-full bg-panel-2 px-3 py-1 text-xs font-bold text-hold transition-colors hover:text-ink disabled:opacity-40"
        >
          Tampilkan semua ({usable.length})
        </button>
        <button
          onClick={onNone}
          disabled={activeCount === 0}
          className="rounded-full bg-panel-2 px-3 py-1 text-xs font-bold text-muted transition-colors hover:text-ink disabled:opacity-40"
        >
          Kosongkan
        </button>
      </div>
    </div>
  );
}

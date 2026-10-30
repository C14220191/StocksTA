import { indexOfTime } from "../lib/candles.js";
import { changeClass, formatChange, formatCandleTime, formatCompact, formatIndicator, formatPlain } from "../lib/format.js";

// Bar info di atas chart: OHLCV candle yang sedang di-hover (atau candle terakhir kalau tidak ada hover)
// dan nilai setiap indikator aktif pada candle yang sama.
// sticky: tetap terlihat saat halaman di-scroll melewati chart yang tinggi (banyak pane).
export default function ChartReadout({ model, hover, currency }) {
  if (!model?.candles.length) return null;
  const { candles, outputs, specs, timeframe } = model;

  const found = hover == null ? -1 : indexOfTime(candles, hover);
  const i = found >= 0 ? found : candles.length - 1;
  const c = candles[i];
  const prev = candles[i - 1];
  const change = prev ? ((c.close - prev.close) / prev.close) * 100 : null;
  const tone = c.close >= c.open ? "text-up" : "text-down";

  return (
    <div className="sticky top-0 z-10 border-b border-line bg-panel/95 px-4 py-2 font-sans text-xs backdrop-blur">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
        <span className="font-semibold text-ink">{formatCandleTime(c.time, timeframe === "1d")}</span>
        {[
          ["O", c.open],
          ["H", c.high],
          ["L", c.low],
          ["C", c.close],
        ].map(([k, v]) => (
          <span key={k}>
            <span className="text-muted">{k} </span>
            <b className={tone}>{formatPlain(v, currency)}</b>
          </span>
        ))}
        <span className={changeClass(change)} title="Perubahan terhadap penutupan candle sebelumnya">
          {formatChange(change)}
        </span>
        <span>
          <span className="text-muted">Vol </span>
          <b>{formatCompact(c.volume)}</b>
        </span>
        {!c.closed && <span className="text-hold">candle berjalan</span>}
        {hover == null && <span className="ml-auto text-muted">arahkan kursor ke candle untuk detail</span>}
      </div>

      {specs.length > 0 && (
        <div className="mt-1.5 grid gap-y-1">
          {specs.map((spec) => {
            const values = outputs[spec.id] ?? {};
            let series = spec.series;
            // Supertrend / PSAR: hanya satu sisi yang berisi pada tiap candle
            if (spec.exclusive) {
              const filled = series.filter((s) => values[s.key]?.[i] != null);
              if (filled.length) series = filled;
            }
            return (
              <div key={spec.id} className="flex flex-wrap items-center gap-x-4 gap-y-0.5">
                <span className="font-semibold">
                  {spec.label}
                  {spec.params && <span className="font-normal text-muted"> ({spec.params})</span>}
                </span>
                {series.map((s) => (
                  <span key={s.key} className="inline-flex items-center gap-1">
                    <span className="inline-block h-1.5 w-1.5 rounded-full" style={{ background: s.color }} />
                    <span className="text-muted">{s.label}</span>
                    <b>{formatIndicator(values[s.key]?.[i], s.unit, currency)}</b>
                  </span>
                ))}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

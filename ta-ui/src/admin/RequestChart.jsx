import { useEffect, useRef, useState } from "react";
import { Table, Td, fmtInt } from "./ui.jsx";

// Stacked bar request per jam / per hari: panggilan AI (bawah) + cache HIT (atas).
// Error tidak digambar sebagai seri (jarang, dan warna status dicadangkan); jumlahnya ada di tooltip
// dan tabel. Warna: --color-series-2 (AI) dan --color-series-1 (HIT), divalidasi untuk panel gelap.
const SERIES = [
  { key: "ai_calls", label: "Panggilan AI (MISS/BYPASS)", color: "var(--color-series-2)" },
  { key: "hits", label: "Cache HIT", color: "var(--color-series-1)" },
];
const H = 220;
const PAD = { top: 12, right: 8, bottom: 26, left: 40 };
const GAP = 2; // celah warna permukaan antar segmen

function niceMax(v) {
  if (v <= 4) return 4;
  const mag = 10 ** Math.floor(Math.log10(v));
  const step = [1, 2, 2.5, 5, 10].find((s) => s * mag * 4 >= v) * mag;
  return step * 4;
}

function label(t, bucket, long = false) {
  const [date, time] = t.split("T");
  const [, mo, d] = date.split("-");
  const month = ["Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"][+mo - 1];
  if (bucket === "hour") return long ? `${+d} ${month} ${time}` : time;
  return `${+d} ${month}`;
}

// Path batang dengan ujung atas membulat (radius 4) dan alas rata.
function topRounded(x, y, w, h, r = 4) {
  const rr = Math.min(r, h / 2, w / 2);
  return `M${x},${y + h}V${y + rr}Q${x},${y} ${x + rr},${y}H${x + w - rr}Q${x + w},${y} ${x + w},${y + rr}V${y + h}Z`;
}

export default function RequestChart({ series, bucket }) {
  const wrap = useRef(null);
  const [width, setWidth] = useState(640);
  const [hover, setHover] = useState(null);
  const [asTable, setAsTable] = useState(false);

  useEffect(() => {
    const el = wrap.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setWidth(Math.max(280, e.contentRect.width)));
    ro.observe(el);
    return () => ro.disconnect();
  }, [asTable]);

  const data = series ?? [];
  const totals = data.map((d) => d.hits + d.ai_calls);
  const max = niceMax(Math.max(0, ...totals));
  const plotW = width - PAD.left - PAD.right;
  const plotH = H - PAD.top - PAD.bottom;
  const slot = data.length ? plotW / data.length : plotW;
  const barW = Math.max(2, Math.min(28, slot * 0.7));
  const y = (v) => PAD.top + plotH - (v / max) * plotH;
  const ticks = [0, 0.25, 0.5, 0.75, 1].map((f) => Math.round(max * f));
  const labelEvery = Math.ceil(data.length / Math.max(1, Math.floor(plotW / 56)));
  const errors = data.reduce((s, d) => s + d.errors, 0);

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3 text-xs">
        <ul className="flex flex-wrap gap-4" aria-label="Legenda">
          {SERIES.slice()
            .reverse()
            .map((s) => (
              <li key={s.key} className="flex items-center gap-2 text-muted">
                <span className="h-2.5 w-2.5 rounded-sm" style={{ background: s.color }} aria-hidden="true" />
                {s.label}
              </li>
            ))}
        </ul>
        <button
          onClick={() => setAsTable((v) => !v)}
          className="rounded-md border border-line px-3 py-1 font-semibold text-muted hover:text-ink"
        >
          {asTable ? "Lihat grafik" : "Lihat tabel"}
        </button>
      </div>

      {asTable ? (
        <div className="max-h-72 overflow-y-auto">
          <Table head={["Waktu", { label: "Cache HIT", right: true }, { label: "Panggilan AI", right: true }, { label: "Error", right: true }]}>
            {data.map((d) => (
              <tr key={d.t}>
                <Td>{label(d.t, bucket, true)}</Td>
                <Td right>{fmtInt(d.hits)}</Td>
                <Td right>{fmtInt(d.ai_calls)}</Td>
                <Td right>{fmtInt(d.errors)}</Td>
              </tr>
            ))}
          </Table>
        </div>
      ) : (
        <div ref={wrap} className="relative" onMouseLeave={() => setHover(null)}>
          <svg width={width} height={H} role="img" aria-label="Jumlah request per periode, dipisah cache HIT dan panggilan AI">
            {ticks.map((t) => (
              <g key={t}>
                <line x1={PAD.left} x2={width - PAD.right} y1={y(t)} y2={y(t)} stroke="var(--color-line)" strokeWidth="1" />
                <text x={PAD.left - 8} y={y(t)} dy="0.32em" textAnchor="end" fontSize="10" fill="var(--color-muted)">
                  {t}
                </text>
              </g>
            ))}
            {data.map((d, i) => {
              const cx = PAD.left + slot * i + slot / 2;
              const x = cx - barW / 2;
              let base = y(0);
              const segs = SERIES.map((s) => ({ ...s, v: d[s.key] })).filter((s) => s.v > 0);
              return (
                <g key={d.t} opacity={hover == null || hover === i ? 1 : 0.45}>
                  {segs.map((s, j) => {
                    const top = y((j === 0 ? 0 : segs.slice(0, j).reduce((a, b) => a + b.v, 0)) + s.v);
                    const h = Math.max(1, base - top - (j > 0 ? GAP : 0));
                    const yTop = base - (j > 0 ? GAP : 0) - h;
                    base = yTop;
                    const last = j === segs.length - 1;
                    return last ? (
                      <path key={s.key} d={topRounded(x, yTop, barW, h)} fill={s.color} />
                    ) : (
                      <rect key={s.key} x={x} y={yTop} width={barW} height={h} fill={s.color} />
                    );
                  })}
                  {i % labelEvery === 0 && (
                    <text x={cx} y={H - 8} textAnchor="middle" fontSize="10" fill="var(--color-muted)">
                      {label(d.t, bucket)}
                    </text>
                  )}
                  {/* target hover selebar slot, setinggi plot */}
                  <rect
                    x={PAD.left + slot * i}
                    y={PAD.top}
                    width={slot}
                    height={plotH}
                    fill="transparent"
                    onMouseEnter={() => setHover(i)}
                  />
                </g>
              );
            })}
            <line x1={PAD.left} x2={width - PAD.right} y1={y(0)} y2={y(0)} stroke="var(--color-line-strong)" strokeWidth="1" />
          </svg>
          {hover != null && data[hover] && (
            <div
              className="pointer-events-none absolute top-2 z-10 min-w-40 rounded-lg border border-line bg-canvas px-3 py-2 text-xs shadow-lg"
              style={{
                left: Math.min(PAD.left + slot * hover + slot / 2 + 10, width - 170),
              }}
            >
              <div className="mb-1 font-semibold">{label(data[hover].t, bucket, true)}</div>
              {SERIES.slice()
                .reverse()
                .map((s) => (
                  <div key={s.key} className="flex items-center justify-between gap-4">
                    <span className="flex items-center gap-2 text-muted">
                      <span className="h-2 w-2 rounded-sm" style={{ background: s.color }} />
                      {s.key === "hits" ? "Cache HIT" : "Panggilan AI"}
                    </span>
                    <span className="tabular-nums">{fmtInt(data[hover][s.key])}</span>
                  </div>
                ))}
              <div className="flex justify-between gap-4 text-muted">
                <span>Error</span>
                <span className="tabular-nums">{fmtInt(data[hover].errors)}</span>
              </div>
            </div>
          )}
        </div>
      )}
      {errors > 0 && <p className="mt-2 text-xs text-muted">{fmtInt(errors)} request error di periode ini (tidak digambar).</p>}
    </div>
  );
}

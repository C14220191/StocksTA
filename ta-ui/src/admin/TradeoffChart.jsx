import { useEffect, useRef, useState } from "react";

// Bidang trade-off (Bab 2.7): sumbu x = SSR pada θ_s (stale serve rate, makin kecil makin segar),
// sumbu y = penghematan S (makin besar makin hemat). Satu titik per kondisi eksperimen.
// Identitas titik lewat label langsung (bukan warna), karena jumlah kondisi > batas palet
// kategorikal untuk scatter; semua titik memakai --color-series-1.
const H = 300;
const PAD = { top: 16, right: 24, bottom: 40, left: 48 };

export function conditionLabel(r) {
  const th = r.threshold_pct != null ? ` θ${r.threshold_pct}` : "";
  return `${r.condition}${th}${r.pregen ? " +pg" : ""}`;
}

export default function TradeoffChart({ rows, thetaS }) {
  const wrap = useRef(null);
  const [width, setWidth] = useState(600);
  const [hover, setHover] = useState(null);
  const pts = rows
    .map((r) => ({ r, x: r.condition === "no_cache" ? 0 : r.ssr_pct?.[thetaS], y: r.savings_pct }))
    .filter((p) => p.x != null && p.y != null);
  // Dipasang ulang saat grafik muncul: sebelum ada data, elemen pembungkus belum dirender.
  const hasPts = pts.length > 0;
  useEffect(() => {
    const ro = new ResizeObserver(([e]) => setWidth(Math.max(300, e.contentRect.width)));
    if (wrap.current) ro.observe(wrap.current);
    return () => ro.disconnect();
  }, [hasPts]);
  const maxX = Math.max(5, ...pts.map((p) => p.x)) * 1.1;
  const minY = Math.min(0, ...pts.map((p) => p.y));
  const maxY = 100;
  const plotW = width - PAD.left - PAD.right;
  const plotH = H - PAD.top - PAD.bottom;
  const sx = (v) => PAD.left + (v / maxX) * plotW;
  const sy = (v) => PAD.top + plotH - ((v - minY) / (maxY - minY)) * plotH;
  const xTicks = [0, 0.25, 0.5, 0.75, 1].map((f) => +(maxX * f).toFixed(1));
  const yTicks = [minY, (minY + maxY) / 2, maxY].map((v) => Math.round(v));

  if (pts.length === 0) {
    return <p className="py-8 text-center text-xs text-muted">Belum ada kondisi dengan data HIT untuk dipetakan.</p>;
  }

  return (
    <div ref={wrap} className="relative" onMouseLeave={() => setHover(null)}>
      <svg width={width} height={H} role="img" aria-label={`Penghematan S terhadap SSR pada θ_s ${thetaS}% per kondisi`}>
        {yTicks.map((t) => (
          <g key={`y${t}`}>
            <line x1={PAD.left} x2={width - PAD.right} y1={sy(t)} y2={sy(t)} stroke="var(--color-line)" />
            <text x={PAD.left - 8} y={sy(t)} dy="0.32em" textAnchor="end" fontSize="10" fill="var(--color-muted)">
              {t}%
            </text>
          </g>
        ))}
        {xTicks.map((t) => (
          <text key={`x${t}`} x={sx(t)} y={H - PAD.bottom + 16} textAnchor="middle" fontSize="10" fill="var(--color-muted)">
            {t}%
          </text>
        ))}
        <line x1={PAD.left} x2={PAD.left} y1={PAD.top} y2={PAD.top + plotH} stroke="var(--color-line-strong)" />
        <text x={PAD.left + plotW / 2} y={H - 6} textAnchor="middle" fontSize="11" fill="var(--color-muted)">
          SSR pada θ_s = {thetaS}% (lebih kiri = lebih segar)
        </text>
        <text
          transform={`translate(12 ${PAD.top + plotH / 2}) rotate(-90)`}
          textAnchor="middle"
          fontSize="11"
          fill="var(--color-muted)"
        >
          Penghematan S (lebih atas = lebih hemat)
        </text>
        {pts.map((p, i) => (
          <g key={conditionLabel(p.r)} onMouseEnter={() => setHover(i)}>
            <circle cx={sx(p.x)} cy={sy(p.y)} r={hover === i ? 7 : 5} fill="var(--color-series-1)" stroke="var(--color-panel)" strokeWidth="2" />
            <text x={sx(p.x) + 9} y={sy(p.y)} dy="0.32em" fontSize="10" fill="var(--color-ink)">
              {conditionLabel(p.r)}
            </text>
            <circle cx={sx(p.x)} cy={sy(p.y)} r={14} fill="transparent" />
          </g>
        ))}
      </svg>
      {hover != null && pts[hover] && (
        <div
          className="pointer-events-none absolute z-10 rounded-lg border border-line bg-canvas px-3 py-2 text-xs shadow-lg"
          style={{ left: Math.min(sx(pts[hover].x) + 14, width - 200), top: Math.max(0, sy(pts[hover].y) - 50) }}
        >
          <div className="mb-1 font-semibold">{conditionLabel(pts[hover].r)}</div>
          <div className="flex justify-between gap-4 text-muted">
            <span>Penghematan S</span>
            <span className="tabular-nums text-ink">{pts[hover].y.toFixed(1)}%</span>
          </div>
          <div className="flex justify-between gap-4 text-muted">
            <span>SSR θ_s {thetaS}%</span>
            <span className="tabular-nums text-ink">{pts[hover].x.toFixed(2)}%</span>
          </div>
          <div className="flex justify-between gap-4 text-muted">
            <span>Hit rate</span>
            <span className="tabular-nums text-ink">{pts[hover].r.hit_rate_pct?.toFixed(1) ?? "-"}%</span>
          </div>
        </div>
      )}
    </div>
  );
}

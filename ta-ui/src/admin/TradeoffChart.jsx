import { useEffect, useRef, useState } from "react";
import { Segmented } from "./ui.jsx";

// Bidang trade-off (Bab 2.7): sumbu x = SSR pada θ_s (stale serve rate, makin kecil makin segar),
// sumbu y = penghematan S (makin besar makin hemat). Satu titik per kondisi eksperimen.
// Label hanya pada no_cache dan kondisi di garis batas Pareto (tidak ada kondisi lain yang lebih
// hemat sekaligus lebih segar): dengan 15 kondisi yang sering berdekatan, label di setiap titik
// saling menimpa. Kondisi lain dikenali lewat tooltip (titik terdekat + yang berimpit) dan tabel.
const H = 300;
const PAD = { top: 16, right: 24, bottom: 40, left: 48 };
const LABEL_H = 13; // tinggi satu baris label (px)
const CHAR_W = 6; // perkiraan lebar karakter fontSize 10 (px)
const NEAR = 10; // titik dalam radius ini dari titik yang disorot dianggap berimpit (px)
const HIT = 16; // radius kursor untuk memilih titik (px)

export function conditionLabel(r) {
  const th = r.threshold_pct != null ? ` θ${r.threshold_pct}` : "";
  return `${r.condition}${th}${r.pregen ? " +pg" : ""}`;
}

// p didominasi kalau ada q yang tidak lebih basi dan tidak kurang hemat, dan lebih baik di salah satunya.
function paretoFront(pts) {
  return new Set(
    pts.filter((p) => !pts.some((q) => q !== p && q.x <= p.x && q.y >= p.y && (q.x < p.x || q.y > p.y)))
  );
}

// Tick "bulat" (1, 2, 2.5, 5 x 10^n) di [lo, hi]
function niceTicks(lo, hi, n = 4) {
  const raw = (hi - lo) / n || 1;
  const mag = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= raw);
  const out = [];
  for (let v = Math.ceil(lo / step) * step; v <= hi + step * 1e-6; v += step) out.push(+v.toFixed(6));
  return out;
}

const fmtTick = (v) => `${+v.toFixed(2)}%`;

const SCALES = [
  { value: "full", label: "0–100%" },
  { value: "zoom", label: "Perbesar" },
];

export default function TradeoffChart({ rows, thetaS }) {
  const wrap = useRef(null);
  const [width, setWidth] = useState(600);
  const [hover, setHover] = useState(null); // indeks titik terdekat kursor
  const [scale, setScale] = useState("full");
  const all = rows
    .map((r) => ({ r, name: conditionLabel(r), x: r.condition === "no_cache" ? 0 : r.ssr_pct?.[thetaS], y: r.savings_pct }))
    .filter((p) => p.x != null && p.y != null);
  // Dipasang ulang saat grafik muncul: sebelum ada data, elemen pembungkus belum dirender.
  const hasPts = all.length > 0;
  useEffect(() => {
    const ro = new ResizeObserver(([e]) => setWidth(Math.max(300, e.contentRect.width)));
    if (wrap.current) ro.observe(wrap.current);
    return () => ro.disconnect();
  }, [hasPts]);

  if (all.length === 0) {
    return <p className="py-8 text-center text-xs text-muted">Belum ada kondisi dengan data HIT untuk dipetakan.</p>;
  }

  // Skala "Perbesar": hanya kondisi ber-cache, supaya selisih beberapa persen di antara mereka terlihat.
  const cached = all.filter((p) => p.r.condition !== "no_cache");
  const zoomed = scale === "zoom" && cached.length >= 2;
  const pts = zoomed ? cached : all;
  let minY, maxY, maxX;
  if (zoomed) {
    const lo = Math.min(...pts.map((p) => p.y));
    const hi = Math.max(...pts.map((p) => p.y));
    const pad = Math.max((hi - lo) * 0.2, 1);
    minY = Math.floor(lo - pad);
    maxY = Math.ceil(hi + pad);
    maxX = Math.max(0.5, ...pts.map((p) => p.x)) * 1.15;
  } else {
    minY = Math.min(0, ...pts.map((p) => p.y));
    maxY = 100;
    maxX = Math.max(5, ...pts.map((p) => p.x)) * 1.1;
  }
  const plotW = width - PAD.left - PAD.right;
  const plotH = H - PAD.top - PAD.bottom;
  const sx = (v) => PAD.left + (v / maxX) * plotW;
  const sy = (v) => PAD.top + plotH - ((v - minY) / (maxY - minY)) * plotH;
  const xTicks = niceTicks(0, maxX);
  const yTicks = niceTicks(minY, maxY);
  for (const p of pts) {
    p.cx = sx(p.x);
    p.cy = sy(p.y);
  }

  const front = paretoFront(pts);
  const frontLine = [...front].sort((a, b) => a.x - b.x || b.y - a.y);

  // Titik garis batas yang berimpit (< 3 px) digabung; urutan = dari paling segar ke paling hemat.
  const frontGroups = [];
  for (const p of frontLine.filter((q) => q.r.condition !== "no_cache")) {
    const g = frontGroups.find((g) => Math.hypot(g.cx - p.cx, g.cy - p.cy) < 3);
    if (g) g.pts.push(p);
    else frontGroups.push({ cx: p.cx, cy: p.cy, pts: [p] });
  }
  const nc = pts.find((p) => p.r.condition === "no_cache");
  // <= 4 titik garis batas: label nama langsung. Lebih dari itu label nama saling menimpa (titiknya
  // berdekatan di sepanjang kurva), jadi titik diberi nomor urut dan namanya didaftar di bawah grafik.
  const numbered = frontGroups.length > 4;

  // Penempatan label: kandidat posisi dicoba berurutan; rintangan = label terpasang + semua titik.
  const placed = [];
  const dots = pts.map((p) => ({ x0: p.cx - 6, x1: p.cx + 6, y0: p.cy - 6, y1: p.cy + 6 }));
  const box = (x, w, anchor) => (anchor === "start" ? x : anchor === "end" ? x - w : x - w / 2);
  const free = (x0, w, y, own) =>
    x0 >= PAD.left + 2 &&
    x0 + w <= width - 2 &&
    y >= PAD.top &&
    y <= PAD.top + plotH + 4 &&
    !placed.some((b) => x0 < b.x1 && x0 + w > b.x0 && Math.abs(y - b.y) < LABEL_H) &&
    !dots.some((d) => d !== own && x0 < d.x1 && x0 + w > d.x0 && y - 5 < d.y1 && y + 5 > d.y0);
  const place = (g, text, candidates) => {
    const w = text.length * CHAR_W;
    const own = dots[pts.findIndex((p) => p.cx === g.cx && p.cy === g.cy)];
    const ok = candidates.find((c) => free(box(g.cx + c.dx, w, c.anchor), w, g.cy + c.dy, own)) ?? candidates[0];
    const x = g.cx + ok.dx;
    const y = g.cy + ok.dy;
    placed.push({ x0: box(x, w, ok.anchor), x1: box(x, w, ok.anchor) + w, y });
    return { cx: g.cx, cy: g.cy, text, x, y, anchor: ok.anchor, leader: !numbered && Math.abs(ok.dy) > 3 };
  };
  const sideCandidates = [0, -1, 1, -2, 2].flatMap((k) => [
    { dx: 10, dy: k * LABEL_H, anchor: "start" },
    { dx: -10, dy: k * LABEL_H, anchor: "end" },
  ]);
  const numberCandidates = [
    { dx: 0, dy: -12, anchor: "middle" },
    { dx: 0, dy: 13, anchor: "middle" },
    { dx: 9, dy: 0, anchor: "start" },
    { dx: -9, dy: 0, anchor: "end" },
    { dx: 9, dy: -11, anchor: "start" },
    { dx: -9, dy: -11, anchor: "end" },
    { dx: 9, dy: 12, anchor: "start" },
    { dx: -9, dy: 12, anchor: "end" },
  ];
  const groupName = (g) =>
    g.pts.length > 2 ? `${g.pts[0].name}, ${g.pts[1].name} +${g.pts.length - 2}` : g.pts.map((p) => p.name).join(", ");
  const labels = [
    ...(nc ? [place({ cx: nc.cx, cy: nc.cy }, nc.name, sideCandidates)] : []),
    ...frontGroups.map((g, i) => place(g, numbered ? String(i + 1) : groupName(g), numbered ? numberCandidates : sideCandidates)),
  ];

  const onMove = (e) => {
    const box = e.currentTarget.getBoundingClientRect();
    const mx = e.clientX - box.left;
    const my = e.clientY - box.top;
    let best = null;
    let bestD = HIT;
    pts.forEach((p, i) => {
      const d = Math.hypot(p.cx - mx, p.cy - my);
      if (d < bestD) [best, bestD] = [i, d];
    });
    setHover(best);
  };
  const hp = hover != null ? pts[hover] : null;
  const near = hp ? pts.filter((p) => p !== hp && Math.hypot(p.cx - hp.cx, p.cy - hp.cy) < NEAR) : [];

  return (
    <div ref={wrap} className="relative" onMouseLeave={() => setHover(null)}>
      <div className="mb-2 flex flex-wrap items-center justify-between gap-3 text-xs text-muted">
        <div className="flex flex-wrap items-center gap-4">
          <span className="flex items-center gap-1.5">
            <svg width="10" height="10" aria-hidden="true">
              <circle cx="5" cy="5" r="4" fill="var(--color-series-1)" />
            </svg>
            Garis batas: tidak ada kondisi lain yang lebih hemat sekaligus lebih segar
          </span>
          <span className="flex items-center gap-1.5">
            <svg width="10" height="10" aria-hidden="true">
              <circle cx="5" cy="5" r="4" fill="var(--color-muted)" />
            </svg>
            Kondisi lain: arahkan kursor, angka lengkap di tabel
          </span>
        </div>
        {cached.length >= 2 && <Segmented label="Skala" value={scale} options={SCALES} onChange={setScale} />}
      </div>
      <svg
        width={width}
        height={H}
        role="img"
        aria-label={`Penghematan S terhadap SSR pada θ_s ${thetaS}% per kondisi`}
        onMouseMove={onMove}
      >
        {yTicks.map((t) => (
          <g key={`y${t}`}>
            <line x1={PAD.left} x2={width - PAD.right} y1={sy(t)} y2={sy(t)} stroke="var(--color-line)" />
            <text x={PAD.left - 8} y={sy(t)} dy="0.32em" textAnchor="end" fontSize="10" fill="var(--color-muted)">
              {fmtTick(t)}
            </text>
          </g>
        ))}
        {xTicks.map((t) => (
          <text key={`x${t}`} x={sx(t)} y={H - PAD.bottom + 16} textAnchor="middle" fontSize="10" fill="var(--color-muted)">
            {fmtTick(t)}
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

        {frontLine.length > 1 && (
          <polyline
            points={frontLine.map((p) => `${p.cx},${p.cy}`).join(" ")}
            fill="none"
            stroke="var(--color-series-1)"
            strokeOpacity="0.45"
            strokeWidth="1.5"
          />
        )}
        {/* titik di luar garis batas dulu, supaya titik garis batas tergambar di atasnya */}
        {[...pts]
          .sort((a, b) => front.has(a) - front.has(b))
          .map((p) => {
            const on = front.has(p);
            const active = hp === p;
            return (
              <circle
                key={p.name}
                cx={p.cx}
                cy={p.cy}
                r={(on ? 5 : 4) + (active ? 2 : 0)}
                fill={on ? "var(--color-series-1)" : "var(--color-muted)"}
                stroke="var(--color-panel)"
                strokeWidth="2"
              />
            );
          })}
        {labels.map((l) => (
          <g key={`${l.text}@${l.cx},${l.cy}`} pointerEvents="none">
            {l.leader && (
              <line
                x1={l.cx + (l.anchor === "start" ? 6 : -6)}
                y1={l.cy}
                x2={l.anchor === "start" ? l.x - 2 : l.x + 2}
                y2={l.y}
                stroke="var(--color-line-strong)"
                strokeWidth="1"
              />
            )}
            <text
              x={l.x}
              y={l.y}
              dy="0.32em"
              textAnchor={l.anchor}
              fontSize="10"
              fontWeight={numbered && l.text !== nc?.name ? 600 : 400}
              fill="var(--color-ink)"
            >
              {l.text}
            </text>
          </g>
        ))}
      </svg>
      {zoomed && <p className="mt-1 text-xs text-muted">Skala diperbesar: no_cache (0%, 0%) berada di luar bidang.</p>}
      {numbered && (
        <ol className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs" aria-label="Kondisi di garis batas">
          {frontGroups.map((g, i) => (
            <li key={groupName(g)} className="flex items-baseline gap-1.5">
              <span className="font-semibold tabular-nums text-ink">{i + 1}</span>
              <span className="text-ink">{g.pts.map((p) => p.name).join(", ")}</span>
              <span className="tabular-nums text-muted">
                S {g.pts[0].y.toFixed(1)}% · SSR {g.pts[0].x.toFixed(2)}%
              </span>
            </li>
          ))}
        </ol>
      )}
      {hp && (
        <div
          className="pointer-events-none absolute z-10 max-w-xs rounded-lg border border-line bg-canvas px-3 py-2 text-xs shadow-lg"
          style={{ left: Math.min(hp.cx + 14, width - 240), top: Math.max(0, hp.cy - 40) }}
        >
          <div className="mb-1 font-semibold">{hp.name}</div>
          <div className="flex justify-between gap-4 text-muted">
            <span>Penghematan S</span>
            <span className="tabular-nums text-ink">{hp.y.toFixed(1)}%</span>
          </div>
          <div className="flex justify-between gap-4 text-muted">
            <span>SSR θ_s {thetaS}%</span>
            <span className="tabular-nums text-ink">{hp.x.toFixed(2)}%</span>
          </div>
          <div className="flex justify-between gap-4 text-muted">
            <span>Hit rate</span>
            <span className="tabular-nums text-ink">
              {hp.r.hit_rate_pct != null ? `${hp.r.hit_rate_pct.toFixed(1)}%` : "-"}
            </span>
          </div>
          {near.length > 0 && (
            <div className="mt-2 border-t border-line pt-2 text-muted">
              Berimpit di titik ini:{" "}
              <span className="text-ink">
                {near
                  .slice(0, 6)
                  .map((p) => `${p.name} (${p.y.toFixed(1)}%)`)
                  .join(", ")}
                {near.length > 6 ? ` +${near.length - 6}` : ""}
              </span>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

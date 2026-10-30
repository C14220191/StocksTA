import { useEffect, useRef, useState } from "react";
import {
  createChart,
  CandlestickSeries,
  LineSeries,
  HistogramSeries,
  ColorType,
  LineStyle,
  TickMarkType,
} from "lightweight-charts";
import ChartReadout from "./ChartReadout.jsx";
import { INDICATORS, INDICATOR_BY_ID, computeOutputs } from "../lib/indicators.js";
import { connectKline, fetchKlines, TF_SECONDS } from "../lib/binance.js";
import { klineToCandle, mergeCandles } from "../lib/candles.js";

const MAIN_HEIGHT = 380; // chart harga + sumbu waktu
const PANE_HEIGHT = 140; // tiap osilator

// Warna chart dibaca dari variabel tema di index.css supaya selalu sama dengan palette halaman.
const cssVar = (name, fallback) =>
  getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;

const hhmm = (d) => d.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit", hour12: false });

const pickSpecs = (active, ctx) => INDICATORS.filter((s) => active.has(s.id) && !s.unavailable?.(ctx));

const toCandlePoint = ({ time, open, high, low, close }) => ({ time, open, high, low, close });

// null (data belum cukup / sisi tren yang tidak aktif) -> whitespace, sehingga garis terputus di situ.
function pointAt(ser, candles, values, i) {
  const time = candles[i].time;
  const v = values?.[i];
  if (v == null || Number.isNaN(v)) return { time };
  if (ser.kind === "hist") {
    return { time, value: v, color: ser.colorFn ? ser.colorFn(v, i, candles, values) : ser.color };
  }
  return { time, value: v };
}
const seriesData = (ser, candles, values) => candles.map((_, i) => pointAt(ser, candles, values, i));

function addSeries(chart, spec, ser, pane) {
  const osc = spec.group === "osc";
  if (ser.kind === "hist") {
    const opts = { priceLineVisible: false, lastValueVisible: false };
    if (ser.overlayScale) opts.priceScaleId = ""; // skala sendiri di dasar chart harga
    const api = chart.addSeries(HistogramSeries, opts, pane);
    if (ser.overlayScale) api.priceScale().applyOptions({ scaleMargins: { top: 0.82, bottom: 0 } });
    return api;
  }
  const opts = {
    color: ser.color,
    lineWidth: ser.width ?? 1,
    lineStyle: ser.dotted ? LineStyle.Dotted : LineStyle.Solid,
    lineVisible: ser.kind !== "dots",
    pointMarkersVisible: ser.kind === "dots",
    pointMarkersRadius: 2,
    priceLineVisible: false,
    lastValueVisible: osc,
    crosshairMarkerVisible: false,
  };
  if (spec.range) {
    opts.autoscaleInfoProvider = () => ({ priceRange: { minValue: spec.range[0], maxValue: spec.range[1] } });
  }
  return chart.addSeries(LineSeries, opts, pane);
}

/**
 * Props
 *  data      respons /candles (candles + timeframe). Indikator dihitung di sini, bukan memakai data.indicators.
 *  active    Set id indikator (lihat lib/indicators.js)
 *  live      { symbol, interval } untuk update real-time dari Binance, atau null
 *  onPrice   dipanggil dengan harga terakhir setiap ada tick live
 *  onStatus  dipanggil dengan "connecting" | "live" | "offline" (null saat live dihentikan)
 */
export default function PriceChart({ data, active, assetType, currency, live, onPrice, onStatus }) {
  const containerRef = useRef(null);
  const chartRef = useRef(null); // { chart, candleSeries, entries, specs, ctx }
  const modelRef = useRef(null); // { source, candles, outputs } - sumber kebenaran untuk candle live
  const savedRange = useRef(null);
  const cb = useRef({});
  cb.current = { onPrice, onStatus };

  const [model, setModel] = useState(null); // untuk readout
  const [hover, setHover] = useState(null); // waktu candle yang di-hover (null = candle terakhir)

  const hasCandles = data?.candles?.length > 0;
  const ctx = { assetType, timeframe: data?.timeframe };
  const oscCount = pickSpecs(active, ctx).filter((s) => s.group === "osc").length;
  const height = MAIN_HEIGHT + oscCount * PANE_HEIGHT;

  // 1) Bangun chart. Dijalankan ulang saat data baru datang atau indikator diubah.
  useEffect(() => {
    const el = containerRef.current;
    if (!el || !hasCandles) return;

    if (modelRef.current?.source !== data) {
      modelRef.current = { source: data, candles: data.candles.map((c) => ({ ...c })), outputs: {} };
    }
    const m = modelRef.current;
    const specs = pickSpecs(active, ctx);
    m.outputs = computeOutputs(specs, m.candles, ctx);

    const up = cssVar("--color-up", "#86efac");
    const down = cssVar("--color-down", "#ff5a5f");
    const muted = cssVar("--color-muted", "#8d96a8");
    const line = cssVar("--color-line", "#2a3345");
    const dateOnly = data.timeframe === "1d";

    const chart = createChart(el, {
      autoSize: true,
      layout: {
        background: { type: ColorType.Solid, color: "transparent" },
        textColor: muted,
        fontFamily: "Inter, system-ui, sans-serif",
        fontSize: 11,
        panes: { separatorColor: line },
      },
      grid: { vertLines: { color: line }, horzLines: { color: line } },
      rightPriceScale: { borderColor: line },
      timeScale: {
        borderColor: line,
        timeVisible: true,
        secondsVisible: false,
        // time dari backend = detik UTC; tampilkan dalam jam lokal browser
        tickMarkFormatter: (time, type) => {
          const d = new Date(Number(time) * 1000);
          if (type === TickMarkType.Time || type === TickMarkType.TimeWithSeconds) return hhmm(d);
          if (type === TickMarkType.DayOfMonth) return d.toLocaleDateString("en-GB", { day: "numeric", month: "short" });
          if (type === TickMarkType.Month) return d.toLocaleDateString("en-GB", { month: "short" });
          return String(d.getFullYear());
        },
      },
      localization: {
        timeFormatter: (time) => {
          const d = new Date(Number(time) * 1000);
          const date = d.toLocaleDateString("en-GB", { day: "2-digit", month: "short" });
          return dateOnly ? date : `${date} ${hhmm(d)}`;
        },
      },
    });

    const candleSeries = chart.addSeries(
      CandlestickSeries,
      { upColor: up, downColor: down, borderUpColor: up, borderDownColor: down, wickUpColor: up, wickDownColor: down },
      0
    );
    candleSeries.setData(m.candles.map(toCandlePoint));

    const entries = [];
    const addSpec = (spec, pane) => {
      const apis = spec.series.map((ser) => {
        const api = addSeries(chart, spec, ser, pane);
        api.setData(seriesData(ser, m.candles, m.outputs[spec.id][ser.key]));
        entries.push({ spec, ser, api });
        return api;
      });
      if (spec.group === "osc") {
        const first = apis.find((_, i) => spec.series[i].kind !== "hist") ?? apis[0];
        first.priceScale().applyOptions({ scaleMargins: { top: 0.1, bottom: 0.1 } });
        for (const price of spec.levels ?? []) {
          first.createPriceLine({ price, color: muted, lineWidth: 1, lineStyle: LineStyle.Dashed, axisLabelVisible: true, title: "" });
        }
      }
    };

    specs.filter((s) => s.group === "overlay").forEach((s) => addSpec(s, 0));
    specs.filter((s) => s.group === "osc").forEach((s, idx) => addSpec(s, idx + 1));

    // proporsi tinggi sama dengan MAIN_HEIGHT / PANE_HEIGHT
    chart.panes().forEach((p, i) => p.setStretchFactor(i === 0 ? MAIN_HEIGHT : PANE_HEIGHT));

    // Pulihkan zoom/geser hanya kalau pengguna memang mengubahnya; selain itu tampilkan semua candle.
    // (Menyimpan rentang dari chart yang belum disentuh akan memulihkan rentang yang belum ter-layout.)
    const saved = savedRange.current;
    let moved = false;
    if (saved?.source === data && saved.range) {
      chart.timeScale().setVisibleLogicalRange(saved.range);
      moved = true;
    } else {
      chart.timeScale().fitContent();
    }
    const markMoved = () => {
      moved = true;
    };
    el.addEventListener("wheel", markMoved, { passive: true });
    el.addEventListener("pointerdown", markMoved);

    chart.subscribeCrosshairMove((p) => setHover(p.time != null && p.point ? Number(p.time) : null));

    chartRef.current = { chart, candleSeries, entries, specs, ctx };
    setModel({ candles: m.candles, outputs: m.outputs, specs, timeframe: data.timeframe });

    return () => {
      el.removeEventListener("wheel", markMoved);
      el.removeEventListener("pointerdown", markMoved);
      savedRange.current = moved ? { source: data, range: chart.timeScale().getVisibleLogicalRange() } : null;
      chart.remove();
      chartRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data, active, assetType, hasCandles]);

  // 2) Update real-time dari Binance. Hanya menyentuh candle terakhir; semua indikator dihitung ulang
  //    dari array candle yang sama, sehingga RSI/MACD/EMA/dst ikut bergerak.
  useEffect(() => {
    if (!live || !hasCandles) return;
    const { symbol, interval } = live;
    const step = TF_SECONDS[interval];
    let stopped = false;
    let busy = false; // selama sinkronisasi REST berjalan, pesan WebSocket diabaikan

    const redraw = (full) => {
      const ref = chartRef.current;
      const m = modelRef.current;
      if (!ref || !m) return;
      m.outputs = computeOutputs(ref.specs, m.candles, ref.ctx);
      const last = m.candles.length - 1;
      if (full) {
        ref.candleSeries.setData(m.candles.map(toCandlePoint));
        for (const e of ref.entries) e.api.setData(seriesData(e.ser, m.candles, m.outputs[e.spec.id][e.ser.key]));
      } else {
        ref.candleSeries.update(toCandlePoint(m.candles[last]));
        for (const e of ref.entries) e.api.update(pointAt(e.ser, m.candles, m.outputs[e.spec.id][e.ser.key], last));
      }
      setModel({ candles: m.candles, outputs: m.outputs, specs: ref.specs, timeframe: ref.ctx.timeframe });
      cb.current.onPrice?.(m.candles[last].close);
    };

    // Candle terakhir dari backend bisa tertinggal (ingestion per jam) atau ada yang terlewat saat
    // koneksi putus: ambil ulang dari Binance mulai candle terakhir dan gabungkan.
    const sync = async () => {
      const m = modelRef.current;
      if (!m || busy) return;
      busy = true;
      try {
        const fresh = await fetchKlines(symbol, interval, m.candles[m.candles.length - 1].time * 1000);
        if (stopped || modelRef.current !== m || !fresh.length) return;
        m.candles = mergeCandles(m.candles, fresh);
        redraw(true);
      } catch {
        /* REST gagal: tetap lanjut dengan WebSocket saja */
      } finally {
        busy = false;
      }
    };

    const onKline = (k) => {
      const m = modelRef.current;
      if (!m || busy) return;
      const bar = klineToCandle(k);
      const last = m.candles[m.candles.length - 1];
      if (bar.time === last.time) {
        m.candles[m.candles.length - 1] = bar;
        redraw(false);
      } else if (bar.time === last.time + step) {
        m.candles.push(bar);
        redraw(false);
      } else if (bar.time > last.time + step) {
        sync(); // ada candle yang terlewat
      }
      // bar.time < last.time: pesan terlambat, abaikan
    };

    const stop = connectKline(symbol, interval, {
      onOpen: sync,
      onStatus: (s) => cb.current.onStatus?.(s),
      onKline,
    });
    return () => {
      stopped = true;
      stop();
      cb.current.onStatus?.(null);
    };
  }, [live?.symbol, live?.interval, data, hasCandles]); // eslint-disable-line react-hooks/exhaustive-deps

  // 3) Khusus development: bandingkan indikator hitungan browser dengan data.indicators dari backend,
  //    supaya terlihat apakah rumusnya sama. Lihat tab Console di DevTools.
  useEffect(() => {
    if (!import.meta.env.DEV || !data?.indicators || !hasCandles) return;
    const dev = { assetType, timeframe: data.timeframe };
    const pairs = [
      ["rsi14", "rsi", "rsi"], ["sma20", "sma", "sma20"], ["sma50", "sma", "sma50"],
      ["ema20", "ema", "ema20"], ["ema50", "ema", "ema50"],
      ["macd.line", "macd", "line"], ["macd.signal", "macd", "signal"], ["macd.hist", "macd", "hist"],
      ["bollinger.upper", "bollinger", "upper"], ["bollinger.middle", "bollinger", "middle"], ["bollinger.lower", "bollinger", "lower"],
    ];
    const pick = (path) => path.split(".").reduce((o, k) => o?.[k], data.indicators);
    const rows = pairs.map(([path, id, key]) => {
      const theirs = pick(path);
      const mine = INDICATOR_BY_ID[id].compute(data.candles, dev)[key];
      let n = 0;
      let max = 0;
      theirs?.forEach((t, i) => {
        if (t != null && mine[i] != null) {
          n++;
          max = Math.max(max, Math.abs(t - mine[i]) / (Math.abs(t) || 1));
        }
      });
      return { indikator: path, dibandingkan: n, selisih_relatif_maks: Number(max.toExponential(2)) };
    });
    console.info("[dev] indikator browser vs backend (selisih ~0 = rumus sama)");
    console.table(rows);
  }, [data, assetType, hasCandles]);

  if (!hasCandles) {
    return <p className="p-6 text-sm text-muted">Tidak ada candle pada rentang ini.</p>;
  }

  return (
    <div>
      <ChartReadout model={model} hover={hover} currency={currency} />
      <div ref={containerRef} style={{ height }} />
      <div className="flex items-center border-t border-line px-4 py-2 font-sans text-xs text-muted">
        <span>
          {model?.candles.length ?? data.candles.length} candle · {data.timeframe}
        </span>
        {live && <span className="ml-auto">Update real-time dari Binance</span>}
      </div>
    </div>
  );
}

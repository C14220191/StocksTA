// Indikator teknikal dihitung di browser dari array candle yang sama dengan yang digambar chart.
// Dengan begitu semuanya ikut berubah saat candle terakhir bergerak lewat WebSocket.
//
// Semua fungsi menerima array dan mengembalikan array dengan panjang SAMA (null = belum cukup data).
// Rumus mengikuti definisi standar: RSI/ATR/ADX memakai Wilder smoothing, EMA di-seed dengan SMA,
// Bollinger memakai standar deviasi populasi.

const nulls = (n) => new Array(n).fill(null);

const firstIdx = (a) => {
  for (let i = 0; i < a.length; i++) if (a[i] != null) return i;
  return -1;
};

// Untuk indikator yang ditumpuk (mis. signal MACD = EMA dari garis MACD yang diawali null):
// jalankan fn hanya pada bagian yang sudah terisi, lalu geser kembali.
function onValid(values, fn) {
  const s = firstIdx(values);
  const out = nulls(values.length);
  if (s < 0) return out;
  const r = fn(values.slice(s));
  for (let i = 0; i < r.length; i++) out[s + i] = r[i];
  return out;
}

function smaCore(v, n) {
  const out = nulls(v.length);
  let sum = 0;
  for (let i = 0; i < v.length; i++) {
    sum += v[i];
    if (i >= n) sum -= v[i - n];
    if (i >= n - 1) out[i] = sum / n;
  }
  return out;
}

function emaCore(v, n) {
  const out = nulls(v.length);
  if (v.length < n) return out;
  const k = 2 / (n + 1);
  let prev = 0;
  for (let i = 0; i < n; i++) prev += v[i];
  prev /= n;
  out[n - 1] = prev;
  for (let i = n; i < v.length; i++) {
    prev = v[i] * k + prev * (1 - k);
    out[i] = prev;
  }
  return out;
}

// Wilder's smoothing (RMA)
function rmaCore(v, n) {
  const out = nulls(v.length);
  if (v.length < n) return out;
  let prev = 0;
  for (let i = 0; i < n; i++) prev += v[i];
  prev /= n;
  out[n - 1] = prev;
  for (let i = n; i < v.length; i++) {
    prev = (prev * (n - 1) + v[i]) / n;
    out[i] = prev;
  }
  return out;
}

export const sma = (v, n) => onValid(v, (x) => smaCore(x, n));
export const ema = (v, n) => onValid(v, (x) => emaCore(x, n));
export const rma = (v, n) => onValid(v, (x) => rmaCore(x, n));

const highest = (v, i, n) => {
  let m = -Infinity;
  for (let j = i - n + 1; j <= i; j++) if (v[j] > m) m = v[j];
  return m;
};
const lowest = (v, i, n) => {
  let m = Infinity;
  for (let j = i - n + 1; j <= i; j++) if (v[j] < m) m = v[j];
  return m;
};

export function trueRange(h, l, c) {
  return h.map((_, i) =>
    i === 0 ? h[i] - l[i] : Math.max(h[i] - l[i], Math.abs(h[i] - c[i - 1]), Math.abs(l[i] - c[i - 1]))
  );
}

export const atr = (h, l, c, n = 14) => rma(trueRange(h, l, c), n);

const rsiValue = (gain, loss) => (loss === 0 ? (gain === 0 ? 50 : 100) : 100 - 100 / (1 + gain / loss));

export function rsi(close, n = 14) {
  const out = nulls(close.length);
  if (close.length <= n) return out;
  let gain = 0;
  let loss = 0;
  for (let i = 1; i <= n; i++) {
    const d = close[i] - close[i - 1];
    if (d > 0) gain += d;
    else loss -= d;
  }
  gain /= n;
  loss /= n;
  out[n] = rsiValue(gain, loss);
  for (let i = n + 1; i < close.length; i++) {
    const d = close[i] - close[i - 1];
    gain = (gain * (n - 1) + Math.max(d, 0)) / n;
    loss = (loss * (n - 1) + Math.max(-d, 0)) / n;
    out[i] = rsiValue(gain, loss);
  }
  return out;
}

export function macd(close, fast = 12, slow = 26, signalN = 9) {
  const f = ema(close, fast);
  const s = ema(close, slow);
  const line = close.map((_, i) => (f[i] == null || s[i] == null ? null : f[i] - s[i]));
  const signal = ema(line, signalN);
  const hist = line.map((v, i) => (v == null || signal[i] == null ? null : v - signal[i]));
  return { line, signal, hist };
}

export function bollinger(close, n = 20, mult = 2) {
  const mid = sma(close, n);
  const upper = nulls(close.length);
  const lower = nulls(close.length);
  for (let i = n - 1; i < close.length; i++) {
    let s = 0;
    for (let j = i - n + 1; j <= i; j++) s += (close[j] - mid[i]) ** 2;
    const sd = Math.sqrt(s / n);
    upper[i] = mid[i] + mult * sd;
    lower[i] = mid[i] - mult * sd;
  }
  return { upper, middle: mid, lower };
}

export function keltner(h, l, c, emaN = 20, atrN = 10, mult = 2) {
  const mid = ema(c, emaN);
  const a = atr(h, l, c, atrN);
  return {
    upper: mid.map((m, i) => (m == null || a[i] == null ? null : m + mult * a[i])),
    middle: mid.map((m, i) => (m == null || a[i] == null ? null : m)),
    lower: mid.map((m, i) => (m == null || a[i] == null ? null : m - mult * a[i])),
  };
}

export function donchian(h, l, n = 20) {
  const upper = nulls(h.length);
  const lower = nulls(h.length);
  const middle = nulls(h.length);
  for (let i = n - 1; i < h.length; i++) {
    upper[i] = highest(h, i, n);
    lower[i] = lowest(l, i, n);
    middle[i] = (upper[i] + lower[i]) / 2;
  }
  return { upper, middle, lower };
}

// VWAP direset tiap hari. Saham: hari dihitung dalam WIB (UTC+7), kripto: UTC.
export function vwap(candles, dayOffsetSec = 0) {
  const out = nulls(candles.length);
  let day = null;
  let pv = 0;
  let vol = 0;
  candles.forEach((c, i) => {
    const d = Math.floor((c.time + dayOffsetSec) / 86400);
    if (d !== day) {
      day = d;
      pv = 0;
      vol = 0;
    }
    pv += ((c.high + c.low + c.close) / 3) * c.volume;
    vol += c.volume;
    out[i] = vol > 0 ? pv / vol : (c.high + c.low + c.close) / 3;
  });
  return out;
}

// Parabolic SAR (Wilder). up = titik di bawah harga (tren naik), down = titik di atas harga.
// Urutan langkah mengikuti ta.sar milik TradingView: saat reversal, SAR baru = ekstrem (termasuk bar
// yang sedang berjalan), dan penjepitan oleh low/high dua bar sebelumnya dilakukan di akhir.
export function psar(h, l, c, start = 0.02, inc = 0.02, maxAf = 0.2) {
  const n = h.length;
  const up = nulls(n);
  const down = nulls(n);
  let result = 0;
  let extreme = 0;
  let acc = start;
  let below = false;
  for (let i = 1; i < n; i++) {
    let firstBar = false;
    if (i === 1) {
      if (c[1] > c[0]) {
        below = true;
        extreme = h[1];
        result = l[0];
      } else {
        below = false;
        extreme = l[1];
        result = h[0];
      }
      firstBar = true;
      acc = start;
    }
    result += acc * (extreme - result);
    if (below) {
      if (result > l[i]) {
        firstBar = true;
        below = false;
        result = Math.max(h[i], extreme);
        extreme = l[i];
        acc = start;
      }
    } else if (result < h[i]) {
      firstBar = true;
      below = true;
      result = Math.min(l[i], extreme);
      extreme = h[i];
      acc = start;
    }
    if (!firstBar) {
      if (below) {
        if (h[i] > extreme) {
          extreme = h[i];
          acc = Math.min(acc + inc, maxAf);
        }
      } else if (l[i] < extreme) {
        extreme = l[i];
        acc = Math.min(acc + inc, maxAf);
      }
    }
    if (below) {
      result = Math.min(result, l[i - 1]);
      if (i > 1) result = Math.min(result, l[i - 2]);
    } else {
      result = Math.max(result, h[i - 1]);
      if (i > 1) result = Math.max(result, h[i - 2]);
    }
    if (below) up[i] = result;
    else down[i] = result;
  }
  return { up, down };
}

export function supertrend(h, l, c, n = 10, mult = 3) {
  const len = h.length;
  const up = nulls(len); // garis hijau (tren naik)
  const down = nulls(len); // garis merah (tren turun)
  const a = atr(h, l, c, n);
  let prevUp = null;
  let prevDn = null;
  let trend = 1;
  for (let i = 0; i < len; i++) {
    if (a[i] == null) continue;
    const hl2 = (h[i] + l[i]) / 2;
    let upper = hl2 - mult * a[i];
    let lower = hl2 + mult * a[i];
    if (prevUp != null) {
      if (c[i - 1] > prevUp) upper = Math.max(upper, prevUp);
      if (c[i - 1] < prevDn) lower = Math.min(lower, prevDn);
      if (trend === -1 && c[i] > prevDn) trend = 1;
      else if (trend === 1 && c[i] < prevUp) trend = -1;
    }
    if (trend === 1) up[i] = upper;
    else down[i] = lower;
    prevUp = upper;
    prevDn = lower;
  }
  return { up, down };
}

export function stochastic(h, l, c, kN = 14, smoothK = 3, dN = 3) {
  const raw = nulls(c.length);
  for (let i = kN - 1; i < c.length; i++) {
    const hh = highest(h, i, kN);
    const ll = lowest(l, i, kN);
    raw[i] = hh === ll ? 50 : ((c[i] - ll) / (hh - ll)) * 100;
  }
  const k = smoothK > 1 ? sma(raw, smoothK) : raw;
  return { k, d: sma(k, dN) };
}

export function stochRsi(close, rsiN = 14, stochN = 14, kN = 3, dN = 3) {
  const r = rsi(close, rsiN);
  const raw = onValid(r, (v) => {
    const out = nulls(v.length);
    for (let i = stochN - 1; i < v.length; i++) {
      const hh = highest(v, i, stochN);
      const ll = lowest(v, i, stochN);
      out[i] = hh === ll ? 0 : ((v[i] - ll) / (hh - ll)) * 100;
    }
    return out;
  });
  const k = kN > 1 ? sma(raw, kN) : raw;
  return { k, d: sma(k, dN) };
}

export function cci(h, l, c, n = 20) {
  const tp = c.map((_, i) => (h[i] + l[i] + c[i]) / 3);
  const mean = sma(tp, n);
  const out = nulls(c.length);
  for (let i = n - 1; i < c.length; i++) {
    let dev = 0;
    for (let j = i - n + 1; j <= i; j++) dev += Math.abs(tp[j] - mean[i]);
    dev /= n;
    out[i] = dev === 0 ? 0 : (tp[i] - mean[i]) / (0.015 * dev);
  }
  return out;
}

export function williamsR(h, l, c, n = 14) {
  const out = nulls(c.length);
  for (let i = n - 1; i < c.length; i++) {
    const hh = highest(h, i, n);
    const ll = lowest(l, i, n);
    out[i] = hh === ll ? -50 : ((hh - c[i]) / (hh - ll)) * -100;
  }
  return out;
}

export function adx(h, l, c, n = 14) {
  const len = c.length;
  const pdi = nulls(len);
  const mdi = nulls(len);
  const adxOut = nulls(len);
  if (len <= n) return { adx: adxOut, pdi, mdi };
  const tr = trueRange(h, l, c);
  const pdm = nulls(len);
  const mdm = nulls(len);
  for (let i = 1; i < len; i++) {
    const upMove = h[i] - h[i - 1];
    const dnMove = l[i - 1] - l[i];
    pdm[i] = upMove > dnMove && upMove > 0 ? upMove : 0;
    mdm[i] = dnMove > upMove && dnMove > 0 ? dnMove : 0;
  }
  let sTr = 0;
  let sP = 0;
  let sM = 0;
  const dx = nulls(len);
  for (let i = 1; i < len; i++) {
    if (i <= n) {
      sTr += tr[i];
      sP += pdm[i];
      sM += mdm[i];
    } else {
      sTr = sTr - sTr / n + tr[i];
      sP = sP - sP / n + pdm[i];
      sM = sM - sM / n + mdm[i];
    }
    if (i >= n) {
      pdi[i] = sTr === 0 ? 0 : (100 * sP) / sTr;
      mdi[i] = sTr === 0 ? 0 : (100 * sM) / sTr;
      const sum = pdi[i] + mdi[i];
      dx[i] = sum === 0 ? 0 : (100 * Math.abs(pdi[i] - mdi[i])) / sum;
    }
  }
  let prev = null;
  for (let i = 2 * n - 1; i < len; i++) {
    if (i === 2 * n - 1) {
      let s = 0;
      for (let j = n; j <= i; j++) s += dx[j];
      prev = s / n;
    } else {
      prev = (prev * (n - 1) + dx[i]) / n;
    }
    adxOut[i] = prev;
  }
  return { adx: adxOut, pdi, mdi };
}

export function obv(c, v) {
  const out = nulls(c.length);
  let acc = 0;
  for (let i = 0; i < c.length; i++) {
    if (i > 0) acc += c[i] > c[i - 1] ? v[i] : c[i] < c[i - 1] ? -v[i] : 0;
    out[i] = acc;
  }
  return out;
}

export function mfi(h, l, c, v, n = 14) {
  const out = nulls(c.length);
  const tp = c.map((_, i) => (h[i] + l[i] + c[i]) / 3);
  for (let i = n; i < c.length; i++) {
    let pos = 0;
    let neg = 0;
    for (let j = i - n + 1; j <= i; j++) {
      const flow = tp[j] * v[j];
      if (tp[j] > tp[j - 1]) pos += flow;
      else if (tp[j] < tp[j - 1]) neg += flow;
    }
    out[i] = neg === 0 ? 100 : 100 - 100 / (1 + pos / neg);
  }
  return out;
}

export function roc(c, n = 12) {
  return c.map((v, i) => (i < n || c[i - n] === 0 ? null : ((v - c[i - n]) / c[i - n]) * 100));
}

export function awesomeOscillator(h, l, fast = 5, slow = 34) {
  const mid = h.map((v, i) => (v + l[i]) / 2);
  const f = sma(mid, fast);
  const s = sma(mid, slow);
  return mid.map((_, i) => (f[i] == null || s[i] == null ? null : f[i] - s[i]));
}

// ---------------------------------------------------------------------------------------------
// Registry: dipakai oleh PriceChart (menggambar), IndicatorPicker (chip), dan ChartReadout (nilai).
//   group "overlay" = digambar di chart harga, "osc" = pane sendiri di bawah chart harga.
//   series[].kind: "line" | "dots" | "hist"; unit: "price" | "num" | "vol" (untuk format angka).
//   levels: garis bantu horizontal; range: skala tetap pane; unavailable(ctx): alasan jika tidak bisa dipakai.
// ---------------------------------------------------------------------------------------------
const UP_A = "rgba(134,239,172,0.55)";
const DOWN_A = "rgba(255,90,95,0.55)";
const UP = "#86efac";
const DOWN = "#ff5a5f";

const sign = (v) => (v >= 0 ? UP_A : DOWN_A);

const col = (candles) => ({
  open: candles.map((c) => c.open),
  high: candles.map((c) => c.high),
  low: candles.map((c) => c.low),
  close: candles.map((c) => c.close),
  volume: candles.map((c) => c.volume),
});

export const INDICATORS = [
  // ---- Overlay -------------------------------------------------------------------------------
  {
    id: "volume", label: "Volume", group: "overlay", params: "",
    compute: (c) => ({ volume: col(c).volume }),
    series: [{ key: "volume", label: "Vol", kind: "hist", unit: "vol", color: UP_A, overlayScale: true,
      colorFn: (v, i, c) => (c[i].close >= c[i].open ? UP_A : DOWN_A) }],
  },
  {
    id: "sma", label: "SMA", group: "overlay", params: "20, 50",
    compute: (c) => { const { close } = col(c); return { sma20: sma(close, 20), sma50: sma(close, 50) }; },
    series: [
      { key: "sma20", label: "SMA 20", color: "#e879f9", unit: "price" },
      { key: "sma50", label: "SMA 50", color: "#2dd4bf", unit: "price" },
    ],
  },
  {
    id: "ema", label: "EMA", group: "overlay", params: "20, 50",
    compute: (c) => { const { close } = col(c); return { ema20: ema(close, 20), ema50: ema(close, 50) }; },
    series: [
      { key: "ema20", label: "EMA 20", color: "#f5a524", unit: "price" },
      { key: "ema50", label: "EMA 50", color: "#60a5fa", unit: "price" },
    ],
  },
  {
    id: "bollinger", label: "Bollinger", group: "overlay", params: "20, 2",
    compute: (c) => bollinger(col(c).close, 20, 2),
    series: [
      { key: "upper", label: "Atas", color: "#a78bfa", unit: "price" },
      { key: "middle", label: "Tengah", color: "#a78bfa", unit: "price", dotted: true },
      { key: "lower", label: "Bawah", color: "#a78bfa", unit: "price" },
    ],
  },
  {
    id: "keltner", label: "Keltner", group: "overlay", params: "EMA 20, ATR 10 x2",
    compute: (c) => { const { high, low, close } = col(c); return keltner(high, low, close, 20, 10, 2); },
    series: [
      { key: "upper", label: "Atas", color: "#f472b6", unit: "price" },
      { key: "middle", label: "Tengah", color: "#f472b6", unit: "price", dotted: true },
      { key: "lower", label: "Bawah", color: "#f472b6", unit: "price" },
    ],
  },
  {
    id: "donchian", label: "Donchian", group: "overlay", params: "20",
    compute: (c) => { const { high, low } = col(c); return donchian(high, low, 20); },
    series: [
      { key: "upper", label: "Atas", color: "#94a3b8", unit: "price" },
      { key: "middle", label: "Tengah", color: "#94a3b8", unit: "price", dotted: true },
      { key: "lower", label: "Bawah", color: "#94a3b8", unit: "price" },
    ],
  },
  {
    id: "vwap", label: "VWAP", group: "overlay", params: "harian",
    unavailable: (ctx) => (ctx.timeframe === "1d" ? "VWAP hanya untuk candle intraday" : null),
    compute: (c, ctx) => ({ vwap: vwap(c, ctx.assetType === "stock" ? 7 * 3600 : 0) }),
    series: [{ key: "vwap", label: "VWAP", color: "#fde047", unit: "price", width: 2 }],
  },
  {
    id: "psar", label: "Parabolic SAR", group: "overlay", params: "0.02, 0.2", exclusive: true,
    compute: (c) => { const { high, low, close } = col(c); return psar(high, low, close, 0.02, 0.02, 0.2); },
    series: [
      { key: "up", label: "SAR naik", color: UP, kind: "dots", unit: "price" },
      { key: "down", label: "SAR turun", color: DOWN, kind: "dots", unit: "price" },
    ],
  },
  {
    id: "supertrend", label: "Supertrend", group: "overlay", params: "10, 3", exclusive: true,
    compute: (c) => { const { high, low, close } = col(c); return supertrend(high, low, close, 10, 3); },
    series: [
      { key: "up", label: "Tren naik", color: UP, unit: "price", width: 2 },
      { key: "down", label: "Tren turun", color: DOWN, unit: "price", width: 2 },
    ],
  },

  // ---- Osilator (pane sendiri) ---------------------------------------------------------------
  {
    id: "rsi", label: "RSI", group: "osc", params: "14", levels: [70, 30], range: [0, 100],
    compute: (c) => ({ rsi: rsi(col(c).close, 14) }),
    series: [{ key: "rsi", label: "RSI", color: "#38bdf8", unit: "num", width: 2 }],
  },
  {
    id: "macd", label: "MACD", group: "osc", params: "12, 26, 9", levels: [0],
    compute: (c) => macd(col(c).close, 12, 26, 9),
    series: [
      { key: "hist", label: "Hist", kind: "hist", color: UP_A, unit: "num", colorFn: (v) => sign(v) },
      { key: "line", label: "MACD", color: "#38bdf8", unit: "num" },
      { key: "signal", label: "Signal", color: "#f5a524", unit: "num" },
    ],
  },
  {
    id: "stoch", label: "Stochastic", group: "osc", params: "14, 3, 3", levels: [80, 20], range: [0, 100],
    compute: (c) => { const { high, low, close } = col(c); return stochastic(high, low, close, 14, 3, 3); },
    series: [
      { key: "k", label: "%K", color: "#38bdf8", unit: "num" },
      { key: "d", label: "%D", color: "#f5a524", unit: "num" },
    ],
  },
  {
    id: "stochrsi", label: "Stoch RSI", group: "osc", params: "14, 14, 3, 3", levels: [80, 20], range: [0, 100],
    compute: (c) => stochRsi(col(c).close, 14, 14, 3, 3),
    series: [
      { key: "k", label: "%K", color: "#a78bfa", unit: "num" },
      { key: "d", label: "%D", color: "#f5a524", unit: "num" },
    ],
  },
  {
    id: "cci", label: "CCI", group: "osc", params: "20", levels: [100, -100],
    compute: (c) => { const { high, low, close } = col(c); return { cci: cci(high, low, close, 20) }; },
    series: [{ key: "cci", label: "CCI", color: "#f472b6", unit: "num" }],
  },
  {
    id: "willr", label: "Williams %R", group: "osc", params: "14", levels: [-20, -80], range: [-100, 0],
    compute: (c) => { const { high, low, close } = col(c); return { willr: williamsR(high, low, close, 14) }; },
    series: [{ key: "willr", label: "%R", color: "#2dd4bf", unit: "num" }],
  },
  {
    id: "atr", label: "ATR", group: "osc", params: "14",
    compute: (c) => { const { high, low, close } = col(c); return { atr: atr(high, low, close, 14) }; },
    series: [{ key: "atr", label: "ATR", color: "#fb923c", unit: "price" }],
  },
  {
    id: "adx", label: "ADX / DMI", group: "osc", params: "14", levels: [25],
    compute: (c) => { const { high, low, close } = col(c); return adx(high, low, close, 14); },
    series: [
      { key: "adx", label: "ADX", color: "#e2e8f0", unit: "num", width: 2 },
      { key: "pdi", label: "+DI", color: UP, unit: "num" },
      { key: "mdi", label: "-DI", color: DOWN, unit: "num" },
    ],
  },
  {
    id: "obv", label: "OBV", group: "osc", params: "",
    compute: (c) => { const { close, volume } = col(c); return { obv: obv(close, volume) }; },
    series: [{ key: "obv", label: "OBV", color: "#60a5fa", unit: "vol" }],
  },
  {
    id: "mfi", label: "MFI", group: "osc", params: "14", levels: [80, 20], range: [0, 100],
    compute: (c) => { const { high, low, close, volume } = col(c); return { mfi: mfi(high, low, close, volume, 14) }; },
    series: [{ key: "mfi", label: "MFI", color: "#2dd4bf", unit: "num" }],
  },
  {
    id: "roc", label: "ROC", group: "osc", params: "12", levels: [0],
    compute: (c) => ({ roc: roc(col(c).close, 12) }),
    series: [{ key: "roc", label: "ROC %", color: "#a78bfa", unit: "num" }],
  },
  {
    id: "ao", label: "Awesome Osc.", group: "osc", params: "5, 34", levels: [0],
    compute: (c) => { const { high, low } = col(c); return { ao: awesomeOscillator(high, low, 5, 34) }; },
    series: [{ key: "ao", label: "AO", kind: "hist", color: UP_A, unit: "num",
      colorFn: (v, i, c, values) => (i > 0 && values[i - 1] != null && v < values[i - 1] ? DOWN_A : UP_A) }],
  },
];

export const INDICATOR_BY_ID = Object.fromEntries(INDICATORS.map((s) => [s.id, s]));

export const OVERLAYS = INDICATORS.filter((s) => s.group === "overlay");
export const OSCILLATORS = INDICATORS.filter((s) => s.group === "osc");

export function computeOutputs(specs, candles, ctx) {
  const out = {};
  for (const s of specs) out[s.id] = s.compute(candles, ctx);
  return out;
}

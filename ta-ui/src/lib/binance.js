// Data real-time instrument Binance langsung dari browser (public market data, tanpa API key).
// Hanya untuk tampilan: tidak melewati backend, cache, maupun AI, jadi tidak memengaruhi
// pengukuran latency / token cost di inference_log.
//
// market = "spot" (kripto, source binance) atau "futures" (USDⓈ-M, source binance_futures, mis. XAUUSDT).
const trim = (u) => u.replace(/\/$/, "");
const ENDPOINTS = {
  spot: {
    ws: trim(import.meta.env.VITE_BINANCE_WS_URL || "wss://stream.binance.com:9443"),
    klines: trim(import.meta.env.VITE_BINANCE_REST_URL || "https://api.binance.com") + "/api/v3/klines",
  },
  futures: {
    // kline/miniTicker futures hanya dikirim lewat path /market (path lama tersambung tapi tanpa data)
    ws: trim(import.meta.env.VITE_BINANCE_FUTURES_WS_URL || "wss://fstream.binance.com/market"),
    klines: trim(import.meta.env.VITE_BINANCE_FUTURES_REST_URL || "https://fapi.binance.com") + "/fapi/v1/klines",
  },
};

// source backend (kolom `source` di respons instrument) -> market Binance; null = tidak ada data live (saham).
export function liveMarket(source) {
  if (source === "binance") return "spot";
  if (source === "binance_futures") return "futures";
  return null;
}

// Interval candle yang didukung UI = interval bawaan Binance, jadi nama `tf` backend bisa dipakai langsung.
export const TF_SECONDS = { "1h": 3600, "2h": 7200, "4h": 14400, "6h": 21600, "1d": 86400 };

// WebSocket dengan reconnect (exponential backoff, maks 30 detik).
// onStatus: "connecting" | "live" | "offline"
function connect(url, { onOpen, onMessage, onStatus }) {
  let ws;
  let stopped = false;
  let attempt = 0;
  let timer;

  const open = () => {
    onStatus?.("connecting");
    ws = new WebSocket(url);
    ws.onopen = () => {
      attempt = 0;
      onStatus?.("live");
      onOpen?.();
    };
    ws.onmessage = (e) => {
      try {
        onMessage(JSON.parse(e.data));
      } catch {
        /* abaikan pesan yang bukan JSON */
      }
    };
    ws.onclose = () => {
      if (stopped) return;
      onStatus?.("offline");
      timer = setTimeout(open, Math.min(30000, 1000 * 2 ** attempt++));
    };
    ws.onerror = () => ws.close();
  };

  open();
  return () => {
    stopped = true;
    clearTimeout(timer);
    if (ws) {
      ws.onclose = null;
      ws.close();
    }
  };
}

// Candle berjalan satu simbol. onKline menerima field `k` dari event kline.
export function connectKline(symbol, interval, { market = "spot", onKline, onOpen, onStatus }) {
  return connect(`${ENDPOINTS[market].ws}/ws/${symbol.toLowerCase()}@kline_${interval}`, {
    onOpen,
    onStatus,
    onMessage: (msg) => {
      const k = msg?.k ?? msg?.data?.k;
      if (k) onKline(k);
    },
  });
}

// Harga terakhir beberapa simbol sekaligus (satu koneksi, stream miniTicker ~1 pesan/detik/simbol).
export function connectTickers(symbols, { market = "spot", onPrice, onStatus }) {
  const streams = symbols.map((s) => `${s.toLowerCase()}@miniTicker`).join("/");
  return connect(`${ENDPOINTS[market].ws}/stream?streams=${streams}`, {
    onStatus,
    onMessage: (msg) => {
      const d = msg?.data;
      if (d?.s && d.c != null) onPrice(d.s, Number(d.c));
    },
  });
}

// Candle dari startMs sampai sekarang (maks 1000). Dipakai untuk menambal candle yang terlewat.
export async function fetchKlines(symbol, interval, startMs, signal, market = "spot") {
  const url = new URL(ENDPOINTS[market].klines);
  url.search = new URLSearchParams({
    symbol: symbol.toUpperCase(),
    interval,
    startTime: String(startMs),
    limit: "1000",
  }).toString();
  const res = await fetch(url, { signal });
  if (!res.ok) throw new Error(`Binance REST HTTP ${res.status}`);
  const rows = await res.json();
  const now = Date.now();
  return rows.map((r) => ({
    time: Math.floor(r[0] / 1000),
    open: Number(r[1]),
    high: Number(r[2]),
    low: Number(r[3]),
    close: Number(r[4]),
    volume: Number(r[5]),
    closed: r[6] < now,
  }));
}

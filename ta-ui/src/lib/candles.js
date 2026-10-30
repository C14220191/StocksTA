// Fungsi murni untuk array candle { time (detik UTC), open, high, low, close, volume, closed }.

// Pesan kline Binance (field k) -> candle
export function klineToCandle(k) {
  return {
    time: Math.floor(k.t / 1000),
    open: Number(k.o),
    high: Number(k.h),
    low: Number(k.l),
    close: Number(k.c),
    volume: Number(k.v),
    closed: !!k.x,
  };
}

// Candle baru (dari REST) menggantikan semua candle lama mulai dari waktu candle pertamanya.
export function mergeCandles(base, incoming) {
  if (!incoming.length) return base;
  const first = incoming[0].time;
  return base.filter((c) => c.time < first).concat(incoming);
}

// Binary search; mengembalikan -1 kalau tidak ada.
export function indexOfTime(candles, time) {
  let lo = 0;
  let hi = candles.length - 1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    const t = candles[mid].time;
    if (t === time) return mid;
    if (t < time) lo = mid + 1;
    else hi = mid - 1;
  }
  return -1;
}

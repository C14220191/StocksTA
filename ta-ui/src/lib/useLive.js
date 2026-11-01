import { useCallback, useEffect, useState } from "react";
import { connectTickers, liveMarket } from "./binance.js";

// Harga live untuk baris-baris Binance di tabel (Home, Search): kripto (spot) dan komoditas
// seperti XAUUSDT (futures). Saham tidak punya WebSocket gratis, jadi barisnya dibiarkan apa adanya.
//
// Perubahan % tetap memakai definisi backend (terhadap penutupan harian sebelumnya): penutupan
// sebelumnya diturunkan dari harga dan change_pct backend, lalu dipakai untuk menghitung ulang
// dengan harga live.
export default function useLivePrices(rows) {
  // "spot:BTCUSDT,ETHUSDT|futures:XAUUSDT" - satu koneksi per market
  const groups = {};
  for (const r of rows ?? []) {
    const market = liveMarket(r?.source);
    if (market) (groups[market] ??= new Set()).add(r.symbol);
  }
  const key = Object.keys(groups)
    .sort()
    .map((m) => `${m}:${[...groups[m]].sort().join(",")}`)
    .join("|");

  const [prices, setPrices] = useState({});
  const [status, setStatus] = useState("connecting");

  useEffect(() => {
    if (!key) return;
    let buffer = {};
    const statuses = {};
    const stops = key.split("|").map((part) => {
      const [market, list] = part.split(":");
      return connectTickers(list.split(","), {
        market,
        onPrice: (symbol, price) => {
          buffer[symbol] = price;
        },
        onStatus: (s) => {
          // Badge "live" hanya kalau semua koneksi tersambung
          statuses[market] = s;
          const all = Object.values(statuses);
          setStatus(all.every((x) => x === "live") ? "live" : all.includes("offline") ? "offline" : "connecting");
        },
      });
    });
    // Render paling cepat 1x per detik, bukan setiap pesan
    const timer = setInterval(() => {
      if (Object.keys(buffer).length) {
        const batch = buffer;
        buffer = {};
        setPrices((p) => ({ ...p, ...batch }));
      }
    }, 1000);
    return () => {
      stops.forEach((stop) => stop());
      clearInterval(timer);
    };
  }, [key]);

  const apply = useCallback(
    (row) => {
      const live = prices[row?.symbol];
      if (!liveMarket(row?.source) || live == null) return row;
      const out = { ...row, price: live };
      if (row.change_pct != null && row.change_pct > -100) {
        const prevClose = row.price / (1 + row.change_pct / 100);
        out.change_pct = (live / prevClose - 1) * 100;
      }
      return out;
    },
    [prices]
  );

  return { apply, status: key ? status : null };
}

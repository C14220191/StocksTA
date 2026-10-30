import { useCallback, useEffect, useState } from "react";
import { connectTickers } from "./binance.js";

// Harga live untuk baris-baris kripto di tabel (Home, Search). Saham tidak punya WebSocket gratis,
// jadi barisnya dibiarkan apa adanya.
//
// Perubahan % tetap memakai definisi backend (terhadap penutupan harian sebelumnya): penutupan
// sebelumnya diturunkan dari harga dan change_pct backend, lalu dipakai untuk menghitung ulang
// dengan harga live.
export default function useLivePrices(rows) {
  const symbols = [
    ...new Set((rows ?? []).filter((r) => r?.asset_type === "crypto").map((r) => r.symbol)),
  ].sort();
  const key = symbols.join(",");

  const [prices, setPrices] = useState({});
  const [status, setStatus] = useState("connecting");

  useEffect(() => {
    if (!key) return;
    let buffer = {};
    const stop = connectTickers(key.split(","), {
      onPrice: (symbol, price) => {
        buffer[symbol] = price;
      },
      onStatus: setStatus,
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
      stop();
      clearInterval(timer);
    };
  }, [key]);

  const apply = useCallback(
    (row) => {
      const live = prices[row?.symbol];
      if (row?.asset_type !== "crypto" || live == null) return row;
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

import { useNavigate } from "react-router-dom";
import SignalBadge from "./SignalBadge.jsx";
import { formatPrice, formatChange, changeClass, formatTurnoverUsd, formatVolume } from "../lib/format.js";

// "turnover": nilai transaksi 24 jam dalam USD (konsisten dengan urutan top_volume dan bisa
//             dibandingkan antara saham dan kripto).
// "volume":   jumlah lembar / koin (volume_24h). Tidak sebanding antar aset, hanya untuk satu pasar.
const VOLUME_MODE = "turnover";

const th = "px-6 py-4 text-sm font-bold";

export default function InstrumentTable({ rows, loading = false, empty = "Tidak ada data.", onSelect }) {
  const navigate = useNavigate();
  const open = (r) => {
    onSelect?.(r);
    navigate(`/detail/${encodeURIComponent(r.symbol)}`);
  };

  return (
    <div className="overflow-x-auto border border-line">
      <table className="w-full min-w-[640px] border-collapse text-left">
        <thead className="bg-canvas">
          <tr>
            <th className={th}>Instrumen</th>
            <th className={th}>Harga</th>
            <th className={`${th} text-center`}>Perubahan</th>
            <th className={`${th} text-right`}>{VOLUME_MODE === "turnover" ? "Nilai Transaksi" : "Volume"}</th>
            <th className="w-20" />
          </tr>
        </thead>
        <tbody>
          {!rows?.length && loading &&
            [0, 1, 2, 3].map((i) => (
              <tr key={i} className="border-t border-line">
                <td colSpan={5} className="px-6 py-4">
                  <div className="h-8 animate-pulse rounded bg-panel" />
                </td>
              </tr>
            ))}

          {!rows?.length && !loading && (
            <tr className="border-t border-line">
              <td colSpan={5} className="px-6 py-6 text-sm text-muted">
                {empty}
              </td>
            </tr>
          )}

          {rows?.map((r) => (
            <tr
              key={r.symbol}
              tabIndex={0}
              onClick={() => open(r)}
              onKeyDown={(e) => e.key === "Enter" && open(r)}
              className="cursor-pointer border-t border-line transition-colors hover:bg-panel focus-visible:bg-panel"
            >
              <td className="px-6 py-3">
                <div className="text-sm font-bold">{r.display_symbol}</div>
                <div className="text-xs text-muted">{r.name}</div>
              </td>
              <td className="px-6 py-3 text-sm font-semibold">{formatPrice(r.price, r.currency)}</td>
              <td className={`px-6 py-3 text-center text-sm font-semibold ${changeClass(r.change_pct)}`}>
                {formatChange(r.change_pct)}
              </td>
              <td className="px-6 py-3 text-right text-sm font-semibold">
                {VOLUME_MODE === "turnover" ? formatTurnoverUsd(r.turnover_24h_usd) : formatVolume(r.volume_24h)}
              </td>
              <td className="px-4 py-3 text-right">
                <SignalBadge signal={r.signal} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

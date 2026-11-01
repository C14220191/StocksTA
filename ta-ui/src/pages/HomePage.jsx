import { useState } from "react";
import InstrumentTable from "../components/InstrumentTable.jsx";
import MoverCard from "../components/MoverCard.jsx";
import LiveBadge from "../components/LiveBadge.jsx";
import useApi from "../lib/useApi.js";
import useLivePrices from "../lib/useLive.js";
import { getOverview } from "../lib/api.js";

// key = nilai parameter `type` di /api/market/overview
const TABS = [
  { key: "all", label: "Semua" },
  { key: "stock", label: "Saham (IDX)" },
  { key: "crypto", label: "Kripto" },
  { key: "commodity", label: "Komoditas" },
];

const TOP_VOLUME_LIMIT = 10;
const MOVERS_LIMIT = 3;

export default function HomePage() {
  const [tab, setTab] = useState("all");
  const { data, error, loading, reload } = useApi(
    (signal) => getOverview({ type: tab, limit: TOP_VOLUME_LIMIT, movers: MOVERS_LIMIT }, signal),
    [tab]
  );
  // Harga kripto di tabel dan kartu diperbarui real-time (urutan baris tetap sampai data dimuat ulang)
  const { apply, status } = useLivePrices(
    data ? [...data.top_volume, ...data.top_gainers, ...data.top_losers] : []
  );

  return (
    <div>
      <h1 className="text-xl font-bold">Beranda</h1>
      <p className="mt-1 text-xs text-muted">Ringkasan pasar saham IDX, kripto, dan komoditas hari ini</p>

      <div role="tablist" className="mt-5 inline-flex border border-line p-1">
        {TABS.map((t) => (
          <button
            key={t.key}
            role="tab"
            aria-selected={tab === t.key}
            onClick={() => setTab(t.key)}
            className={`rounded-lg px-6 py-2 text-xs font-bold transition-colors ${
              tab === t.key ? "bg-panel-2 text-ink" : "text-muted hover:text-ink"
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {error ? (
        <div className="mt-8 rounded-xl border border-line bg-panel p-6">
          <p className="text-sm text-down">{error.message}</p>
          <button onClick={reload} className="mt-3 rounded-lg bg-panel-2 px-4 py-2 text-xs font-bold hover:text-hold">
            Coba lagi
          </button>
        </div>
      ) : (
        <div className={loading && data ? "opacity-60 transition-opacity" : "transition-opacity"}>
          <h2 className="mb-4 mt-8 flex items-baseline gap-3 text-lg font-bold">
            Volume Tertinggi
            <LiveBadge status={status} />
          </h2>
          <InstrumentTable
            rows={data?.top_volume?.map(apply)}
            loading={loading}
            empty="Belum ada data pasar. Pastikan ingestion sudah berjalan."
          />

          <div className="mt-10 flex flex-col gap-10 md:flex-row">
            <MoverCard title="Penguatan Terbesar" rows={data?.top_gainers?.map(apply)} empty="Belum ada instrumen yang menguat." />
            <MoverCard title="Pelemahan Terbesar" rows={data?.top_losers?.map(apply)} empty="Belum ada instrumen yang melemah." />
          </div>
        </div>
      )}
    </div>
  );
}

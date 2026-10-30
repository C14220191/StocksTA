import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import InstrumentTable from "../components/InstrumentTable.jsx";
import useApi, { useDebounced } from "../lib/useApi.js";
import useLivePrices from "../lib/useLive.js";
import { searchInstruments } from "../lib/api.js";
import { loadRecent, saveRecent } from "../lib/recent.js";

export default function SearchPage() {
  const [params, setParams] = useSearchParams();
  const [input, setInput] = useState(params.get("q") ?? "");
  const [recent] = useState(loadRecent);

  // Tunggu pengguna berhenti mengetik supaya tidak memanggil backend di setiap huruf
  const term = useDebounced(input, 300).trim();

  useEffect(() => {
    setParams(term ? { q: term } : {}, { replace: true });
  }, [term, setParams]);

  const { data, error, loading } = useApi(
    (signal) => (term ? searchInstruments(term, "all", signal) : Promise.resolve(null)),
    [term]
  );
  const { apply } = useLivePrices(data?.results);

  return (
    <div>
      <h1 className="text-xl font-bold">Hasil Pencarian</h1>
      <p className="mt-1 text-xs text-muted">Cari kode saham IDX atau pasangan kripto yang tidak tampil di beranda</p>

      <input
        autoFocus
        value={input}
        onChange={(e) => setInput(e.target.value)}
        placeholder="Contoh: ANTM, BTC/USDT, bank"
        aria-label="Cari instrumen"
        className="mt-5 w-full rounded-full border border-line bg-panel px-8 py-2 text-lg font-bold outline-none placeholder:font-normal placeholder:text-muted focus:border-line-strong"
      />

      {term && data && (
        <p className="mb-4 mt-4 text-xs font-bold">
          {data.count} hasil untuk “{data.query}”
        </p>
      )}

      {error && <p className="mt-6 text-sm text-down">{error.message}</p>}

      {term && !error && (
        <div className={loading && data ? "opacity-60 transition-opacity" : "transition-opacity"}>
          <InstrumentTable
            rows={data?.results?.map(apply)}
            loading={loading}
            empty={`Tidak ada instrumen yang cocok dengan “${term}”.`}
            onSelect={(r) => saveRecent(r.display_symbol)}
          />
        </div>
      )}

      {recent.length > 0 && (
        <div className="mt-8">
          <h2 className="mb-3 text-xs text-muted">Pencarian terakhir</h2>
          <div className="flex flex-wrap gap-2">
            {recent.map((label) => (
              <button
                key={label}
                onClick={() => setInput(label)}
                className="rounded-full border border-line px-4 py-1 text-xs font-bold transition-colors hover:border-line-strong"
              >
                {label}
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

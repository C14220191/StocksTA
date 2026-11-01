import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import InstrumentTable from "../components/InstrumentTable.jsx";
import useApi from "../lib/useApi.js";
import useLivePrices from "../lib/useLive.js";
import { searchInstruments } from "../lib/api.js";
import { loadRecent, saveRecent } from "../lib/recent.js";

// Kata kunci datang dari URL (?q=), diisi oleh kotak pencarian di Header saat Enter ditekan.
export default function SearchPage() {
  const [params, setParams] = useSearchParams();
  const term = (params.get("q") ?? "").trim();
  const [recent] = useState(loadRecent);

  const { data, error, loading } = useApi(
    (signal) => (term ? searchInstruments(term, "all", signal) : Promise.resolve(null)),
    [term]
  );
  const { apply } = useLivePrices(data?.results);

  return (
    <div>
      <h1 className="text-xl font-bold">Hasil Pencarian</h1>
      <p className="mt-1 text-xs text-muted">
        {term
          ? "Pilih instrumen untuk melihat grafik dan ringkasan analisis AI"
          : "Ketik kode saham IDX, pasangan kripto, atau komoditas (mis. XAU) di kotak pencarian di atas, lalu tekan Enter"}
      </p>

      {term && data && (
        <p className="mb-4 mt-6 text-xs font-bold">
          {data.count} hasil untuk “{data.query}”
        </p>
      )}

      {error && <p className="mt-6 text-sm text-down">{error.message}</p>}

      {term && !error && (
        <div className={`${data ? "" : "mt-6"} ${loading && data ? "opacity-60 transition-opacity" : "transition-opacity"}`}>
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
                onClick={() => setParams({ q: label })}
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

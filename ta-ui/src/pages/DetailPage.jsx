import { useState } from "react";
import { Link } from "react-router-dom";
import SignalBadge from "../components/SignalBadge.jsx";
import PriceChart from "../components/PriceChart.jsx";
import IndicatorPicker from "../components/IndicatorPicker.jsx";
import LiveBadge from "../components/LiveBadge.jsx";
import AiAnalysis, { useAnalyses } from "../components/AiAnalysis.jsx";
import useApi from "../lib/useApi.js";
import { getCandles, getInstrument } from "../lib/api.js";
import { liveMarket } from "../lib/binance.js";
import { METHODS, consensus } from "../lib/methods.js";
import {
  ageSecondsFromIso,
  assetLabel,
  changeClass,
  formatAgeSeconds,
  formatChange,
  formatPrice,
  formatVolume,
} from "../lib/format.js";

// Rentang = parameter `range`; ukuran candle = parameter `tf`.
const RANGES = ["1D", "1W", "1M", "3M", "6M", "1Y", "ALL"];
const RANGE_LABEL = { ALL: "Semua" };
const TIMEFRAMES = ["1h", "2h", "4h", "6h", "1d"];
// ALL = seluruh data di database (sekitar 1 tahun), dianggap 1 tahun untuk batas jumlah candle.
const RANGE_HOURS = { "1D": 24, "1W": 168, "1M": 720, "3M": 2160, "6M": 4368, "1Y": 8760, ALL: 8760 };
const TF_HOURS = { "1h": 1, "2h": 2, "4h": 4, "6h": 6, "1d": 24 };

// Backend menerima 6 sampai 10.000 candle; UI membatasi 1.000 supaya chart dan indikator tetap ringan.
const tfAllowed = (range, tf) => {
  const n = RANGE_HOURS[range] / TF_HOURS[tf];
  return n >= 6 && n <= 1000;
};

function StatCard({ label, value }) {
  return (
    <div className="rounded-xl bg-panel-2 px-5 py-4 font-sans">
      <div className="text-xs text-muted">{label}</div>
      <div className="mt-2 text-xl font-semibold">{value}</div>
    </div>
  );
}

function Segmented({ children, label }) {
  return (
    <div role="tablist" aria-label={label} className="inline-flex rounded-lg bg-panel p-1">
      {children}
    </div>
  );
}

function SegButton({ selected, disabled, title, onClick, children }) {
  return (
    <button
      role="tab"
      aria-selected={selected}
      disabled={disabled}
      title={title}
      onClick={onClick}
      className={`rounded-md px-4 py-2 text-xs font-bold transition-colors ${
        selected ? "bg-panel-2 text-ink" : "text-muted hover:text-ink"
      } disabled:cursor-not-allowed disabled:opacity-30 disabled:hover:text-muted`}
    >
      {children}
    </button>
  );
}

export default function DetailPage({ symbol }) {
  const [range, setRange] = useState("1D");
  const [tf, setTf] = useState("auto"); // "auto" = bawaan backend untuk range tsb
  const [active, setActive] = useState(() => new Set(["rsi", "ema"])); // id dari lib/indicators.js
  const [livePrice, setLivePrice] = useState(null);
  const [liveStatus, setLiveStatus] = useState(null);

  const detail = useApi((signal) => getInstrument(symbol, signal), [symbol]);
  const candles = useApi(
    (signal) => getCandles(symbol, range, tf === "auto" ? undefined : tf, signal),
    [symbol, range, tf]
  );
  const analyses = useAnalyses(symbol, !!detail.data);

  const changeRange = (r) => {
    setRange(r);
    if (tf !== "auto" && !tfAllowed(r, tf)) setTf("auto");
  };

  const toggle = (id) =>
    setActive((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });

  if (detail.error) {
    return (
      <div className="rounded-xl border border-line bg-panel p-6">
        <p className="text-sm text-down">{detail.error.message}</p>
        <div className="mt-3 flex gap-3 text-xs font-bold">
          {detail.error.status !== 404 && (
            <button onClick={detail.reload} className="rounded-lg bg-panel-2 px-4 py-2 hover:text-hold">
              Coba lagi
            </button>
          )}
          <Link to="/search" className="rounded-lg bg-panel-2 px-4 py-2 hover:text-hold">
            Cari instrumen lain
          </Link>
        </div>
      </div>
    );
  }

  const inst = detail.data?.instrument;
  const stats = detail.data?.stats;
  const currency = inst?.currency;
  const price = (v) => formatPrice(v, currency, { short: true });

  // Kripto dan komoditas Binance: harga dan candle terakhir real-time (spot / futures). Saham: data ingestion backend.
  const market = liveMarket(inst?.source);
  const live = market && candles.data ? { symbol: inst.symbol, interval: candles.data.timeframe, market } : null;
  const shownPrice = livePrice ?? inst?.price;
  const prevClose = inst?.change_pct != null && inst.change_pct > -100 ? inst.price / (1 + inst.change_pct / 100) : null;
  const shownChange = livePrice != null && prevClose ? (livePrice / prevClose - 1) * 100 : inst?.change_pct;
  const pickerCtx = { timeframe: candles.data?.timeframe, assetType: inst?.asset_type };
  // Bias gabungan tiga metode: dari analisis kunjungan ini kalau sudah ada, selain itu dari tabel instrument.
  const freshSignals = METHODS.map((m) => analyses[m.id]?.data?.detail && analyses[m.id].data.signal);
  const headerSignal = freshSignals.some(Boolean) ? consensus(freshSignals) : inst?.signal;

  return (
    <div>
      <div className="flex items-baseline gap-3">
        <h1 className="text-xl font-bold">{inst?.display_symbol ?? symbol}</h1>
        {inst && <SignalBadge signal={headerSignal} />}
      </div>
      <p className="mt-1 min-h-4 text-xs text-muted">{inst ? `${inst.name} - ${assetLabel(inst.asset_type)}` : ""}</p>

      {inst && (
        <div className="mt-3 flex flex-wrap items-baseline gap-x-3 gap-y-1 font-sans">
          <span className="text-2xl font-semibold">{formatPrice(shownPrice, currency)}</span>
          <span className={`text-sm font-semibold ${changeClass(shownChange)}`}>{formatChange(shownChange)}</span>
          {market ? (
            <LiveBadge status={liveStatus ?? "connecting"} />
          ) : (
            <span className="text-xs text-muted">
              harga per {formatAgeSeconds(ageSecondsFromIso(inst.price_ts))}, tidak real-time
            </span>
          )}
        </div>
      )}

      <div className="mt-5 flex flex-wrap items-center gap-x-6 gap-y-3">
        <Segmented label="Rentang waktu">
          {RANGES.map((r) => (
            <SegButton key={r} selected={range === r} onClick={() => changeRange(r)}>
              {RANGE_LABEL[r] ?? r}
            </SegButton>
          ))}
        </Segmented>

        <div className="flex items-center gap-2 text-xs text-muted">
          <span>Candle</span>
          <Segmented label="Ukuran candle">
            <SegButton selected={tf === "auto"} onClick={() => setTf("auto")}>
              Auto
            </SegButton>
            {TIMEFRAMES.map((t) => {
              const ok = tfAllowed(range, t);
              return (
                <SegButton
                  key={t}
                  selected={tf === t}
                  disabled={!ok}
                  title={ok ? undefined : `Tidak tersedia untuk rentang ${range}`}
                  onClick={() => setTf(t)}
                >
                  {t.toUpperCase()}
                </SegButton>
              );
            })}
          </Segmented>
        </div>
      </div>

      <IndicatorPicker
        active={active}
        ctx={pickerCtx}
        onToggle={toggle}
        onAll={(ids) => setActive(new Set(ids))}
        onNone={() => setActive(new Set())}
      />

      <div className="relative mt-5 overflow-clip rounded-lg border border-line bg-panel">
        {candles.data && inst && (
          <PriceChart
            data={candles.data}
            active={active}
            assetType={inst?.asset_type}
            currency={currency}
            live={live}
            onPrice={setLivePrice}
            onStatus={setLiveStatus}
          />
        )}
        {!candles.data && candles.loading && <div className="h-80 animate-pulse bg-panel-2" />}
        {candles.error && <p className="p-6 text-sm text-down">{candles.error.message}</p>}
        {candles.loading && candles.data && (
          <div className="absolute inset-0 flex items-center justify-center bg-surface/50 text-xs text-muted">
            Memuat candle...
          </div>
        )}
      </div>

      <div className="mt-8 grid grid-cols-2 gap-4 lg:grid-cols-4">
        <StatCard label="RSI (14)" value={stats?.rsi14 != null ? stats.rsi14.toFixed(1) : "-"} />
        <StatCard label="Support" value={price(stats?.support)} />
        <StatCard label="Resistance" value={price(stats?.resistance)} />
        <StatCard label="Volume 24 jam" value={formatVolume(stats?.volume_24h)} />
      </div>
      {stats?.basis && <p className="mt-2 font-sans text-xs text-muted">Dasar: {stats.basis}</p>}

      <AiAnalysis
        latest={detail.data?.ai_latest}
        analyses={analyses}
        currency={currency}
        livePrice={livePrice}
        assetType={inst?.asset_type}
      />
    </div>
  );
}

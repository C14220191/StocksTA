import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import SignalBadge from "../components/SignalBadge.jsx";
import PriceChart from "../components/PriceChart.jsx";
import IndicatorPicker from "../components/IndicatorPicker.jsx";
import LiveBadge from "../components/LiveBadge.jsx";
import useApi from "../lib/useApi.js";
import { analyzeSymbol, getCandles, getInstrument } from "../lib/api.js";
import {
  ageSecondsFromIso,
  assetLabel,
  changeClass,
  formatAgeSeconds,
  formatCandleTime,
  formatChange,
  formatPrice,
  formatVolume,
  localizeUtcTimes,
} from "../lib/format.js";

// Rentang = parameter `range`; ukuran candle = parameter `tf`.
const RANGES = ["1D", "1W", "1M", "3M", "6M", "1Y", "ALL"];
const RANGE_LABEL = { ALL: "Semua" };
const TIMEFRAMES = ["1h", "2h", "4h", "6h", "1d"];
// ALL = seluruh data di database (sekitar 1 tahun), dianggap 1 tahun untuk batas jumlah candle.
const RANGE_HOURS = { "1D": 24, "1W": 168, "1M": 720, "3M": 2160, "6M": 4368, "1Y": 8760, ALL: 8760 };
const TF_HOURS = { "1h": 1, "2h": 2, "4h": 4, "6h": 6, "1d": 24 };

// Harga bergerak sejauh ini (persen) sejak analisis dibuat -> tampilkan peringatan di panel AI.
const MOVE_WARN_PCT = 1;

// Backend menerima 6 sampai 10.000 candle; UI membatasi 1.000 supaya chart dan indikator tetap ringan.
const tfAllowed = (range, tf) => {
  const n = RANGE_HOURS[range] / TF_HOURS[tf];
  return n >= 6 && n <= 1000;
};

// /api/analyze dipanggil sekali per kunjungan, dan hanya setelah instrumennya terbukti ada.
function useAnalysis(symbol, enabled) {
  const [state, setState] = useState({ data: null, error: null, loading: true });
  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    analyzeSymbol(symbol)
      .then((data) => !cancelled && setState({ data, error: null, loading: false }))
      .catch((error) => !cancelled && setState({ data: null, error, loading: false }));
    return () => {
      cancelled = true;
    };
  }, [symbol, enabled]);
  return state;
}

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

// Asal ringkasan yang tampil: dibuat AI untuk kunjungan ini (MISS/BYPASS), dari cache (HIT),
// atau ringkasan tersimpan terakhir yang ditampilkan sambil menunggu /api/analyze.
function analysisOrigin(fresh) {
  if (!fresh) return "Analisis tersimpan, dibuat";
  if (fresh.cache_status === "HIT") return "Dari cache, dianalisis";
  return "Analisis baru, dibuat";
}

function AiPanel({ latest, ai, currency, livePrice }) {
  const fresh = ai.data;
  const shown = fresh ?? latest; // tampilkan ringkasan tersimpan dulu sambil menunggu analisis terbaru
  const hasLevels = shown?.support != null && shown?.resistance != null;
  // source_data_ts = waktu BUKA candle 1h terakhir yang dilihat AI; candle itu tutup 1 jam kemudian.
  const dataUntil = shown?.source_data_ts ? new Date(shown.source_data_ts).getTime() / 1000 + 3600 : null;
  const priceAtGen = fresh?.price_at_generation > 0 ? fresh.price_at_generation : null; // ai_latest belum membawa harga
  // Kripto: dihitung ulang dari harga live supaya ikut bergerak; saham: nilai dari backend.
  const deviation = priceAtGen && livePrice ? (livePrice / priceAtGen - 1) * 100 : fresh?.price_deviation_pct ?? null;
  const moved = deviation != null && Math.abs(deviation) >= MOVE_WARN_PCT;

  return (
    <section className="bg-panel p-6 font-sans" aria-live="polite">
      <h2 className="text-base font-semibold">Ringkasan analisis AI</h2>

      {!shown && ai.loading && (
        <div className="mt-4 space-y-2" aria-busy="true">
          <div className="h-3 w-full animate-pulse rounded bg-panel-2" />
          <div className="h-3 w-11/12 animate-pulse rounded bg-panel-2" />
          <div className="h-3 w-4/5 animate-pulse rounded bg-panel-2" />
          <p className="pt-2 text-xs text-muted">Menyiapkan analisis, bisa memakan 5 sampai 15 detik...</p>
        </div>
      )}

      {!shown && ai.error && <p className="mt-3 text-sm text-down">{ai.error.message}</p>}

      {shown && (
        <>
          <p className="mt-3 whitespace-pre-line text-sm leading-relaxed">
            {localizeUtcTimes(shown.summary, shown.generated_at)}
          </p>
          {hasLevels && (
            <p className="mt-3 text-xs text-muted">
              Level menurut AI: support {formatPrice(shown.support, currency, { short: true })} · resistance{" "}
              {formatPrice(shown.resistance, currency, { short: true })}
            </p>
          )}
          <p className="mt-4 text-xs text-muted">
            {analysisOrigin(fresh)} {formatCandleTime(new Date(shown.generated_at).getTime() / 1000)} (
            {formatAgeSeconds(ageSecondsFromIso(shown.generated_at))})
            {dataUntil && <> · data candle hingga {formatCandleTime(dataUntil)}</>}
            {priceAtGen && <> · harga saat analisis {formatPrice(priceAtGen, currency, { short: true })}</>}
          </p>
          {moved && (
            <p className="mt-2 text-xs text-hold">
              Harga sudah bergerak {formatChange(deviation)} sejak analisis ini dibuat, ringkasan mungkin kurang relevan.
            </p>
          )}
          {ai.loading && (
            <p className="mt-2 flex items-center gap-2 text-xs text-hold">
              <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-hold" />
              Memperbarui analisis, bisa memakan 5 sampai 15 detik jika belum ada di cache...
            </p>
          )}
          {ai.error && (
            <p className="mt-2 text-xs text-hold">
              Pembaruan analisis gagal ({ai.error.message}). Menampilkan analisis yang tersimpan.
            </p>
          )}
        </>
      )}
    </section>
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
  const ai = useAnalysis(symbol, !!detail.data);

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

  // Kripto: harga dan candle terakhir diperbarui real-time dari Binance. Saham: dari data ingestion backend.
  const isCrypto = inst?.asset_type === "crypto";
  const live = isCrypto && candles.data ? { symbol: inst.symbol, interval: candles.data.timeframe } : null;
  const shownPrice = livePrice ?? inst?.price;
  const prevClose = inst?.change_pct != null && inst.change_pct > -100 ? inst.price / (1 + inst.change_pct / 100) : null;
  const shownChange = livePrice != null && prevClose ? (livePrice / prevClose - 1) * 100 : inst?.change_pct;
  const pickerCtx = { timeframe: candles.data?.timeframe, assetType: inst?.asset_type };

  return (
    <div>
      <div className="flex items-baseline gap-3">
        <h1 className="text-xl font-bold">{inst?.display_symbol ?? symbol}</h1>
        {inst && <SignalBadge signal={ai.data?.signal ?? inst.signal} />}
      </div>
      <p className="mt-1 min-h-4 text-xs text-muted">{inst ? `${inst.name} - ${assetLabel(inst.asset_type)}` : ""}</p>

      {inst && (
        <div className="mt-3 flex flex-wrap items-baseline gap-x-3 gap-y-1 font-sans">
          <span className="text-2xl font-semibold">{formatPrice(shownPrice, currency)}</span>
          <span className={`text-sm font-semibold ${changeClass(shownChange)}`}>{formatChange(shownChange)}</span>
          {isCrypto ? (
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

      <div className="mt-8 grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.6fr)]">
        <div className="grid grid-cols-2 gap-4 self-start">
          <StatCard label="RSI (14)" value={stats?.rsi14 != null ? stats.rsi14.toFixed(1) : "-"} />
          <StatCard label="Support" value={price(stats?.support)} />
          <StatCard label="Resistance" value={price(stats?.resistance)} />
          <StatCard label="Volume 24 jam" value={formatVolume(stats?.volume_24h)} />
          {stats?.basis && <p className="col-span-2 font-sans text-xs text-muted">Dasar: {stats.basis}</p>}
        </div>

        <AiPanel latest={detail.data?.ai_latest} ai={ai} currency={currency} livePrice={livePrice} />
      </div>
    </div>
  );
}

import { useState } from "react";
import { Link } from "react-router-dom";
import useApi from "../../lib/useApi.js";
import { addInstrument, getAdminInstruments, lookupInstrument, reingestInstrument } from "../../lib/api.js";
import { assetLabel, formatPrice } from "../../lib/format.js";
import { ErrorBox, Kpi, PageHeader, Panel, Segmented, StatusDot, Table, Td, fmtDateTime, fmtInt, usePolling } from "../ui.jsx";

const TYPES = [
  {
    value: "stock",
    label: "Saham IDX",
    placeholder: "BBCA atau BBCA.JK",
    help: "Dicek ke Yahoo Finance: harus saham Bursa Efek Indonesia (IDR). Candle dan fundamental diambil dari Yahoo.",
  },
  {
    value: "crypto",
    label: "Kripto",
    placeholder: "SOL, SOLUSDT, atau SOL/USDT",
    help: "Dicek ke Binance spot: pasangan USDT berstatus TRADING.",
  },
  {
    value: "commodity",
    label: "Komoditas",
    placeholder: "XAU atau XAUUSDT",
    help: "Dicek ke Binance USDⓈ-M futures: hanya kontrak bertipe COMMODITY (TradFi perpetual), mis. emas XAUUSDT.",
  },
];
const FILTERS = [{ value: "all", label: "Semua" }, ...TYPES.map(({ value, label }) => ({ value, label }))];
const SOURCE = { yahoo: "Yahoo Finance", binance: "Binance spot", binance_futures: "Binance futures" };

function fmtPrice(p, currency) {
  if (p == null) return "-";
  return currency !== "IDR" && Math.abs(p) < 0.01 ? `$${p.toPrecision(4)}` : formatPrice(p, currency, { short: true });
}

// Status data satu instrumen: berjalan, gagal (pesan job), belum ada data, atau siap.
function dataStatus(i) {
  if (i.ingesting) return { status: "warn", label: "Backfill berjalan" };
  const failed = Object.entries(i.jobs ?? {}).find(([, j]) => j.status === "fail");
  if (failed) return { status: "down", label: "Backfill gagal", detail: failed[1].message };
  if (!i.candles_1h || !i.candles_1d) return { status: "warn", label: "Belum ada data" };
  return { status: "ok", label: "Siap" };
}

// Fitur yang baru aktif setelah backend restart (daftar price feed dan σ_ref dibaca saat start).
function restartNeeds(i) {
  const needs = [];
  if (i.live_price === false) needs.push("harga live");
  if (i.sigma_ref === false && i.candles_1h > 0) needs.push("σ_ref");
  return needs;
}

function ReingestButton({ symbol, onDone }) {
  const [state, setState] = useState("idle"); // idle | busy | error
  const click = async () => {
    setState("busy");
    try {
      await reingestInstrument(symbol);
      setState("idle");
      onDone();
    } catch {
      setState("error");
    }
  };
  return (
    <button
      onClick={click}
      disabled={state === "busy"}
      className="rounded-md border border-line px-3 py-1 text-xs font-semibold text-muted transition-colors hover:text-ink disabled:opacity-30"
    >
      {state === "busy" ? "..." : state === "error" ? "Gagal, ulangi" : "Ulangi backfill"}
    </button>
  );
}

function AddInstrument({ onAdded }) {
  const [type, setType] = useState("stock");
  const [symbol, setSymbol] = useState("");
  const [name, setName] = useState("");
  const [preview, setPreview] = useState(null);
  const [result, setResult] = useState(null);
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(null); // null | "lookup" | "add"
  const t = TYPES.find((x) => x.value === type);

  const reset = (fn) => (v) => {
    fn(v);
    setPreview(null);
    setError(null);
  };

  const check = async (e) => {
    e.preventDefault();
    if (!symbol.trim() || busy) return;
    setBusy("lookup");
    setError(null);
    setResult(null);
    try {
      const p = await lookupInstrument(type, symbol);
      setPreview(p);
      setName(p.name);
    } catch (err) {
      setPreview(null);
      setError(err);
    } finally {
      setBusy(null);
    }
  };

  const add = async () => {
    setBusy("add");
    setError(null);
    try {
      const r = await addInstrument({ asset_type: type, symbol: preview.symbol, name });
      setResult(r);
      setPreview(null);
      setSymbol("");
      setName("");
      onAdded();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(null);
    }
  };

  return (
    <Panel title="Tambah instrumen" aside="Simbol dicek ke sumber datanya sebelum disimpan">
      <form onSubmit={check} className="space-y-4">
        <Segmented label="Jenis instrumen" value={type} options={TYPES} onChange={reset(setType)} />
        <div className="flex flex-wrap items-end gap-3">
          <label className="block min-w-56 flex-1 text-xs font-semibold text-muted">
            Simbol
            <input
              value={symbol}
              onChange={(e) => reset(setSymbol)(e.target.value)}
              placeholder={t.placeholder}
              className="mt-2 w-full rounded-lg border border-line bg-canvas px-3 py-2 text-sm font-normal text-ink outline-none focus:border-line-strong"
            />
          </label>
          <button
            type="submit"
            disabled={!symbol.trim() || busy != null}
            className="rounded-lg bg-panel-2 px-4 py-2 text-sm font-semibold text-ink transition-colors hover:text-hold disabled:cursor-not-allowed disabled:opacity-40"
          >
            {busy === "lookup" ? "Mengecek..." : "Cek simbol"}
          </button>
        </div>
        <p className="text-xs text-muted">{t.help}</p>
      </form>

      {error && (
        <p role="alert" className="mt-4 text-sm text-down">
          {error.message}
        </p>
      )}

      {preview && (
        <div className="mt-5 rounded-xl border border-line bg-panel-2 p-4">
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <div className="text-base font-semibold">{preview.symbol}</div>
            <div className="text-xs text-muted">
              {preview.exchange} · harga terakhir {fmtPrice(preview.price, preview.currency)}
            </div>
          </div>
          {preview.exists ? (
            <p className="mt-3 text-sm text-muted">
              Sudah terdaftar.{" "}
              <Link to={`/detail/${encodeURIComponent(preview.symbol)}`} className="underline">
                Buka halaman Detail
              </Link>
            </p>
          ) : (
            <div className="mt-4 flex flex-wrap items-end gap-3">
              <label className="block min-w-56 flex-1 text-xs font-semibold text-muted">
                Nama yang ditampilkan
                <input
                  value={name}
                  maxLength={80}
                  onChange={(e) => setName(e.target.value)}
                  className="mt-2 w-full rounded-lg border border-line bg-canvas px-3 py-2 text-sm font-normal text-ink outline-none focus:border-line-strong"
                />
              </label>
              <button
                onClick={add}
                disabled={busy != null}
                className="rounded-lg border border-status-good px-4 py-2 text-sm font-semibold text-ink transition-colors hover:bg-panel disabled:opacity-40"
              >
                {busy === "add" ? "Menyimpan..." : `Tambah ${assetLabel(preview.asset_type)}`}
              </button>
            </div>
          )}
        </div>
      )}

      {result && (
        <div role="status" className="mt-5 rounded-xl border border-line p-4 text-sm">
          <p className="font-semibold">
            {result.instrument.symbol} ({result.instrument.name}) ditambahkan.
          </p>
          <ul className="mt-2 list-disc space-y-1 pl-5 text-xs text-muted">
            {!result.ingest_started && <li>Backfill untuk simbol ini sudah berjalan sebelumnya.</li>}
            {result.notes.map((n) => (
              <li key={n}>{n}</li>
            ))}
          </ul>
        </div>
      )}
    </Panel>
  );
}

export default function InstrumentsPage() {
  const { data, error, loading, reload } = useApi((signal) => getAdminInstruments(signal), []);
  const anyIngesting = data?.instruments?.some((i) => i.ingesting);
  usePolling(reload, anyIngesting ? 5000 : 30000);
  const [filter, setFilter] = useState("all");

  const rows = (data?.instruments ?? []).filter((i) => filter === "all" || i.asset_type === filter);
  const needRestart = (data?.instruments ?? []).filter((i) => restartNeeds(i).length > 0);
  const c = data?.counts ?? {};
  const busy = loading && !data;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Instrumen"
        subtitle="Daftar instrumen yang diambil datanya, dan penambahan saham IDX, kripto, atau komoditas baru."
      />

      <div className="grid gap-4 sm:grid-cols-3">
        <Kpi label="Saham IDX" value={fmtInt(c.stock ?? 0)} loading={busy} sub="Yahoo Finance, per jam" />
        <Kpi label="Kripto" value={fmtInt(c.crypto ?? 0)} loading={busy} sub="Binance spot, live" />
        <Kpi label="Komoditas" value={fmtInt(c.commodity ?? 0)} loading={busy} sub="Binance futures, live" />
      </div>

      <AddInstrument onAdded={reload} />

      {needRestart.length > 0 && (
        <div role="note" className="rounded-xl border border-hold/40 bg-hold/10 px-5 py-4 text-xs leading-relaxed">
          <strong className="text-ink">Restart backend untuk mengaktifkan {needRestart.map((i) => i.symbol).join(", ")}.</strong>{" "}
          <span className="text-muted">
            Daftar harga live dan σ_ref TTL dinamis dibaca saat backend start. Sampai restart, harga diambil dari
            candle 1 jam terakhir dan TTL memakai TTL_min. Server: <code>./ta-up.sh restart</code>
          </span>
        </div>
      )}

      <ErrorBox error={error} onRetry={reload} />

      <Panel
        title="Daftar instrumen"
        aside={<Segmented label="Saring jenis" value={filter} options={FILTERS} onChange={setFilter} />}
      >
        <Table
          head={[
            "Simbol",
            "Nama",
            "Jenis",
            "Sumber",
            { label: "Candle 1h", right: true },
            "1h terakhir",
            { label: "Candle 1D", right: true },
            "Status data",
            "Perlu restart",
            "",
          ]}
          empty={!busy && rows.length === 0 ? "Belum ada instrumen." : null}
        >
          {rows.map((i) => {
            const s = dataStatus(i);
            const needs = restartNeeds(i);
            return (
              <tr key={i.symbol}>
                <Td>
                  <Link to={`/detail/${encodeURIComponent(i.symbol)}`} className="font-semibold hover:underline">
                    {i.display_symbol}
                  </Link>
                </Td>
                <Td>{i.name || "-"}</Td>
                <Td>{assetLabel(i.asset_type)}</Td>
                <Td className="text-muted">{SOURCE[i.source] ?? i.source}</Td>
                <Td right>{fmtInt(i.candles_1h)}</Td>
                <Td className="text-muted">{fmtDateTime(i.last_1h)}</Td>
                <Td right>{fmtInt(i.candles_1d)}</Td>
                <Td>
                  <span title={s.detail ?? undefined}>
                    <StatusDot status={s.status} label={s.label} />
                  </span>
                </Td>
                <Td className="text-muted">{needs.length ? needs.join(", ") : "-"}</Td>
                <Td>{!i.ingesting && s.status !== "ok" && <ReingestButton symbol={i.symbol} onDone={reload} />}</Td>
              </tr>
            );
          })}
        </Table>
      </Panel>
    </div>
  );
}

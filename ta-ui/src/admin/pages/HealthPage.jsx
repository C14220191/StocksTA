import useApi from "../../lib/useApi.js";
import useLivePrices from "../../lib/useLive.js";
import { getAdminHealth } from "../../lib/api.js";
import { ErrorBox, PageHeader, Panel, StatusDot, Table, Td, fmtDateTime, usePolling } from "../ui.jsx";

const WS_STATUS = { live: "ok", connecting: "warn", offline: "down" };

// Cek WebSocket Binance dari browser: jalur yang dipakai chart dan harga live di UI pengguna
// (tidak lewat backend, jadi tidak terlihat dari /api/admin/health).
function BrowserStream({ name, rows }) {
  const { status } = useLivePrices(rows);
  return (
    <ComponentCard
      c={{
        name,
        status: WS_STATUS[status] ?? "warn",
        detail: `${status === "live" ? "Tersambung" : status === "offline" ? "Terputus, mencoba ulang" : "Menyambung"} · ${rows
          .map((r) => r.symbol)
          .join(", ")} · diuji dari browser ini`,
      }}
    />
  );
}

function ComponentCard({ c }) {
  return (
    <div className="rounded-xl border border-line bg-panel px-5 py-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">{c.name}</h3>
        <div className="flex items-center gap-3">
          {c.latency_ms != null && <span className="text-xs tabular-nums text-muted">{c.latency_ms} ms</span>}
          <StatusDot status={c.status} />
        </div>
      </div>
      <p className="mt-2 text-xs text-muted">{c.detail}</p>
    </div>
  );
}

export default function HealthPage() {
  const { data, error, loading, reload } = useApi((signal) => getAdminHealth(signal), []);
  usePolling(reload, 10000);
  const comps = data?.components ?? [];
  const bad = comps.filter((c) => c.status !== "ok").length;

  return (
    <div className="space-y-6">
      <PageHeader title="API Health" subtitle="Status koneksi ke seluruh sumber data dan layanan · diperbarui tiap 10 detik">
        <span className="text-xs text-muted">{data && `Diperiksa ${fmtDateTime(data.checked_at)}`}</span>
        <button
          onClick={reload}
          disabled={loading}
          className="rounded-lg border border-line px-3 py-2 text-xs font-semibold text-muted hover:text-ink disabled:opacity-50"
        >
          {loading ? "Memeriksa..." : "Periksa ulang"}
        </button>
      </PageHeader>

      <ErrorBox error={error} onRetry={reload} />

      {data && (
        <p className="text-sm">
          {bad === 0 ? (
            <StatusDot status="ok" label="Semua komponen backend normal" />
          ) : (
            <StatusDot status="warn" label={`${bad} komponen perlu dicek`} />
          )}
        </p>
      )}

      <div className="grid gap-4 lg:grid-cols-2">
        {comps.map((c) => (
          <ComponentCard key={c.name} c={c} />
        ))}
        {!data && !error && [0, 1, 2, 3].map((i) => <div key={i} className="h-24 animate-pulse rounded-xl bg-panel" />)}
        <BrowserStream name="Binance WebSocket spot (browser)" rows={[{ symbol: "BTCUSDT", source: "binance" }]} />
        <BrowserStream name="Binance WebSocket futures (browser)" rows={[{ symbol: "XAUUSDT", source: "binance_futures" }]} />
      </div>

      <Panel title="Log kesalahan" aside="7 hari terakhir: request /api/analyze yang gagal dan job yang gagal">
        <Table
          head={["Waktu", "Sumber", "Pesan"]}
          empty={data && data.errors.length === 0 ? "Tidak ada kesalahan dalam 7 hari terakhir." : null}
        >
          {data?.errors.map((e, i) => (
            <tr key={i}>
              <Td className="whitespace-nowrap">{fmtDateTime(e.at)}</Td>
              <Td className="whitespace-nowrap font-semibold">{e.source}</Td>
              <Td className="max-w-xl break-words text-muted">{e.message}</Td>
            </tr>
          ))}
        </Table>
      </Panel>
    </div>
  );
}

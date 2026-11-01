import { useState } from "react";
import useApi from "../../lib/useApi.js";
import { getAdminOverview } from "../../lib/api.js";
import SignalBadge from "../../components/SignalBadge.jsx";
import RequestChart from "../RequestChart.jsx";
import {
  ErrorBox,
  Kpi,
  ModeChip,
  PageHeader,
  Panel,
  Segmented,
  Select,
  Table,
  Td,
  TRIGGERS,
  fmtInt,
  fmtMs,
  fmtPct,
  fmtTokens,
  fmtUsd,
  usePolling,
} from "../ui.jsx";

const PERIODS = [
  { value: "24h", label: "24 jam" },
  { value: "7d", label: "7 hari" },
  { value: "30d", label: "30 hari" },
];
const PERIOD_TEXT = { "24h": "24 jam terakhir", "7d": "7 hari terakhir", "30d": "30 hari terakhir" };

export default function DashboardPage() {
  const [period, setPeriod] = useState("24h");
  const [trigger, setTrigger] = useState("all");
  const { data, error, loading, reload } = useApi(
    (signal) => getAdminOverview({ period, trigger }, signal),
    [period, trigger]
  );
  usePolling(reload, 30000);

  const k = data?.kpi;
  const costNoCache = k ? k.cost_usd + k.cost_saved : null;
  const savedShare = k && costNoCache > 0 ? k.cost_saved / costNoCache : null;
  const tokensNoCache = k ? k.tokens + k.tokens_saved : null;
  const tokenShare = k && tokensNoCache > 0 ? k.tokens_saved / tokensNoCache : null;
  const busy = loading && !data;

  return (
    <div className="space-y-6">
      <PageHeader title="Dashboard" subtitle={`Ringkasan operasional ${PERIOD_TEXT[period]}`}>
        <ModeChip mode={data?.cache_mode} />
        <Segmented label="Periode" value={period} options={PERIODS} onChange={setPeriod} />
        <Select label="Sumber" value={trigger} options={TRIGGERS} onChange={setTrigger} />
      </PageHeader>

      <ErrorBox error={error} onRetry={reload} />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <Kpi
          loading={busy}
          label="Biaya AI"
          value={fmtUsd(k?.cost_usd)}
          sub={
            k &&
            (k.cost_usd === 0 && k.ai_calls > 0
              ? `${fmtTokens(k.tokens)} token · model gratis, biaya $0`
              : `${fmtTokens(k.tokens)} token · tanpa cache ≈ ${fmtUsd(costNoCache)}`)
          }
        />
        <Kpi
          loading={busy}
          label="Penghematan cache"
          value={k ? `${fmtTokens(k.tokens_saved)} token` : "-"}
          sub={
            k &&
            `≈ ${fmtUsd(k.cost_saved)} · hemat ${fmtPct(savedShare ?? tokenShare)} · hit ratio ${fmtPct(k.hit_ratio)}`
          }
        />
        <Kpi
          loading={busy}
          label="Latensi (median)"
          value={fmtMs(k?.latency_p50_ms)}
          sub={k && `p95 ${fmtMs(k.latency_p95_ms)} · HIT ${fmtMs(k.latency_hit_avg_ms)} · AI ${fmtMs(k.latency_ai_avg_ms)}`}
        />
        <Kpi
          loading={busy}
          label="Kunjungan halaman Detail"
          value={fmtInt(k?.visits)}
          sub={k && `hari ini ${fmtInt(k.visits_today)} · total request ${fmtInt(k.requests)}`}
        />
      </div>
      {k?.per_call_from_all_time && k.hits > 0 && (
        <p className="-mt-3 text-xs text-muted">
          Tidak ada panggilan AI di periode ini; penghematan diestimasi dari rata-rata token per panggilan sepanjang waktu.
        </p>
      )}

      <Panel
        title={data?.bucket === "day" ? "Request per hari" : "Request per jam"}
        aside={`Waktu ${data?.timezone === "Asia/Jakarta" ? "WIB" : data?.timezone ?? ""} · ${fmtInt(k?.errors)} error`}
      >
        {data ? <RequestChart series={data.series} bucket={data.bucket} /> : <div className="h-56 animate-pulse rounded bg-panel-2" />}
      </Panel>

      <Panel title="Instrumen paling sering dianalisis" aside="Satu request = satu panggilan /api/analyze">
        <Table
          head={[
            "Instrumen",
            { label: "Request", right: true },
            { label: "Hit ratio", right: true },
            { label: "Latensi rata-rata", right: true },
            "Sinyal terakhir",
          ]}
          empty={data && data.top_instruments.length === 0 ? "Belum ada request di periode ini." : null}
        >
          {data?.top_instruments.map((t) => (
            <tr key={t.symbol}>
              <Td className="font-semibold">{t.symbol}</Td>
              <Td right>{fmtInt(t.requests)}</Td>
              <Td right>{fmtPct(t.requests ? t.hits / t.requests : null)}</Td>
              <Td right>{fmtMs(t.latency_avg_ms)}</Td>
              <Td>
                <SignalBadge signal={t.signal} className="text-xs" />
              </Td>
            </tr>
          ))}
        </Table>
      </Panel>
    </div>
  );
}

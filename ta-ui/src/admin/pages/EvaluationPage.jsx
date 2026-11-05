import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import useApi from "../../lib/useApi.js";
import { getAdminEvaluation } from "../../lib/api.js";
import TradeoffChart, { conditionLabel } from "../TradeoffChart.jsx";
import {
  ErrorBox,
  PageHeader,
  Panel,
  Segmented,
  Select,
  Table,
  Td,
  fmtDateTime,
  fmtDuration,
  fmtInt,
  fmtMs,
  fmtTokens,
  fmtUsd,
} from "../ui.jsx";

const RANGES = [
  { value: "7", label: "7 hari" },
  { value: "30", label: "30 hari" },
  { value: "90", label: "90 hari" },
  { value: "3650", label: "Semua" },
];
const PROMPTS = [
  { value: "current", label: "Prompt aktif (AI sungguhan)" },
  { value: "stub", label: "Stub (uji pipeline / lab)" },
  { value: "all", label: "Semua versi prompt" },
];
const SOURCES = [
  { value: "all", label: "Semua request" },
  { value: "k6", label: "k6 (eksperimen)" },
  { value: "ui", label: "UI (pengunjung)" },
  { value: "pilot", label: "Pilot study" },
  { value: "manual", label: "Manual" },
];
const METHOD_FILTER = [
  { value: "all", label: "Semua metode" },
  { value: "debate", label: "Multi-Agent Debate" },
  { value: "mtf", label: "Multi-Timeframe" },
  { value: "grounded", label: "Grounded Brief" },
  { value: "summary", label: "Ringkasan tunggal (≤ hermes-v4)" },
];
const THETA_S = ["1", "2", "5", "10"];
const BIAS_OF = { BUY: "bullish", SELL: "bearish", HOLD: "netral" };

const pct = (v, d = 1) => (v == null ? "-" : `${v.toFixed(d)}%`);
const rowKey = (r) => `${r.condition}-${r.threshold_pct}-${r.pregen}`;

// Kolom CSV = nama field API apa adanya (ssr_pct dipecah per θ_s), untuk diolah di spreadsheet / Python.
function downloadCsv(rows, filename) {
  const flat = rows.map(({ ssr_pct, prompt_versions, quota_pct, requests_per_quota, ...r }) => ({
    ...r,
    ...Object.fromEntries(THETA_S.map((t) => [`ssr_${t}pct`, ssr_pct?.[t] ?? null])),
    ...Object.fromEntries(Object.entries(quota_pct ?? {}).map(([w, v]) => [`quota_${w.toLowerCase()}_pct`, v])),
    ...Object.fromEntries(Object.entries(requests_per_quota ?? {}).map(([w, v]) => [`requests_per_100pct_${w.toLowerCase()}`, v])),
    prompt_versions: (prompt_versions ?? []).join(" "),
  }));
  const cols = Object.keys(flat[0]);
  const esc = (v) => {
    if (v == null) return "";
    const s = String(v);
    return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
  };
  const csv = [cols.join(","), ...flat.map((r) => cols.map((c) => esc(r[c])).join(","))].join("\n");
  const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
  const a = Object.assign(document.createElement("a"), { href: url, download: filename });
  a.click();
  URL.revokeObjectURL(url);
}

// ΔL = (L_nc - L_c) / L_nc, ditampilkan sebagai penurunan terhadap baseline no_cache.
function DeltaL({ v, base }) {
  if (v == null || base == null || base === 0) return null;
  const d = (base - v) / base;
  return (
    <span className={`ml-1 text-[10px] ${Math.abs(d) < 0.005 ? "text-muted" : d > 0 ? "text-up" : "text-down"}`}>
      {d > 0 ? "−" : "+"}
      {Math.abs(d * 100).toFixed(0)}%
    </span>
  );
}

function Cond({ r }) {
  return (
    <Td className="whitespace-nowrap font-semibold">
      {conditionLabel(r)}
      {r.condition === "no_cache" && <span className="ml-2 text-[10px] font-normal text-muted">baseline</span>}
    </Td>
  );
}

// AI berlangganan: biaya = konsumsi kuota (backend/quota.go). Jendela Codex: Session (5 jam), Weekly.
function QuotaPanel({ data, rows, empty }) {
  const q = data?.quota;
  const windows = q?.windows ?? [];
  const money = (v) =>
    v == null ? "-" : new Intl.NumberFormat("id-ID", { style: "currency", currency: q.currency || "USD", maximumFractionDigits: 4 }).format(v);
  return (
    <Panel
      title="2b. Kuota langganan AI"
      aside={q ? `${q.provider}${q.plan ? ` · ${q.plan}` : ""} · dasar ${fmtTokens(q.tokens_basis)} token` : "quota_snapshots"}
    >
      {data?.quota_error && <p className="text-sm text-down">{data.quota_error}</p>}
      {!q && !data?.quota_error && (
        <p className="text-sm text-muted">
          Belum ada snapshot kuota di periode ini. Jalankan <code className="text-ink">ta-up quota</code> tiap 10 menit selama
          eksperimen (cron / Task Scheduler), serta tepat sebelum dan sesudah sesi uji.
        </p>
      )}
      {q && (
        <>
          <div className="mb-4 flex flex-wrap gap-3 text-xs">
            {windows.map((w) => (
              <div key={w.label} className="rounded-lg bg-panel-2 px-4 py-3">
                <div className="font-semibold text-ink">{w.label}</div>
                <div className="mt-1 text-muted">
                  terpakai {pct(w.consumed_pct, 2)} · {fmtInt(w.snapshots)} snapshot
                </div>
                <div className="text-muted">
                  {w.pct_per_1m_tokens != null ? `${w.pct_per_1m_tokens.toFixed(3)}% per 1 juta token` : "belum bisa dihitung"}
                </div>
              </div>
            ))}
          </div>
          {q.note && <p className="mb-3 text-xs text-hold">{q.note}</p>}
          <Table
            head={[
              "Kondisi",
              ...windows.map((w) => ({ label: `Kuota ${w.label}`, right: true })),
              ...windows.map((w) => ({ label: `Request per 100% ${w.label}`, right: true })),
              { label: "Biaya amortisasi", right: true },
            ]}
            empty={empty}
          >
            {rows.map((r) => (
              <tr key={rowKey(r)}>
                <Cond r={r} />
                {windows.map((w) => (
                  <Td key={`q-${w.label}`} right>
                    {pct(r.quota_pct?.[w.label], 3)}
                  </Td>
                ))}
                {windows.map((w) => (
                  <Td key={`c-${w.label}`} right className="font-semibold">
                    {r.requests_per_quota?.[w.label] != null ? fmtInt(Math.round(r.requests_per_quota[w.label])) : "-"}
                  </Td>
                ))}
                <Td right>{money(r.subscription_cost)}</Td>
              </tr>
            ))}
          </Table>
          <p className="mt-3 text-xs text-muted">
            Kuota kondisi = token (input + output) kondisi × persen kuota per token, dengan persen per token = kuota terpakai
            seluruh akun ÷ token semua analisis di periode snapshot. Request per 100% = kapasitas: berapa request yang bisa
            dilayani satu jendela penuh. Biaya amortisasi = kuota mingguan ÷ 100 × biaya langganan bulanan × 12/52
            (SUBSCRIPTION_FEE_MONTHLY{q.fee_monthly == null ? ", belum diisi di run.env" : ""}). Asumsi: kuota sebanding dengan
            token, dan akun tidak dipakai untuk keperluan lain selama pengukuran.
          </p>
        </>
      )}
    </Panel>
  );
}

export default function EvaluationPage() {
  // Filter awal bisa dari URL, mis. /admin/evaluation?prompt=stub&trigger=k6
  const [params] = useSearchParams();
  const [days, setDays] = useState(params.get("days") ?? "30");
  const [trigger, setTrigger] = useState(params.get("trigger") ?? "all");
  const [prompt, setPrompt] = useState(params.get("prompt") ?? "current");
  const [method, setMethod] = useState(params.get("method") ?? "all");
  const [thetaS, setThetaS] = useState("2");
  const { data, error, loading, reload } = useApi(
    (signal) => getAdminEvaluation({ days, trigger, prompt, method }, signal),
    [days, trigger, prompt, method]
  );
  const rows = data?.conditions ?? [];
  const base = rows.find((r) => r.condition === "no_cache");
  const empty = data && rows.length === 0 ? "Belum ada data untuk filter ini." : null;

  return (
    <div className="space-y-6">
      <PageHeader title="Evaluasi eksperimen" subtitle="Latency, cost token, dan freshness per kondisi (metrik Bab 2.7)">
        <Segmented label="Rentang" value={days} options={RANGES} onChange={setDays} />
        <Select label="Sumber" value={trigger} options={SOURCES} onChange={setTrigger} />
        <Select label="Prompt" value={prompt} options={PROMPTS} onChange={setPrompt} />
        <Select label="Metode" value={method} options={METHOD_FILTER} onChange={setMethod} />
        {rows.length > 0 && (
          <button
            onClick={() => downloadCsv(rows, `evaluasi-${data.days}hari-${data.trigger}-${data.method}.csv`)}
            className="rounded-lg border border-line px-3 py-2 text-xs font-semibold text-muted hover:text-ink"
          >
            Unduh CSV
          </button>
        )}
      </PageHeader>

      <ErrorBox error={error} onRetry={reload} />

      <div className={`space-y-6 ${loading && data ? "opacity-60 transition-opacity" : ""}`}>
        <Panel
          title="Trade-off: penghematan vs kebaruan"
          aside={<Segmented label="θ_s" value={thetaS} options={THETA_S.map((t) => ({ value: t, label: `θ_s ${t}%` }))} onChange={setThetaS} />}
        >
          <TradeoffChart rows={rows} thetaS={thetaS} />
          <p className="mt-2 text-xs text-muted">
            Kondisi terbaik berada di kiri atas (hemat dan jarang menyajikan analisis basi). no_cache = titik (0, 0).
          </p>
        </Panel>

        <Panel title="1. Latency dan cache hit rate" aside="Request pengguna (pre-generation tidak dihitung)">
          <Table
            head={[
              "Kondisi",
              { label: "N", right: true },
              { label: "H (hit rate)", right: true },
              { label: "Rata-rata", right: true },
              { label: "p50", right: true },
              { label: "p95", right: true },
              { label: "p99", right: true },
              { label: "HIT / AI", right: true },
              { label: "Error", right: true },
            ]}
            empty={empty}
          >
            {rows.map((r) => (
              <tr key={rowKey(r)}>
                <Cond r={r} />
                <Td right>{fmtInt(r.requests)}</Td>
                <Td right>{pct(r.hit_rate_pct)}</Td>
                <Td right>
                  {fmtMs(r.latency_avg_ms)}
                  {r !== base && <DeltaL v={r.latency_avg_ms} base={base?.latency_avg_ms} />}
                </Td>
                <Td right>{fmtMs(r.latency_p50_ms)}</Td>
                <Td right>{fmtMs(r.latency_p95_ms)}</Td>
                <Td right>{fmtMs(r.latency_p99_ms)}</Td>
                <Td right>
                  {fmtMs(r.latency_hit_avg_ms)} / {fmtMs(r.latency_ai_avg_ms)}
                </Td>
                <Td right className={r.errors > 0 ? "text-down" : ""}>
                  {fmtInt(r.errors)}
                </Td>
              </tr>
            ))}
          </Table>
          <p className="mt-3 text-xs text-muted">Angka hijau di samping rata-rata = ΔL, penurunan latency terhadap baseline no_cache.</p>
        </Panel>

        <Panel title="2. Cost token dan wasted inference">
          <Table
            head={[
              "Kondisi",
              { label: "N_gen", right: true },
              { label: "N_pre", right: true },
              { label: "Token / request", right: true },
              { label: "Token total", right: true },
              { label: "Biaya", right: true },
              { label: "S (hemat)", right: true },
              { label: "W (pregen terbuang)", right: true },
            ]}
            empty={empty}
          >
            {rows.map((r) => (
              <tr key={rowKey(r)}>
                <Cond r={r} />
                <Td right>{fmtInt(r.n_gen)}</Td>
                <Td right>{r.pregen ? fmtInt(r.n_pre) : "-"}</Td>
                <Td right>{fmtTokens(r.tokens_per_request)}</Td>
                <Td right>{fmtTokens(r.tokens_in + r.tokens_out)}</Td>
                <Td right>{fmtUsd(r.cost_usd)}</Td>
                <Td right className="font-semibold">
                  {pct(r.savings_pct)}
                </Td>
                <Td right>
                  {r.pregen ? (
                    <>
                      {pct(r.wasted_inference_pct)}
                      <span className="ml-1 text-muted">
                        ({fmtInt(r.n_pre_unused)}/{fmtInt(r.n_pre_ended)})
                      </span>
                    </>
                  ) : (
                    "-"
                  )}
                </Td>
              </tr>
            ))}
          </Table>
          <p className="mt-3 text-xs text-muted">
            S = (C_nc − C) / C_nc dengan C = N_gen × c̄ (N_gen = N_miss + N_pre), sehingga cost pre-generation yang terbuang
            sudah ikut dihitung. W = entri pre-generation yang tidak pernah disajikan sampai kedaluwarsa atau tergantikan.
          </p>
        </Panel>

        <QuotaPanel data={data} rows={rows} empty={empty} />

        <Panel title="3. Freshness" aside="D_serve, SSR, dan SFR dihitung pada setiap cache HIT">
          <Table
            head={[
              "Kondisi",
              { label: "Data age", right: true },
              { label: "Cache age (p95)", right: true },
              { label: "D_serve rata-rata (p95)", right: true },
              ...THETA_S.map((t) => ({ label: `SSR ${t}%`, right: true })),
              { label: "SFR", right: true },
              { label: "Invalidasi harga / S", right: true },
              { label: "L_inv p50 (p95)", right: true },
            ]}
            empty={empty}
          >
            {rows.map((r) => (
              <tr key={rowKey(r)}>
                <Cond r={r} />
                <Td right>{fmtDuration(r.data_age_avg_s)}</Td>
                <Td right>
                  {fmtDuration(r.cache_age_avg_s)} ({fmtDuration(r.cache_age_p95_s)})
                </Td>
                <Td right>
                  {pct(r.d_serve_avg_pct, 3)} ({pct(r.d_serve_p95_pct, 3)})
                </Td>
                {THETA_S.map((t) => (
                  <Td
                    key={t}
                    right
                    className={r.threshold_pct != null && String(r.threshold_pct) === t ? "font-semibold text-ink" : ""}
                  >
                    {r.condition === "no_cache" ? "-" : pct(r.ssr_pct?.[t], 2)}
                  </Td>
                ))}
                <Td right>{r.sfr_base > 0 ? pct(r.sfr_pct) : "-"}</Td>
                <Td right>
                  {r.threshold_pct != null ? `${fmtInt(r.inv_price_move)} / ${fmtInt(r.inv_state_change)}` : "-"}
                </Td>
                <Td right>
                  {r.l_inv_p50_ms != null ? `${fmtMs(r.l_inv_p50_ms)} (${fmtMs(r.l_inv_p95_ms)})` : "-"}
                </Td>
              </tr>
            ))}
          </Table>
          <p className="mt-3 text-xs text-muted">
            SSR θ_s = porsi HIT dengan drift harga melebihi θ_s; angka tebal = θ_s sama dengan θ kondisi itu. SFR = porsi HIT
            yang kondisi indikatornya (zona RSI, harga vs EMA20, tanda histogram MACD) sudah berbeda dari saat generate. L_inv
            = waktu invalidasi dikurangi waktu transaksi yang pertama kali melewati ambang (watcher).
          </p>
          {rows.length > 0 && (
            <p className="mt-1 text-xs text-muted">
              Data dari {fmtDateTime(rows.reduce((m, r) => (r.first_at < m ? r.first_at : m), rows[0].first_at))} sampai{" "}
              {fmtDateTime(rows.reduce((m, r) => (r.last_at > m ? r.last_at : m), rows[0].last_at))}.
            </p>
          )}
        </Panel>

        <Panel title="Akurasi bias (backtest 24 jam)" aside="bullish benar jika naik > band, bearish turun > band, netral dalam band">
          <Table
            head={["Instrumen", "Metode", "Bias", { label: "n", right: true }, { label: "Benar", right: true }, { label: "Akurasi", right: true }, { label: "Return 24 jam", right: true }]}
            empty={data && data.accuracy.length === 0 ? "Belum ada analisis yang di-backtest." : null}
          >
            {data?.accuracy.map((a) => (
              <tr key={`${a.symbol}-${a.method}-${a.signal}`}>
                <Td className="font-semibold">{a.symbol}</Td>
                <Td className="text-muted">{a.method}</Td>
                <Td>{BIAS_OF[a.signal] ?? a.signal}</Td>
                <Td right>{fmtInt(a.n)}</Td>
                <Td right>{fmtInt(a.correct)}</Td>
                <Td right>{pct((a.correct / a.n) * 100, 0)}</Td>
                <Td right>{a.avg_return_pct != null ? `${a.avg_return_pct > 0 ? "+" : ""}${a.avg_return_pct.toFixed(2)}%` : "-"}</Td>
              </tr>
            ))}
          </Table>
        </Panel>
      </div>
    </div>
  );
}

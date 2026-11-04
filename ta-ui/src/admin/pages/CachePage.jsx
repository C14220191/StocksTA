import { useEffect, useState } from "react";
import useApi from "../../lib/useApi.js";
import { getAdminCache, invalidateCache } from "../../lib/api.js";
import { formatPlain } from "../../lib/format.js";
import SignalBadge from "../../components/SignalBadge.jsx";
import { METHODS } from "../../lib/methods.js";
import {
  ErrorBox,
  Kpi,
  ModeChip,
  PageHeader,
  Panel,
  StatusDot,
  Table,
  Td,
  fmtDateTime,
  fmtDuration,
  fmtInt,
  fmtPct,
  fmtSignedPct,
  usePolling,
} from "../ui.jsx";

const CACHE_STATUS = {
  cached: { status: "ok", label: "Tersimpan" },
  expiring: { status: "warn", label: "Hampir habis" },
  empty: { status: "idle", label: "Kosong" },
  disabled: { status: "idle", label: "Cache nonaktif" },
};
const JOB_LABEL = {
  ingest_1h: "Ingestion candle 1h",
  ingest_1d: "Ingestion candle 1d",
  backtest: "Backtest bias 24 jam",
  fundamentals: "Fundamental saham (Yahoo)",
};
const METHOD_SHORT = { debate: "Debate", mtf: "Multi-timeframe", grounded: "Grounded" };
const REASON = { price_move: "Harga ≥ θ", state_change: "Kondisi S berubah", manual: "Manual (admin)", ttl_expired: "TTL habis" };
const DETECTED = { watcher: "watcher", request: "saat request", scheduler: "scheduler", admin: "admin" };
// "rsi:neutral;ema20:above;macd:pos" -> "RSI netral · di atas EMA20 · MACD +"
function fmtState(s) {
  if (!s) return "-";
  const m = Object.fromEntries(s.split(";").map((kv) => kv.split(":")));
  const rsi = { neutral: "netral", overbought: "overbought", oversold: "oversold" }[m.rsi] ?? m.rsi;
  return `RSI ${rsi} · ${m.ema20 === "above" ? "di atas" : "di bawah"} EMA20 · MACD ${m.macd === "pos" ? "+" : "−"}`;
}

// Detik berjalan sejak data terakhir dimuat, untuk hitung mundur TTL tanpa memanggil backend.
function useElapsed(stamp) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  return Math.max(0, (now - stamp) / 1000);
}

function InvalidateButton({ symbol, method, disabled, onDone }) {
  const [state, setState] = useState("idle"); // idle | confirm | busy | error
  useEffect(() => {
    if (state !== "confirm") return;
    const t = setTimeout(() => setState("idle"), 4000);
    return () => clearTimeout(t);
  }, [state]);

  const click = async () => {
    if (state === "idle") return setState("confirm");
    setState("busy");
    try {
      await invalidateCache(symbol, method);
      setState("idle");
      onDone();
    } catch {
      setState("error");
    }
  };
  return (
    <button
      disabled={disabled || state === "busy"}
      onClick={click}
      className={`rounded-md border px-3 py-1 text-xs font-semibold transition-colors disabled:cursor-not-allowed disabled:opacity-30 ${
        state === "confirm" ? "border-status-critical text-ink" : "border-line text-muted hover:text-ink"
      }`}
    >
      {state === "confirm" ? "Yakin? klik lagi" : state === "busy" ? "..." : state === "error" ? "Gagal, ulangi" : "Invalidate"}
    </button>
  );
}

export default function CachePage() {
  const { data, error, loading, reload } = useApi((signal) => getAdminCache(signal), []);
  usePolling(reload, 10000);
  const [stamp, setStamp] = useState(Date.now());
  useEffect(() => setStamp(Date.now()), [data]);
  const elapsed = useElapsed(stamp);

  const k = data?.kpi;
  const q = k?.ai_queue;
  const th = data?.invalidation_threshold_pct;
  const disabled = data?.cache_mode === "no_cache";
  const busy = loading && !data;
  const p = data?.params;
  const dyn = p && `TTL = clamp(${fmtDuration(+p.TTL_BASE_SECONDS)} / (1 + ${p.DYNAMIC_TTL_K}·σ/σ_ref), ${fmtDuration(+p.DYNAMIC_TTL_MIN_SECONDS)}, ${fmtDuration(+p.DYNAMIC_TTL_MAX_SECONDS)}), σ_ref = ${p.SIGMA_REF_STAT}`;
  const ev = `buang saat drift ≥ ${th}%${data?.invalidate_on_state ? " atau kondisi S berubah" : ""}`;
  const ttlRule = {
    no_cache: "tidak ada cache, setiap request memanggil AI",
    static_ttl: p && `TTL tetap = TTL_base ${fmtDuration(+p.TTL_BASE_SECONDS)}`,
    dynamic_ttl: dyn,
    event_only: p && `${ev} (watcher proaktif; batas pengaman ${fmtDuration(+p.EVENT_MAX_TTL_SECONDS)})`,
    hybrid: p && `${dyn} + ${ev}`,
  }[data?.cache_mode];
  const pg = data?.pregen;

  return (
    <div className="space-y-6">
      <PageHeader title="Cache & Job monitor" subtitle="Status cache per instrumen dan job terjadwal · diperbarui tiap 10 detik">
        <ModeChip mode={data?.cache_mode} threshold={th} />
      </PageHeader>
      {ttlRule && (
        <p className="-mt-3 text-xs text-muted">
          Aturan aktif: {ttlRule} · prompt {data.prompt_version}
          {pg && ` · pre-generation tiap ${fmtDuration(pg.interval_ns / 1e9)} untuk top-${pg.top_n} (akses ${pg.window_hours} jam terakhir)`}
        </p>
      )}

      <ErrorBox error={error} onRetry={reload} />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <Kpi
          loading={busy}
          label="Cache hit ratio (24 jam)"
          value={disabled ? "-" : fmtPct(k?.hit_ratio_24h)}
          sub={k && (disabled ? "cache nonaktif (no_cache)" : `${fmtInt(k.hits_24h)} HIT · ${fmtInt(k.misses_24h)} MISS`)}
        />
        <Kpi loading={busy} label="Invalidasi (24 jam)" value={fmtInt(k?.invalidations_24h)} sub="harga bergerak + manual" />
        <Kpi
          loading={busy}
          label="Antrean AI (Hermes)"
          value={q ? `${q.running} jalan` : "-"}
          sub={q ? `${q.waiting} menunggu · ${q.completed} selesai · ${q.failed + q.rejected} gagal` : "AI service tidak terhubung"}
        />
        <Kpi
          loading={busy}
          label="Pre-generate (24 jam)"
          value={pg ? fmtInt(k?.scheduled_24h) : "-"}
          sub={pg ? `top-${pg.top_n} instrumen populer, tiap ${fmtDuration(pg.interval_ns / 1e9)}` : "pre-generation nonaktif (PREGEN=off)"}
        />
      </div>

      <Panel title="Status cache per instrumen" aside={th != null ? `Deviasi dibandingkan ambang ${th}%` : null}>
        <Table
          head={[
            "Instrumen",
            "Metode",
            { label: "Akses", right: true },
            "Status",
            "Asal",
            { label: "Sisa TTL", right: true },
            "Dibuat",
            "Bias",
            "Kondisi S (saat dibuat → sekarang)",
            { label: "Harga saat dibuat", right: true },
            { label: "Harga sekarang", right: true },
            { label: "Deviasi", right: true },
            { label: "TTL terakhir", right: true },
            "",
          ]}
        >
          {data?.instruments.map((r) => {
            const st = CACHE_STATUS[r.status] ?? CACHE_STATUS.empty;
            const ttlLeft = r.ttl_seconds != null ? r.ttl_seconds - elapsed : null;
            const near = th != null && r.deviation_pct != null && Math.abs(r.deviation_pct) >= th * 0.75;
            return (
              <tr key={`${r.symbol}-${r.method}`}>
                <Td className="font-semibold">{r.symbol}</Td>
                <Td className="text-muted" title={METHODS.find((m) => m.id === r.method)?.name}>
                  {METHOD_SHORT[r.method] ?? r.method}
                </Td>
                <Td right>{fmtInt(r.access)}</Td>
                <Td>
                  <StatusDot status={st.status} label={st.label} />
                </Td>
                <Td className="text-muted">{r.origin === "pregen" ? "pre-generation" : r.origin ? "on-demand" : "-"}</Td>
                <Td right>{ttlLeft != null ? fmtDuration(Math.max(0, ttlLeft)) : r.status === "cached" ? "tanpa batas" : "-"}</Td>
                <Td>{fmtDateTime(r.generated_at)}</Td>
                <Td>{r.signal ? <SignalBadge signal={r.signal} className="text-xs" /> : "-"}</Td>
                <Td className={`whitespace-nowrap ${r.state_gen && r.state_now && r.state_gen !== r.state_now ? "text-hold" : "text-muted"}`}>
                  {r.state_gen ? (r.state_gen === r.state_now ? fmtState(r.state_gen) : `${fmtState(r.state_gen)} → ${fmtState(r.state_now)}`) : "-"}
                </Td>
                <Td right>{r.price_at_generation != null ? formatPlain(r.price_at_generation) : "-"}</Td>
                <Td right>
                  {r.price_now != null ? formatPlain(r.price_now) : "-"}
                  {r.price_source && <span className="ml-1 text-muted">({r.price_source})</span>}
                </Td>
                <Td right className={near ? "font-semibold text-hold" : ""}>
                  {fmtSignedPct(r.deviation_pct)}
                </Td>
                <Td right>{r.last_ttl_applied != null ? fmtDuration(r.last_ttl_applied) : "-"}</Td>
                <Td>
                  <InvalidateButton
                    symbol={r.symbol}
                    method={r.method}
                    disabled={disabled || !(r.status === "cached" || r.status === "expiring")}
                    onDone={reload}
                  />
                </Td>
              </tr>
            );
          })}
        </Table>
        <p className="mt-3 text-xs text-muted">
          Satu baris per instrumen per metode analisis (entri cache terpisah, aturan invalidasi sama). Akses = jumlah
          request analisis dalam window popularitas (dasar pre-generation; satu kunjungan Detail = satu request per metode).
          Hampir habis = sisa TTL di bawah 5 menit.
          Deviasi dan kondisi S berwarna kuning saat mendekati ambang / sudah berubah. Invalidate manual tercatat di
          cache_events dengan alasan "manual" — jangan dipakai saat eksperimen berjalan.
        </p>
      </Panel>

      <div className="grid gap-6 xl:grid-cols-2">
        <Panel title="Job terjadwal">
          {data?.jobs_error && <p className="mb-3 text-xs text-hold">{data.jobs_error}</p>}
          <Table head={["Job", "Jadwal", "Terakhir", "Status", { label: "24 jam", right: true }]}>
            {data?.jobs?.map((j) => (
              <tr key={j.job}>
                <Td className="font-semibold">{JOB_LABEL[j.job] ?? j.job}</Td>
                <Td className="text-muted">{j.expected}</Td>
                <Td>{fmtDateTime(j.last_run)}</Td>
                <Td>
                  <StatusDot
                    status={j.last_status === "fail" ? "down" : j.stale ? "warn" : "ok"}
                    label={j.last_status === "fail" ? "Gagal" : j.stale ? (j.last_run ? "Terlambat" : "Belum pernah") : "Berjalan"}
                  />
                </Td>
                <Td right>
                  {j.runs_24h} run{j.fails_24h > 0 && <span className="text-down"> · {j.fails_24h} gagal</span>}
                </Td>
              </tr>
            ))}
          </Table>
          {k && (
            <p className="mt-3 text-xs text-muted">
              {fmtInt(k.backtest_pending)} ringkasan AI sudah berumur 24 jam tapi belum di-backtest.
            </p>
          )}
        </Panel>

        <Panel title="Invalidasi terakhir">
          <Table
            head={["Waktu", "Instrumen", "Alasan", "Deteksi", { label: "Perubahan harga", right: true }, { label: "L_inv", right: true }]}
            empty={data && data.events.length === 0 ? "Belum ada invalidasi." : null}
          >
            {data?.events.map((e, i) => (
              <tr key={i}>
                <Td>{fmtDateTime(e.at)}</Td>
                <Td className="font-semibold">{e.symbol}</Td>
                <Td title={e.state_from ? `${fmtState(e.state_from)} → ${fmtState(e.state_to)}` : undefined}>
                  {REASON[e.reason] ?? e.reason}
                </Td>
                <Td className="text-muted">{DETECTED[e.detected_by] ?? "-"}</Td>
                <Td right>{fmtSignedPct(e.change_pct)}</Td>
                <Td right>{e.l_inv_ms != null ? `${e.l_inv_ms.toFixed(0)} ms` : "-"}</Td>
              </tr>
            ))}
          </Table>
        </Panel>
      </div>

      <Panel title="Riwayat eksekusi job" aside="30 terakhir">
        <Table
          head={["Selesai", "Job", "Target", "Status", { label: "Baris", right: true }, "Pesan"]}
          empty={data && (data.job_runs ?? []).length === 0 ? "Belum ada riwayat. Jalankan .\\ta-up.ps1 ingest." : null}
        >
          {data?.job_runs?.map((j, i) => (
            <tr key={i}>
              <Td>{fmtDateTime(j.finished_at)}</Td>
              <Td>{JOB_LABEL[j.job] ?? j.job}</Td>
              <Td className="font-semibold">{j.target ?? "-"}</Td>
              <Td>
                <StatusDot status={j.status === "ok" ? "ok" : "down"} label={j.status === "ok" ? "OK" : "Gagal"} />
              </Td>
              <Td right>{fmtInt(j.rows)}</Td>
              <Td className="max-w-md truncate text-muted">{j.message ?? ""}</Td>
            </tr>
          ))}
        </Table>
      </Panel>
    </div>
  );
}

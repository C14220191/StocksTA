import { useEffect, useState } from "react";
import SignalBadge from "./SignalBadge.jsx";
import { analyzeSymbol } from "../lib/api.js";
import { BIAS_WORD, METHODS } from "../lib/methods.js";
import {
  ageSecondsFromIso,
  formatAgeSeconds,
  formatCandleTime,
  formatChange,
  formatPrice,
  localizeUtcTimes,
} from "../lib/format.js";

// Harga bergerak sejauh ini (persen) sejak analisis dibuat -> tampilkan peringatan di kartu.
const MOVE_WARN_PCT = 1;

// /api/analyze dipanggil sekali per metode per kunjungan, dan hanya setelah instrumennya terbukti ada.
// Ketiganya dikirim bersamaan; AI service mengantre proses Hermes, jadi kartu selesai satu per satu.
export function useAnalyses(symbol, enabled) {
  const [state, setState] = useState({});
  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    setState(Object.fromEntries(METHODS.map((m) => [m.id, { data: null, error: null, loading: true }])));
    for (const m of METHODS) {
      analyzeSymbol(symbol, m.id)
        .then((data) => !cancelled && setState((s) => ({ ...s, [m.id]: { data, error: null, loading: false } })))
        .catch((error) => !cancelled && setState((s) => ({ ...s, [m.id]: { data: null, error, loading: false } })));
    }
    return () => {
      cancelled = true;
    };
  }, [symbol, enabled]);
  return state;
}

// Asal analisis yang tampil: dibuat AI untuk kunjungan ini (MISS/BYPASS), dari cache (HIT),
// atau analisis tersimpan terakhir yang ditampilkan sambil menunggu /api/analyze.
function analysisOrigin(fresh) {
  if (!fresh) return "Analisis tersimpan, dibuat";
  if (fresh.cache_status === "HIT") return fresh.origin === "pregen" ? "Dari cache (pre-generation), dianalisis" : "Dari cache, dianalisis";
  return "Analisis baru, dibuat";
}

const modelLabel = (m) => (!m || m === "(default hermes)" ? "model default Hermes" : m);

export function AiDisclaimer({ model, withFundamentals }) {
  return (
    <div role="note" className="mt-3 rounded-lg border border-hold/40 bg-hold/10 px-4 py-3 font-sans text-xs leading-relaxed">
      <strong className="text-ink">Dibuat oleh AI, bukan ajakan berinvestasi.</strong>{" "}
      <span className="text-muted">
        Ketiga analisis di bawah dibuat otomatis oleh AI ({modelLabel(model)} melalui Hermes Agent) dari data harga,
        indikator teknikal{withFundamentals ? ", dan laporan keuangan Yahoo Finance" : ""}. Isinya deskriptif dan bisa
        keliru. Bukan rekomendasi, ajakan, atau nasihat untuk membeli, menjual, atau menahan aset apa pun; keputusan
        investasi sepenuhnya tanggung jawab Anda.
      </span>
    </div>
  );
}

function Bias({ word }) {
  const b = BIAS_WORD[word];
  return <span className={`font-semibold ${b?.cls ?? "text-muted"}`}>{b?.label ?? "-"}</span>;
}

function Confidence({ value }) {
  if (value == null) return null;
  return (
    <span className="flex items-center gap-2 text-xs text-muted" title="Keyakinan menurut AI (0-100)">
      <span className="h-1.5 w-16 overflow-hidden rounded-full bg-panel-2">
        <span className="block h-full rounded-full bg-hold" style={{ width: `${value}%` }} />
      </span>
      keyakinan {value}/100
    </span>
  );
}

function Section({ title, children }) {
  return (
    <div className="mt-4">
      <h4 className="text-xs font-bold uppercase tracking-wide text-muted">{title}</h4>
      <div className="mt-2 text-sm leading-relaxed">{children}</div>
    </div>
  );
}

function List({ items, ordered }) {
  if (!items?.length) return <p className="text-muted">-</p>;
  const Tag = ordered ? "ol" : "ul";
  return (
    <Tag className={`space-y-1 pl-5 ${ordered ? "list-decimal" : "list-disc"}`}>
      {items.map((x, i) => (
        <li key={i}>{x}</li>
      ))}
    </Tag>
  );
}

function SimpleTable({ head, rows }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[32rem] text-left text-sm">
        <thead className="text-xs text-muted">
          <tr>
            {head.map((h) => (
              <th key={h} className="py-2 pr-4 font-semibold">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={i} className="border-t border-line align-top">
              {r.map((c, j) => (
                <td key={j} className="py-2 pr-4">
                  {c}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

const ROLE = { fundamental: "Fundamental", technical: "Teknikal", sentiment: "Sentimen" };

function DebateDetail({ d }) {
  return (
    <>
      <Section title="Tahap 1 · Analis independen">
        <SimpleTable
          head={["Analis", "Sinyal", "Keyakinan", "Alasan"]}
          rows={(d.analysts ?? []).map((a) => [ROLE[a.role] ?? a.role, <Bias word={a.signal} />, a.confidence ?? "-", a.reason || "-"])}
        />
      </Section>
      <Section title="Tahap 2 · Debat bull vs bear">
        <div className="space-y-3">
          {(d.debate ?? []).map((r) => (
            <div key={r.round} className="rounded-lg bg-panel-2 px-4 py-3">
              <div className="text-xs font-bold text-muted">Ronde {r.round}</div>
              <p className="mt-1">
                <span className="font-semibold text-up">Bull:</span> {r.bull || "-"}
              </p>
              <p className="mt-1">
                <span className="font-semibold text-down">Bear:</span> {r.bear || "-"}
              </p>
            </div>
          ))}
        </div>
      </Section>
      <Section title="Tahap 3 · Risk manager">
        <p>
          <span className="text-muted">Skenario terburuk:</span> {d.risk?.worst_case || "-"}
        </p>
        <p className="mt-1">
          <span className="text-muted">Volatilitas:</span> {d.risk?.volatility || "-"}
        </p>
      </Section>
      <Section title="Tahap 4 · Kondisi yang akan mengubah pandangan">
        <List items={d.change_conditions} />
      </Section>
      {d.missing_data?.length > 0 && (
        <p className="mt-3 text-xs text-muted">Data yang tidak tersedia: {d.missing_data.join(", ")}.</p>
      )}
    </>
  );
}

const FACTORS = [
  ["trend", "Trend"],
  ["structure", "Struktur"],
  ["level", "Level S/R"],
  ["rsi", "RSI"],
  ["volume", "Volume"],
];

function MtfDetail({ d, currency }) {
  const c = d.confluence ?? {};
  const clear = d.setup === "jelas";
  return (
    <>
      <Section title="Top-down 1D → 4H → 1H">
        <SimpleTable
          head={["Timeframe", "Trend", "Struktur", "Selaras 1D", "Catatan"]}
          rows={(d.timeframes ?? []).map((t) => [
            t.tf,
            t.trend || "-",
            t.structure || "-",
            t.tf === "1D" ? "acuan" : t.aligned == null ? "-" : t.aligned ? "ya" : "tidak",
            t.note || "-",
          ])}
        />
      </Section>
      <Section title={`Confluence score ${c.score ?? 0}/5`}>
        <div className="flex flex-wrap gap-2">
          {FACTORS.map(([k, label]) => (
            <span
              key={k}
              className={`rounded-md px-2 py-1 text-xs font-semibold ${c[k] ? "bg-up/15 text-up" : "bg-panel-2 text-muted"}`}
            >
              {c[k] ? "✓" : "✗"} {label}
            </span>
          ))}
        </div>
        <p className={`mt-2 text-xs font-semibold ${clear ? "text-up" : "text-hold"}`}>
          {clear ? "Setup jelas (skor minimal 4)" : "Tidak ada setup yang jelas (skor di bawah 4)"}
        </p>
      </Section>
      <Section title="Skenario">
        <p>
          <span className="font-semibold text-up">Bullish:</span> {d.scenarios?.bullish || "-"}
        </p>
        <p className="mt-1">
          <span className="font-semibold text-down">Bearish:</span> {d.scenarios?.bearish || "-"}
        </p>
        <p className="mt-1">
          <span className="font-semibold">Pembatal analisis:</span> {d.scenarios?.invalidation || "-"}
          {d.invalidation_level != null && <> (level {formatPrice(d.invalidation_level, currency, { short: true })})</>}
        </p>
      </Section>
    </>
  );
}

const TREND_CLS = { naik: "text-up", turun: "text-down", stabil: "text-hold" };

function GroundedDetail({ d }) {
  return (
    <>
      <Section title="Pandangan dominan yang diuji">
        <p>{d.thesis || "-"}</p>
        {d.thesis_holds != null && (
          <p className={`mt-1 text-xs font-semibold ${d.thesis_holds ? "text-up" : "text-down"}`}>
            {d.thesis_holds ? "Masih bertahan setelah dibantah" : "Tidak bertahan setelah dibantah"}
          </p>
        )}
      </Section>
      <Section title="A · Research brief">
        <p>{d.brief || "-"}</p>
        {d.metrics?.length > 0 && (
          <div className="mt-3">
            <SimpleTable
              head={["Metrik", "Nilai", "Tren", "Sumber"]}
              rows={d.metrics.map((m) => [
                m.name,
                m.value || "-",
                <span className={TREND_CLS[m.trend] ?? "text-muted"}>{m.trend}</span>,
                <span className="text-muted">{m.source || "-"}</span>,
              ])}
            />
          </div>
        )}
      </Section>
      <Section title="Red flags">
        <List items={d.red_flags} />
      </Section>
      <Section title="B · Devil's advocate: 5 kelemahan terbesar">
        <List items={d.weaknesses} ordered />
      </Section>
      <Section title="Data yang akan membuktikan pandangan ini salah">
        <List items={d.falsifiers} />
      </Section>
    </>
  );
}

const DETAIL = { debate: DebateDetail, mtf: MtfDetail, grounded: GroundedDetail };

function AboutMethod({ m }) {
  return (
    <details className="mt-5 rounded-lg bg-panel-2 px-4 py-3 text-xs leading-relaxed">
      <summary className="cursor-pointer font-semibold text-muted hover:text-ink">Tentang metode ini</summary>
      <p className="mt-2">
        <span className="text-muted">Cocok untuk:</span> {m.fit}
      </p>
      <ul className="mt-2 list-disc space-y-1 pl-5">
        {m.how.map((x) => (
          <li key={x}>{x}</li>
        ))}
      </ul>
      <p className="mt-2">
        <span className="text-muted">Penyesuaian di aplikasi ini:</span> {m.adapted}
      </p>
      <p className="mt-1">
        <span className="text-muted">Kelemahan:</span> {m.weakness}
      </p>
    </details>
  );
}

function MethodCard({ m, latest, state = {}, currency, livePrice }) {
  const fresh = state.data;
  // analisis baru yang formatnya tidak valid tidak menggantikan analisis tersimpan yang valid
  const invalid = fresh && !fresh.detail;
  const shown = fresh && !invalid ? fresh : latest ?? fresh;
  const d = shown?.detail;
  const Detail = DETAIL[m.id];
  const hasLevels = shown?.support != null && shown?.resistance != null;
  // source_data_ts = waktu BUKA candle 1h terakhir yang dilihat AI; candle itu tutup 1 jam kemudian.
  const dataUntil = shown?.source_data_ts ? new Date(shown.source_data_ts).getTime() / 1000 + 3600 : null;
  const priceAtGen = shown?.price_at_generation > 0 ? shown.price_at_generation : null;
  // Kripto: dihitung ulang dari harga live supaya ikut bergerak; saham: nilai dari backend.
  const deviation = priceAtGen && livePrice ? (livePrice / priceAtGen - 1) * 100 : fresh?.price_deviation_pct ?? null;
  const moved = deviation != null && Math.abs(deviation) >= MOVE_WARN_PCT;

  return (
    <article className="rounded-xl border border-line bg-panel p-6 font-sans" aria-live="polite">
      <header className="flex flex-wrap items-start justify-between gap-x-6 gap-y-2">
        <div>
          <h3 className="text-base font-semibold">{m.name}</h3>
          <p className="mt-0.5 text-xs text-muted">Metode: {m.source}</p>
        </div>
        {shown && (
          <div className="flex flex-col items-end gap-1">
            <span className="text-xs text-muted">
              Bias: <SignalBadge signal={shown.signal} className="text-sm" />
            </span>
            <Confidence value={shown.confidence} />
          </div>
        )}
      </header>

      {!shown && state.loading && (
        <div className="mt-4 space-y-2" aria-busy="true">
          <div className="h-3 w-full animate-pulse rounded bg-panel-2" />
          <div className="h-3 w-11/12 animate-pulse rounded bg-panel-2" />
          <div className="h-3 w-4/5 animate-pulse rounded bg-panel-2" />
          <p className="pt-2 text-xs text-muted">Menyiapkan analisis, bisa memakan 10 detik sampai 1 menit...</p>
        </div>
      )}
      {!shown && state.error && <p className="mt-3 text-sm text-down">{state.error.message}</p>}

      {shown && (
        <>
          <p className="mt-4 text-sm leading-relaxed">{localizeUtcTimes(shown.summary, shown.generated_at)}</p>
          {d && <Detail d={d} currency={currency} />}
          {!d && (
            <p className="mt-3 text-xs text-hold">
              Format jawaban AI tidak valid, jadi rincian metode tidak bisa ditampilkan dan analisis ini tidak disimpan di
              cache.
            </p>
          )}
          {d?.advice_terms?.length > 0 && (
            <p className="mt-3 text-xs text-hold">
              Teks AI memuat istilah transaksi ({d.advice_terms.join(", ")}). Abaikan sebagai saran; analisis ini hanya
              deskriptif.
            </p>
          )}
          {hasLevels && (
            <p className="mt-4 text-xs text-muted">
              Level menurut AI: support {formatPrice(shown.support, currency, { short: true })} · resistance{" "}
              {formatPrice(shown.resistance, currency, { short: true })}
            </p>
          )}
          <p className="mt-2 text-xs text-muted">
            {analysisOrigin(fresh && !invalid ? fresh : null)} {formatCandleTime(new Date(shown.generated_at).getTime() / 1000)} (
            {formatAgeSeconds(ageSecondsFromIso(shown.generated_at))})
            {dataUntil && <> · data candle hingga {formatCandleTime(dataUntil)}</>}
            {priceAtGen && <> · harga saat analisis {formatPrice(priceAtGen, currency, { short: true })}</>}
          </p>
          {moved && (
            <p className="mt-2 text-xs text-hold">
              Harga sudah bergerak {formatChange(deviation)} sejak analisis ini dibuat, isinya mungkin kurang relevan.
            </p>
          )}
          {state.loading && (
            <p className="mt-2 flex items-center gap-2 text-xs text-hold">
              <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-hold" />
              Memperbarui analisis, bisa memakan 10 detik sampai 1 menit jika belum ada di cache...
            </p>
          )}
          {invalid && latest && (
            <p className="mt-2 text-xs text-hold">Analisis baru gagal diformat; menampilkan analisis valid sebelumnya.</p>
          )}
          {state.error && (
            <p className="mt-2 text-xs text-hold">
              Pembaruan analisis gagal ({state.error.message}). Menampilkan analisis yang tersimpan.
            </p>
          )}
        </>
      )}

      <AboutMethod m={m} />
    </article>
  );
}

// latest = ai_latest dari /api/instruments/:symbol (per metode), analyses = hasil useAnalyses.
export default function AiAnalysis({ latest, analyses, currency, livePrice, assetType }) {
  const model = METHODS.map((m) => analyses[m.id]?.data?.model ?? latest?.[m.id]?.model).find(Boolean);
  return (
    <section className="mt-8">
      <h2 className="text-base font-semibold">Analisis AI</h2>
      <AiDisclaimer model={model} withFundamentals={assetType === "stock"} />
      <div className="mt-4 space-y-4">
        {METHODS.map((m) => (
          <MethodCard
            key={m.id}
            m={m}
            latest={latest?.[m.id]}
            state={analyses[m.id]}
            currency={currency}
            livePrice={livePrice}
          />
        ))}
      </div>
    </section>
  );
}

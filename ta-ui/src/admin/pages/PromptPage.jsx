import { useState } from "react";
import useApi from "../../lib/useApi.js";
import { getAdminPrompt, getOverview } from "../../lib/api.js";
import { assetLabel } from "../../lib/format.js";
import { ErrorBox, PageHeader, Panel, Table, Td, fmtDateTime, fmtInt, fmtMs, fmtPct, fmtTokens } from "../ui.jsx";

export default function PromptPage() {
  const [symbol, setSymbol] = useState("BTCUSDT");
  const instruments = useApi((signal) => getOverview({ type: "all", limit: 50, movers: 1 }, signal), []);
  const { data, error, loading, reload } = useApi((signal) => getAdminPrompt(symbol, signal), [symbol]);
  const preview = data?.preview;
  const active = data?.versions?.find((v) => v.version === data.active_version);

  return (
    <div className="space-y-6">
      <PageHeader title="Prompt" subtitle="Template prompt yang dikirim ke Hermes Agent, dirender dengan data terkini">
        {data && (
          <span className="rounded-full border border-line px-3 py-1 text-xs text-muted">
            Versi aktif <span className="font-semibold text-ink">{data.active_version}</span>
          </span>
        )}
      </PageHeader>

      <ErrorBox error={error} onRetry={reload} />

      <div className="rounded-xl border border-hold/40 bg-panel px-5 py-4 text-xs leading-relaxed">
        <p className="font-semibold text-hold">Prompt dikunci selama eksperimen</p>
        <p className="mt-1 text-muted">
          Mengubah prompt mengubah token dan isi ringkasan, sehingga data antar kondisi tidak sebanding lagi. Untuk mengubah:
          edit <code className="text-ink">build_prompt</code> di <code className="text-ink">ai-service/app.py</code>, naikkan{" "}
          <code className="text-ink">PROMPT_VERSION</code> di <code className="text-ink">run.env</code>, lalu restart. Versi lama
          tetap tercatat di riwayat di bawah dan cache-nya otomatis tidak terpakai (versi termasuk dalam cache key).
        </p>
      </div>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1.7fr)_minmax(0,1fr)]">
        <Panel
          title="Pratinjau prompt"
          aside={preview ? `${fmtInt(preview.chars)} karakter · tanpa memanggil model (0 token)` : null}
        >
          <div className="mb-4 flex flex-wrap gap-2" role="tablist" aria-label="Instrumen untuk pratinjau">
            {(instruments.data?.top_volume ?? []).map((r) => (
              <button
                key={r.symbol}
                role="tab"
                aria-selected={symbol === r.symbol}
                onClick={() => setSymbol(r.symbol)}
                title={assetLabel(r.asset_type)}
                className={`rounded-full border px-3 py-1 text-xs font-semibold transition-colors ${
                  symbol === r.symbol ? "border-hold text-ink" : "border-line text-muted hover:text-ink"
                }`}
              >
                {r.display_symbol}
              </button>
            ))}
          </div>
          {data?.preview_error && <p className="text-sm text-down">{data.preview_error}</p>}
          {preview && (
            <pre
              className={`max-h-96 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-canvas p-4 font-mono text-xs leading-relaxed ${
                loading ? "opacity-60" : ""
              }`}
            >
              {preview.prompt}
            </pre>
          )}
          {!preview && !data?.preview_error && <div className="h-48 animate-pulse rounded-lg bg-panel-2" />}
          {active?.tokens_in_avg != null && (
            <p className="mt-3 text-xs text-muted">
              Token input aktual rata-rata {fmtTokens(active.tokens_in_avg)} per panggilan: termasuk system prompt dan skema
              tool Hermes (~3.000 token tetap), bukan hanya teks di atas.
            </p>
          )}
        </Panel>

        <Panel title="Variabel yang diisi">
          <ul className="space-y-3 text-xs">
            {(preview?.variables ?? []).map((v) => (
              <li key={v.name}>
                <code className="rounded bg-panel-2 px-2 py-0.5 font-mono text-ink">{`{${v.name}}`}</code>
                <p className="mt-1 text-muted">{v.desc}</p>
              </li>
            ))}
          </ul>
          <p className="mt-4 text-xs text-muted">
            Output yang diminta: JSON dengan <code>summary</code> (maks. 80 kata), <code>signal</code> (BUY/SELL/HOLD),{" "}
            <code>support</code>, <code>resistance</code>. Support/resistance di luar 0,5–1,5× harga dibuang.
          </p>
        </Panel>
      </div>

      <Panel title="Riwayat versi prompt" aside="Dari ai_summaries; metrik token & durasi hanya untuk panggilan yang tercatat sejak migrasi 004">
        <Table
          head={[
            "Versi",
            "Dipakai",
            { label: "Ringkasan", right: true },
            "Model",
            { label: "Akurasi backtest", right: true },
            { label: "Token in / out", right: true },
            { label: "Durasi AI", right: true },
            { label: "JSON valid", right: true },
            { label: "Tanpa sinyal", right: true },
          ]}
          empty={data && data.versions.length === 0 ? "Belum ada ringkasan AI." : null}
        >
          {data?.versions.map((v) => (
            <tr key={v.version}>
              <Td className="font-semibold">
                {v.version}
                {v.version === data.active_version && (
                  <span className="ml-2 rounded-full bg-panel-2 px-2 py-0.5 text-[10px] font-semibold text-up">aktif</span>
                )}
              </Td>
              <Td className="text-muted">
                {fmtDateTime(v.first_used)} – {fmtDateTime(v.last_used)}
              </Td>
              <Td right>{fmtInt(v.summaries)}</Td>
              <Td className="text-muted">{v.models.join(", ")}</Td>
              <Td right>
                {v.backtested ? `${fmtPct(v.correct / v.backtested)} (${v.correct}/${v.backtested})` : "-"}
              </Td>
              <Td right>
                {v.tokens_in_avg != null ? `${fmtTokens(v.tokens_in_avg)} / ${fmtTokens(v.tokens_out_avg)}` : "-"}
              </Td>
              <Td right>{fmtMs(v.exec_ms_avg)}</Td>
              <Td right>{fmtPct(v.format_ok_rate)}</Td>
              <Td right>{fmtInt(v.no_signal)}</Td>
            </tr>
          ))}
        </Table>
      </Panel>
    </div>
  );
}

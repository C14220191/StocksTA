package main

import (
	"context"
	"sort"
	"strconv"
	"time"
)

// Biaya komputasi dengan AI berlangganan (mis. Codex lewat ChatGPT). Harga per token = 0, tetapi
// kuotanya terbatas per jendela waktu (Session 5 jam, Weekly), jadi biaya diukur sebagai konsumsi
// kuota (tabel quota_snapshots, ingestion/quota.py):
//
//	Q_w        = total persen kuota jendela w yang terpakai selama periode snapshot
//	r_w        = Q_w / T_total            persen kuota per token (T = token input + output semua
//	                                       analisis yang di-generate di periode yang sama)
//	Q_w(k)     = T(k) x r_w               kuota yang dipakai kondisi k
//	Kap_w(k)   = N(k) / Q_w(k) x 100      request terlayani per 100% kuota jendela w
//	C_sub(k)   = Q_weekly(k) / 100 x F x 12/52    amortisasi biaya langganan bulanan F
//
// Asumsi: kuota sebanding dengan token input + output (cache_read tidak dihitung), dan akun tidak
// dipakai untuk keperluan lain selama periode pengukuran (pemakaian lain ikut masuk Q_w).
// S = (N - N_gen) / N tidak bergantung pada asumsi ini.

type quotaPoint struct {
	At       time.Time
	Used     float64
	ResetsAt *time.Time
}

// quotaConsumed: total persen terpakai sepanjang deret snapshot (urut waktu). Kenaikan dijumlahkan;
// kalau jendela reset di antara dua snapshot (persen turun atau waktu reset bergeser), pemakaian
// setelah reset = nilai snapshot berikutnya. Pemakaian antara snapshot terakhir sebelum reset dan
// saat reset tidak terlihat, jadi snapshot harus rapat (10 menit).
func quotaConsumed(pts []quotaPoint) float64 {
	var sum float64
	for i := 1; i < len(pts); i++ {
		prev, cur := pts[i-1], pts[i]
		reset := cur.Used < prev.Used ||
			(prev.ResetsAt != nil && cur.ResetsAt != nil && cur.ResetsAt.Sub(*prev.ResetsAt) > time.Minute)
		if reset {
			sum += cur.Used
		} else {
			sum += cur.Used - prev.Used
		}
	}
	return sum
}

type quotaWindow struct {
	Label        string    `json:"label"`
	ConsumedPct  float64   `json:"consumed_pct"`
	Snapshots    int       `json:"snapshots"`
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	PctPer1MTok  *float64  `json:"pct_per_1m_tokens"` // persen kuota per 1 juta token
	pctPerToken  float64
}

type quotaInfo struct {
	Provider    string        `json:"provider"`
	Plan        *string       `json:"plan"`
	Windows     []quotaWindow `json:"windows"`
	TokensBasis int64         `json:"tokens_basis"` // token input + output semua analisis di periode snapshot
	FeeMonthly  *float64      `json:"fee_monthly"`
	Currency    string        `json:"currency"`
	Note        string        `json:"note,omitempty"`
}

func quotaProvider() string {
	return env("QUOTA_PROVIDER", env("HERMES_PROVIDER", "openai-codex"))
}

// quotaSummary: konsumsi kuota per jendela dalam `days` hari terakhir. nil kalau belum ada snapshot.
func (a *App) quotaSummary(ctx context.Context, days int) (*quotaInfo, error) {
	q := &quotaInfo{Provider: quotaProvider(), Currency: env("SUBSCRIPTION_CURRENCY", "USD")}
	if fee, err := strconv.ParseFloat(env("SUBSCRIPTION_FEE_MONTHLY", ""), 64); err == nil && fee > 0 {
		q.FeeMonthly = &fee
	}
	rows, err := a.store.pool.Query(ctx, `
		SELECT window_label, fetched_at, used_percent::float8, resets_at, plan
		FROM quota_snapshots
		WHERE provider = $1 AND used_percent IS NOT NULL AND fetched_at >= now() - make_interval(days => $2)
		ORDER BY window_label, fetched_at`, q.Provider, days)
	if err != nil {
		return nil, err
	}
	series := map[string][]quotaPoint{}
	for rows.Next() {
		var label string
		var p quotaPoint
		var plan *string
		if err := rows.Scan(&label, &p.At, &p.Used, &p.ResetsAt, &plan); err != nil {
			rows.Close()
			return nil, err
		}
		series[label] = append(series[label], p)
		if plan != nil {
			q.Plan = plan
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(series) == 0 {
		return nil, nil
	}

	var from, to time.Time
	for label, pts := range series {
		w := quotaWindow{Label: label, ConsumedPct: quotaConsumed(pts), Snapshots: len(pts),
			From: pts[0].At, To: pts[len(pts)-1].At}
		q.Windows = append(q.Windows, w)
		if from.IsZero() || w.From.Before(from) {
			from = w.From
		}
		if w.To.After(to) {
			to = w.To
		}
	}
	sort.Slice(q.Windows, func(i, j int) bool { return q.Windows[i].Label < q.Windows[j].Label })

	// Semua analisis yang benar-benar di-generate di periode snapshot, semua kondisi dan trigger
	// (kuota akun dipakai bersama oleh semua instance lab). Stub tidak memakai kuota.
	if err := a.store.pool.QueryRow(ctx, `
		SELECT coalesce(sum(tokens_in + tokens_out), 0) FROM inference_log
		WHERE created_at BETWEEN $1 AND $2 AND cache_status IN ('MISS', 'BYPASS', 'PREGEN')
		  AND coalesce(model, '') <> 'stub'`, from, to).Scan(&q.TokensBasis); err != nil {
		return nil, err
	}
	for i := range q.Windows {
		w := &q.Windows[i]
		if q.TokensBasis > 0 && w.ConsumedPct > 0 {
			w.pctPerToken = w.ConsumedPct / float64(q.TokensBasis)
			per := w.pctPerToken * 1e6
			w.PctPer1MTok = &per
		}
	}
	if q.TokensBasis == 0 {
		q.Note = "belum ada analisis AI (selain stub) di periode snapshot"
	}
	return q, nil
}

// applyQuota mengisi kolom kuota per kondisi (lihat rumus di atas).
func applyQuota(rows []*evalRow, q *quotaInfo) {
	if q == nil {
		return
	}
	for _, e := range rows {
		tokens := float64(e.TokensIn + e.TokensOut)
		served := float64(e.Requests - e.Errors)
		for _, w := range q.Windows {
			if w.pctPerToken <= 0 {
				continue
			}
			if e.QuotaPct == nil {
				e.QuotaPct, e.RequestsPerQuota = map[string]float64{}, map[string]float64{}
			}
			pct := tokens * w.pctPerToken
			e.QuotaPct[w.Label] = pct
			if pct > 0 && served > 0 {
				e.RequestsPerQuota[w.Label] = served / pct * 100
			}
			if w.Label == "Weekly" && q.FeeMonthly != nil {
				c := pct / 100 * *q.FeeMonthly * 12 / 52
				e.SubscriptionCost = &c
			}
		}
	}
}

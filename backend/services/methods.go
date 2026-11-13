package services

import "strings"

// Metode analisis AI. Setiap metode adalah Hermes skill (.hermes/skills/trading-analysis) dan
// menjadi satu "pekerjaan generate" tersendiri: entri cache, singleflight, invalidation, dan baris
// inference_log dipisah per (instrument, metode). Aturan invalidation SAMA untuk semua metode
// (drift harga dan S dari candle 1h), jadi perbedaan antar metode hanya pada biaya dan isi generate.
//
//	debate   Multi-Agent Debate (Bull vs Bear)             skill ta-bull-bear-debate
//	mtf      Structured Multi-Timeframe Technical Analysis  skill ta-multi-timeframe
//	grounded Grounded Research Brief + Devil's Advocate    skill ta-grounded-brief
var AnalysisMethods = []string{"debate", "mtf", "grounded"}

func ValidMethod(m string) bool {
	for _, x := range AnalysisMethods {
		if m == x {
			return true
		}
	}
	return false
}

var MethodsList = strings.Join(AnalysisMethods, ", ")

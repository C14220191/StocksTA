package main

import "math"

// Indikator teknikal untuk grafik di UI. Semua fungsi mengembalikan deret yang sejajar
// dengan input; posisi yang datanya belum cukup bernilai NaN (dikirim ke UI sebagai null).

// sma: simple moving average.
func sma(v []float64, p int) []float64 {
	out := nanSeries(len(v))
	if p <= 0 {
		return out
	}
	var sum float64
	for i, x := range v {
		sum += x
		if i >= p {
			sum -= v[i-p]
		}
		if i >= p-1 {
			out[i] = sum / float64(p)
		}
	}
	return out
}

// ema: exponential moving average. Mulai dari nilai pertama yang bukan NaN
// (supaya bisa dipakai untuk garis sinyal MACD), di-seed dengan SMA p nilai pertama.
func ema(v []float64, p int) []float64 {
	out := nanSeries(len(v))
	start := -1
	for i, x := range v {
		if !math.IsNaN(x) {
			start = i
			break
		}
	}
	if p <= 0 || start < 0 || start+p > len(v) {
		return out
	}
	var seed float64
	for _, x := range v[start : start+p] {
		seed += x
	}
	k := 2.0 / float64(p+1)
	prev := seed / float64(p)
	out[start+p-1] = prev
	for i := start + p; i < len(v); i++ {
		prev = v[i]*k + prev*(1-k)
		out[i] = prev
	}
	return out
}

// rsi: Relative Strength Index metode Wilder.
func rsi(v []float64, p int) []float64 {
	out := nanSeries(len(v))
	if p <= 0 || len(v) <= p {
		return out
	}
	var gain, loss float64
	for i := 1; i <= p; i++ {
		d := v[i] - v[i-1]
		if d > 0 {
			gain += d
		} else {
			loss -= d
		}
	}
	avgGain, avgLoss := gain/float64(p), loss/float64(p)
	out[p] = rsiValue(avgGain, avgLoss)
	for i := p + 1; i < len(v); i++ {
		d := v[i] - v[i-1]
		g, l := 0.0, 0.0
		if d > 0 {
			g = d
		} else {
			l = -d
		}
		avgGain = (avgGain*float64(p-1) + g) / float64(p)
		avgLoss = (avgLoss*float64(p-1) + l) / float64(p)
		out[i] = rsiValue(avgGain, avgLoss)
	}
	return out
}

func rsiValue(avgGain, avgLoss float64) float64 {
	if avgLoss == 0 {
		if avgGain == 0 {
			return 50
		}
		return 100
	}
	return 100 - 100/(1+avgGain/avgLoss)
}

// macd: garis MACD (EMA fast - EMA slow), garis sinyal, dan histogram.
func macd(v []float64, fast, slow, signal int) (line, sig, hist []float64) {
	ef, es := ema(v, fast), ema(v, slow)
	line = nanSeries(len(v))
	for i := range v {
		if !math.IsNaN(ef[i]) && !math.IsNaN(es[i]) {
			line[i] = ef[i] - es[i]
		}
	}
	sig = ema(line, signal)
	hist = nanSeries(len(v))
	for i := range v {
		if !math.IsNaN(line[i]) && !math.IsNaN(sig[i]) {
			hist[i] = line[i] - sig[i]
		}
	}
	return line, sig, hist
}

// bollinger: middle = SMA p, upper/lower = middle +- k x standar deviasi (populasi).
func bollinger(v []float64, p int, k float64) (upper, middle, lower []float64) {
	middle = sma(v, p)
	upper, lower = nanSeries(len(v)), nanSeries(len(v))
	for i := p - 1; i < len(v) && p > 0; i++ {
		if i < 0 {
			continue
		}
		var ss float64
		for _, x := range v[i-p+1 : i+1] {
			d := x - middle[i]
			ss += d * d
		}
		sd := math.Sqrt(ss / float64(p))
		upper[i] = middle[i] + k*sd
		lower[i] = middle[i] - k*sd
	}
	return upper, middle, lower
}

// stochastic: %K = (close - lowest low p) / (highest high p - lowest low p) x 100, %D = SMA d dari %K.
func stochastic(high, low, close []float64, p, d int) (k, dLine []float64) {
	k = nanSeries(len(close))
	for i := p - 1; i < len(close) && p > 0; i++ {
		hh, ll := high[i], low[i]
		for j := i - p + 1; j <= i; j++ {
			hh, ll = math.Max(hh, high[j]), math.Min(ll, low[j])
		}
		if hh > ll {
			k[i] = (close[i] - ll) / (hh - ll) * 100
		} else {
			k[i] = 50 // rentang nol: posisi di tengah
		}
	}
	dLine = nanSeries(len(close))
	for i := range close {
		if i >= p-1+d-1 {
			var s float64
			for j := i - d + 1; j <= i; j++ {
				s += k[j]
			}
			dLine[i] = s / float64(d)
		}
	}
	return k, dLine
}

// rsiWilderState: rata-rata gain/loss Wilder setelah nilai terakhir, supaya RSI bisa dihitung
// untuk satu close tambahan tanpa mengulang seluruh deret (dipakai vektor kondisi S).
func rsiWilderState(v []float64, p int) (avgGain, avgLoss float64, ok bool) {
	if p <= 0 || len(v) <= p {
		return 0, 0, false
	}
	for i := 1; i <= p; i++ {
		if d := v[i] - v[i-1]; d > 0 {
			avgGain += d
		} else {
			avgLoss -= d
		}
	}
	avgGain, avgLoss = avgGain/float64(p), avgLoss/float64(p)
	for i := p + 1; i < len(v); i++ {
		g, l := 0.0, 0.0
		if d := v[i] - v[i-1]; d > 0 {
			g = d
		} else {
			l = -d
		}
		avgGain = (avgGain*float64(p-1) + g) / float64(p)
		avgLoss = (avgLoss*float64(p-1) + l) / float64(p)
	}
	return avgGain, avgLoss, true
}

func nanSeries(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = math.NaN()
	}
	return out
}

// toJSON mengubah deret ke []*float64 (NaN -> null), sekaligus memotong ke n nilai terakhir.
func toJSON(v []float64, n int) []*float64 {
	if n > len(v) {
		n = len(v)
	}
	tail := v[len(v)-n:]
	out := make([]*float64, len(tail))
	for i, x := range tail {
		if !math.IsNaN(x) && !math.IsInf(x, 0) {
			val := x
			out[i] = &val
		}
	}
	return out
}

// lastValid: nilai terakhir yang bukan NaN, nil kalau tidak ada.
func lastValid(v []float64) *float64 {
	for i := len(v) - 1; i >= 0; i-- {
		if !math.IsNaN(v[i]) && !math.IsInf(v[i], 0) {
			x := v[i]
			return &x
		}
	}
	return nil
}

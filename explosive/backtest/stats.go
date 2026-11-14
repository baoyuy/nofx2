package backtest

import (
	"math"
	"sort"
)

// Feature is one input of the ranking model.
type Feature struct {
	Name string
	Get  func(Row) float64
}

// Features are the kline-only inputs the replay can reconstruct. Open
// interest and funding need separate history and are not here yet.
func Features() []Feature {
	return []Feature{
		{"day_gain", func(r Row) float64 { return r.DayGain }},
		{"elapsed_h", func(r Row) float64 { return r.ElapsedH }},
		{"vol_slope", func(r Row) float64 { return r.VolSlope }},
		{"high_dist", func(r Row) float64 { return r.HighDist }},
		{"change_1h", func(r Row) float64 { return r.Change1h }},
		{"change_4h", func(r Row) float64 { return r.Change4h }},
		{"atr_pct", func(r Row) float64 { return r.ATRPct }},
		{"taker_ratio", func(r Row) float64 { return r.TakerRatio }},
		{"efficiency", func(r Row) float64 { return r.Efficiency }},
		{"rel_strength", func(r Row) float64 { return r.RelStrength }},
		{"log_quote", func(r Row) float64 { return math.Log10(1 + r.QuoteVol) }},
	}
}

// Spearman is the rank correlation of a feature and the remaining return.
func Spearman(rows []Row, get func(Row) float64) float64 { return spearmanOn(rows, get) }

// spearmanOn computes ranks preserving pairing.
func spearmanOn(rows []Row, get func(Row) float64) float64 {
	n := len(rows)
	if n < 5 {
		return 0
	}
	type item struct {
		x, y float64
	}
	items := make([]item, n)
	for i, r := range rows {
		items[i] = item{get(r), r.Remaining}
	}
	rank := func(vals []float64) []float64 {
		idx := make([]int, len(vals))
		for i := range idx {
			idx[i] = i
		}
		sort.Slice(idx, func(a, b int) bool { return vals[idx[a]] < vals[idx[b]] })
		out := make([]float64, len(vals))
		for pos, i := range idx {
			out[i] = float64(pos)
		}
		return out
	}
	xs := make([]float64, n)
	ys := make([]float64, n)
	for i, it := range items {
		xs[i], ys[i] = it.x, it.y
	}
	rx, ry := rank(xs), rank(ys)
	var mx, my float64
	for i := range rx {
		mx += rx[i] / float64(n)
		my += ry[i] / float64(n)
	}
	var num, dx, dy float64
	for i := range rx {
		a, b := rx[i]-mx, ry[i]-my
		num += a * b
		dx += a * a
		dy += b * b
	}
	if dx == 0 || dy == 0 {
		return 0
	}
	return num / math.Sqrt(dx*dy)
}

// IC per day: the mean of each day's rank correlation, so one busy day cannot
// dominate the result.
func DailyIC(rows []Row, get func(Row) float64) float64 {
	byDay := map[int64][]Row{}
	for _, r := range rows {
		byDay[r.Day] = append(byDay[r.Day], r)
	}
	var sum, n float64
	for _, rs := range byDay {
		if len(rs) < 5 {
			continue
		}
		sum += spearmanOn(rs, get)
		n++
	}
	if n == 0 {
		return 0
	}
	return sum / n
}

// Band is one score bucket's realised outcome.
type Band struct {
	Name      string
	From      float64
	Samples   int
	Up        float64
	AvgRet    float64
	AvgDD     float64
	Sustained float64
}

// Bands groups rows by score into [edges[i], edges[i+1]) buckets; the last
// edge is +Inf.
func Bands(rows []Row, score func(Row) float64, names []string, edges []float64) []Band {
	out := make([]Band, len(names))
	for i := range names {
		out[i] = Band{Name: names[i], From: edges[i]}
	}
	for _, r := range rows {
		s := score(r)
		i := sort.SearchFloat64s(edges, s)
		if i >= len(names) {
			i = len(names) - 1
		}
		b := &out[i]
		b.Samples++
		if r.Remaining > 0 {
			b.Up++
		}
		if r.Remaining > 0 && r.MaxDD > -0.03 {
			b.Sustained++
		}
		b.AvgRet += r.Remaining
		b.AvgDD += r.MaxDD
	}
	for i := range out {
		b := &out[i]
		if b.Samples == 0 {
			continue
		}
		n := float64(b.Samples)
		b.Up /= n
		b.Sustained /= n
		b.AvgRet /= n
		b.AvgDD /= n
	}
	return out
}

// FitWeights weights each feature by its daily IC after z-scoring, which is a
// simple, explainable first model. Returns the weights and the score function.
func FitWeights(rows []Row, feats []Feature) map[string]float64 {
	weights := map[string]float64{}
	for _, f := range feats {
		weights[f.Name] = math.Max(-1, math.Min(1, DailyIC(rows, f.Get)))
	}
	return weights
}

// ZScore normalises a feature across rows.
func ZScore(rows []Row, get func(Row) float64) (mean, sd float64) {
	if len(rows) == 0 {
		return 0, 1
	}
	for _, r := range rows {
		mean += get(r)
	}
	mean /= float64(len(rows))
	for _, r := range rows {
		d := get(r) - mean
		sd += d * d
	}
	sd = math.Sqrt(sd / float64(len(rows)))
	if sd == 0 {
		sd = 1
	}
	return mean, sd
}

// Score builds a weighted z-score model.
func Score(rows []Row, feats []Feature, weights map[string]float64) func(Row) float64 {
	type norm struct {
		get  func(Row) float64
		mean float64
		sd   float64
		w    float64
	}
	normed := make([]norm, 0, len(feats))
	for _, f := range feats {
		w := weights[f.Name]
		if w == 0 {
			continue
		}
		m, s := ZScore(rows, f.Get)
		normed = append(normed, norm{get: f.Get, mean: m, sd: s, w: w})
	}
	return func(r Row) float64 {
		var out float64
		for _, n := range normed {
			out += n.w * (n.get(r) - n.mean) / n.sd
		}
		return out
	}
}

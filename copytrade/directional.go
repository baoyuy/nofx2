package copytrade

import "math"

// DirectionEvidence describes an observed historical frequency, never a model
// confidence or a promise about the next trade. Flat results count as misses.
type DirectionEvidence struct {
	Samples    int     `json:"samples"`
	Hits       int     `json:"hits"`
	HitRate    float64 `json:"hit_rate"`
	Lower95    float64 `json:"lower_95"`
	MeanReturn float64 `json:"mean_return"`
}

func MeasureDirection(returns []float64) DirectionEvidence {
	e := DirectionEvidence{}
	for _, r := range returns {
		if math.IsNaN(r) || math.IsInf(r, 0) {
			continue
		}
		e.Samples++
		e.MeanReturn += r
		if r > 0 {
			e.Hits++
		}
	}
	if e.Samples == 0 {
		return e
	}
	n := float64(e.Samples)
	e.HitRate = float64(e.Hits) / n
	e.MeanReturn /= n
	z := 1.96
	p := e.HitRate
	e.Lower95 = (p + z*z/(2*n) - z*math.Sqrt(p*(1-p)/n+z*z/(4*n*n))) / (1 + z*z/n)
	return e
}

// DirectionReady is deliberately conservative. These are provisional gates,
// not fitted probabilities: until independently verified, do not force ten.
func DirectionReady(e DirectionEvidence) bool {
	return e.Samples >= 12 && e.Lower95 > .5 && e.MeanReturn > .002
}

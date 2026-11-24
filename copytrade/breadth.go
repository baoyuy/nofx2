package copytrade

import "math"

// Market breadth: how much of the perpetual market moved up over a completed
// window. Used as a one-sided VETO, never as a reason to open.
//
// Measured on the user's own history (11-19..11-22, a falling market): a gate
// that picks the side from prior breadth did not improve cost-adjusted return
// and lost money on the day the market turned. A veto that only blocks picks
// taken AGAINST a lopsided market left the overall result unchanged while
// cutting the worst day's loss. So breadth may remove a candidate, and may
// never promote one. Thresholds are wide on purpose; with breadth between them
// nothing is blocked.
//
// This is a conditional filter, not a market forecast. Three days of one
// regime cannot establish that breadth predicts anything.

// BreadthVetoLow/High: a long is blocked only when breadth is below Low, a
// short only when breadth is above High.
const (
	BreadthVetoLow  = 0.30
	BreadthVetoHigh = 0.70
)

// Breadth summarises one completed lookback window over many symbols.
type Breadth struct {
	// Up is the share of measured symbols whose return over the window was
	// positive, 0..1.
	Up float64 `json:"up"`
	// Median is the median return over the window (fraction).
	Median float64 `json:"median"`
	// Samples is how many symbols were measured.
	Samples int `json:"samples"`
	// Valid reports that enough symbols were measured to use the reading.
	Valid bool `json:"valid"`
}

// BreadthMinSamples is the fewest symbols that make a breadth reading usable.
const BreadthMinSamples = 20

// MeasureBreadth builds a breadth reading from per-symbol window returns.
func MeasureBreadth(returns []float64) Breadth {
	clean := make([]float64, 0, len(returns))
	for _, r := range returns {
		if math.IsNaN(r) || math.IsInf(r, 0) {
			continue
		}
		clean = append(clean, r)
	}
	b := Breadth{Samples: len(clean)}
	if len(clean) == 0 {
		return b
	}
	up := 0
	for _, r := range clean {
		if r > 0 {
			up++
		}
	}
	b.Up = float64(up) / float64(len(clean))
	b.Median = median(clean)
	b.Valid = len(clean) >= BreadthMinSamples
	return b
}

func median(xs []float64) float64 {
	s := append([]float64(nil), xs...)
	// insertion sort: inputs here are a few hundred values at most
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// BreadthVetoes reports whether a candidate on `side` is taken against a
// lopsided market and must be dropped. An invalid reading vetoes nothing.
func (b Breadth) BreadthVetoes(side string) bool {
	if !b.Valid {
		return false
	}
	switch side {
	case "long":
		return b.Up < BreadthVetoLow
	case "short":
		return b.Up > BreadthVetoHigh
	}
	return false
}

// Label describes the reading for display and logs.
func (b Breadth) Label() string {
	if !b.Valid {
		return "unknown"
	}
	switch {
	case b.Up < BreadthVetoLow:
		return "broad-down"
	case b.Up > BreadthVetoHigh:
		return "broad-up"
	}
	return "mixed"
}

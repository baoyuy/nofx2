package copytrade

import (
	"math"
	"time"
)

// StableDirection combines current style-weighted trader readings with mature
// historical 4h outcomes. Percentages are calibrated with a neutral Beta prior.
type StableDirection struct {
	Direction      string  `json:"direction"`
	Score          float64 `json:"score"`
	Accuracy       float64 `json:"accuracy"`
	AIAccuracy     float64 `json:"ai_accuracy"`
	TraderAccuracy float64 `json:"trader_accuracy"`
	AI_Samples     int     `json:"ai_samples"`
	TraderSamples  int     `json:"trader_samples"`
	Traders        int     `json:"traders"`
	Risk           float64 `json:"risk"`
	Fresh          bool    `json:"fresh"`
	Eligible       bool    `json:"eligible"`
}

// StableScore uses modest shrinkage so a few lucky outcomes cannot dominate.
func StableScore(aiHits, aiN, traderHits, traderN int, direction string, risk float64, fresh bool) StableDirection {
	aiHits, aiN = clampCounts(aiHits, aiN)
	traderHits, traderN = clampCounts(traderHits, traderN)
	smooth := func(h, n int) float64 { return (float64(h) + 4) / (float64(n) + 8) }
	out := StableDirection{Direction: direction, AIAccuracy: smooth(aiHits, aiN), TraderAccuracy: smooth(traderHits, traderN), AI_Samples: aiN, TraderSamples: traderN, Risk: math.Max(0, math.Min(1, risk)), Fresh: fresh}
	weight := 0.0
	if aiN > 0 {
		weight += .55
	}
	if traderN > 0 {
		weight += .45
	}
	if weight > 0 {
		out.Accuracy = (.55*boolFloat(aiN > 0)*out.AIAccuracy + .45*boolFloat(traderN > 0)*out.TraderAccuracy) / weight
	}
	out.Score = 100 * out.Accuracy * (1 - .35*out.Risk)
	if !fresh {
		out.Score *= .5
	}
	// Each source must independently clear its minimum so one source cannot
	// hide the absence of the other in a fused ranking.
	out.Eligible = aiN >= 4 && traderN >= 4 && fresh && (direction == "long" || direction == "short")
	return out
}

func clampCounts(hits, n int) (int, int) {
	if n < 0 {
		n = 0
	}
	if hits < 0 {
		hits = 0
	}
	if hits > n {
		hits = n
	}
	return hits, n
}

func boolFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

func StableFresh(at int64, now time.Time) bool {
	return at > 0 && now.Sub(time.UnixMilli(at)) <= 30*time.Minute
}

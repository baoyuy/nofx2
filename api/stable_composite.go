package api

import (
	"nofx/copytrade"
)

// stableComposite is the 4h "stable" score shared by the page ranking and the
// program-stable trader, which opens real orders on it. Keeping a single
// definition is deliberate: the two used to be separate copies, which is how
// each picked up different bugs.
//
// What changed, and why:
//
//   - The crowd's lean (Rank, Confidence) only helps when it AGREES with the
//     AI's side, and counts against it when it opposes. Before, these scores
//     measured how strongly the crowd leaned to its OWN side and were added
//     regardless, so an AI short with a crowd strongly long gained ~20 points
//     and climbed the short ranking.
//   - The AI's own hit rate counts once. It used to enter twice: on its own,
//     and again inside the AI/trader blend.
//   - Freshness is a gate, not a bonus. The program only ever sees fresh
//     readings, so "+15 for fresh" was a constant 15 points of nothing.
//
// The result is an evidence ranking, not a probability. Its weights are
// chosen, not fitted; nothing here has demonstrated a trading edge yet, and the
// baselines panel is how that should be judged.
type stableInputs struct {
	Dir      string // the AI's side: long | short
	Accuracy float64
	AIRate   float64
	AISample int
	Rank     copytrade.Rank
	Conf     copytrade.Confidence
	Risk     float64
}

const (
	wAccuracy  = 0.45 // AI/trader blended, side-specific, deduplicated accuracy
	wCrowdRank = 0.30 // crowd lean, signed by agreement with the AI side
	wCrowdConf = 0.25 // crowd confidence, signed the same way
	// minAISamples below this the history cannot support the side.
	minAISamples = 4
)

// alignment is +1 when the crowd leans the AI's way, -1 when it leans against
// it, and 0 when it has no lean.
func alignment(aiDir, crowdDir string) float64 {
	switch {
	case crowdDir != "long" && crowdDir != "short":
		return 0
	case crowdDir == aiDir:
		return 1
	}
	return -1
}

func stableComposite(in stableInputs) float64 {
	accuracy := clampPct(in.Accuracy * 100)
	// A crowd leaning against the AI is evidence against the call. Centred on
	// 50 so "no information" adds nothing either way.
	rank := 50 + 0.5*alignment(in.Dir, in.Rank.Direction)*clampPct(in.Rank.Score)
	conf := 50 + 0.5*alignment(in.Dir, in.Conf.Direction)*clampPct(in.Conf.Score)
	comp := wAccuracy*accuracy + wCrowdRank*rank + wCrowdConf*conf
	// Penalties that are about evidence quality, not direction.
	comp -= 20 * in.Risk
	if in.AISample < minAISamples {
		comp -= 10
	}
	return clampPct(comp)
}

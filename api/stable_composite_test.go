package api

import (
	"testing"

	"nofx/copytrade"
)

func rankOf(dir string, score float64) copytrade.Rank {
	return copytrade.Rank{Direction: dir, Score: score}
}

func confOf(dir string, score float64) copytrade.Confidence {
	return copytrade.Confidence{Direction: dir, Score: score}
}

// A crowd leaning AGAINST the AI's side is evidence against the call. Before,
// the crowd scores measured how strongly the crowd leaned to its own side and
// were added whatever that side was, so an AI short with a strongly long crowd
// gained points and climbed the short ranking.
func TestStableCompositePenalisesACrowdLeaningTheOtherWay(t *testing.T) {
	base := stableInputs{Dir: "short", Accuracy: 0.6, AIRate: 0.6, AISample: 20}
	agree := base
	agree.Rank, agree.Conf = rankOf("short", 80), confOf("short", 80)
	oppose := base
	oppose.Rank, oppose.Conf = rankOf("long", 80), confOf("long", 80)
	silent := base
	silent.Rank, silent.Conf = rankOf("neutral", 80), confOf("neutral", 80)

	a, o, n := stableComposite(agree), stableComposite(oppose), stableComposite(silent)
	if !(o < n && n < a) {
		t.Fatalf("expected oppose < neutral < agree, got %.2f / %.2f / %.2f", o, n, a)
	}
	// A crowd with no lean must not move the score at all.
	noCrowd := base
	if got := stableComposite(noCrowd); got != n {
		t.Fatalf("an absent crowd must score like a neutral one: %.2f vs %.2f", got, n)
	}
}

// Accuracy is the dominant term, and it is not double counted.
func TestStableCompositeFollowsAccuracy(t *testing.T) {
	mk := func(acc float64) stableInputs {
		return stableInputs{Dir: "long", Accuracy: acc, AIRate: acc, AISample: 20,
			Rank: rankOf("long", 50), Conf: confOf("long", 50)}
	}
	low, high := stableComposite(mk(0.40)), stableComposite(mk(0.70))
	if high <= low {
		t.Fatalf("higher accuracy must score higher: %.2f vs %.2f", high, low)
	}
	// 30 points of accuracy at weight 0.45 moves the score ~13.5 points. If the
	// AI rate were still added separately it would move about twice as much.
	if d := high - low; d < 12 || d > 15 {
		t.Fatalf("accuracy should be weighted once (~13.5 points), moved %.2f", d)
	}
}

// Freshness is a gate applied before this point, so it must not appear as a
// bonus: the program only ever sees fresh readings, so a "+15 if fresh" term
// was a constant.
func TestStableCompositeHasNoFreshnessBonus(t *testing.T) {
	in := stableInputs{Dir: "long", Accuracy: 0.6, AIRate: 0.6, AISample: 20,
		Rank: rankOf("long", 60), Conf: confOf("long", 60)}
	// Nothing in the inputs can express freshness, which is the point: the
	// score is reproducible from evidence only.
	if got := stableComposite(in); got <= 0 || got > 100 {
		t.Fatalf("score out of range: %v", got)
	}
}

// Thin history and crowded losses are penalties about evidence quality.
func TestStableCompositePenalisesThinHistoryAndRisk(t *testing.T) {
	in := stableInputs{Dir: "long", Accuracy: 0.6, AIRate: 0.6, AISample: 20,
		Rank: rankOf("long", 60), Conf: confOf("long", 60)}
	thin := in
	thin.AISample = minAISamples - 1
	if stableComposite(thin) >= stableComposite(in) {
		t.Fatal("too few samples must cost points")
	}
	risky := in
	risky.Risk = 1
	if stableComposite(risky) >= stableComposite(in) {
		t.Fatal("trapped/liquidated holders must cost points")
	}
}

func TestAlignmentSigns(t *testing.T) {
	for _, tc := range []struct {
		ai, crowd string
		want      float64
	}{
		{"long", "long", 1}, {"short", "short", 1},
		{"long", "short", -1}, {"short", "long", -1},
		{"long", "neutral", 0}, {"short", "", 0},
	} {
		if got := alignment(tc.ai, tc.crowd); got != tc.want {
			t.Fatalf("alignment(%q,%q) = %v, want %v", tc.ai, tc.crowd, got, tc.want)
		}
	}
}

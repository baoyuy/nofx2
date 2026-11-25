package copytrade

import "testing"

func TestStableScoreShrinksSmallSamplesAndRequiresMaturity(t *testing.T) {
	small := StableScore(1, 1, 1, 1, "long", 0, true)
	if small.Eligible {
		t.Fatal("small sample must not enter main ranking")
	}
	large := StableScore(18, 20, 16, 20, "short", 0, true)
	if !large.Eligible || large.Direction != "short" || large.Score < 60 {
		t.Fatalf("unexpected stable score: %+v", large)
	}
	stale := StableScore(18, 20, 16, 20, "long", 0, false)
	if stale.Score >= large.Score {
		t.Fatal("stale signals must be discounted")
	}
	risky := StableScore(18, 20, 16, 20, "long", 1, true)
	if risky.Score >= large.Score {
		t.Fatal("risk must reduce the score")
	}
	missingTrader := StableScore(18, 20, 0, 0, "long", 0, true)
	if missingTrader.Eligible {
		t.Fatal("AI-only history must not qualify as a fused ranking")
	}
	if missingTrader.Accuracy <= .5 {
		t.Fatalf("available source accuracy should be reported without a neutral missing-source penalty: %+v", missingTrader)
	}
	invalid := StableScore(30, -1, -2, -3, "long", 0, true)
	if invalid.AI_Samples != 0 || invalid.TraderSamples != 0 || invalid.AIAccuracy != .5 || invalid.TraderAccuracy != .5 {
		t.Fatalf("invalid counts should be clamped: %+v", invalid)
	}
}

// The reason must distinguish a stale reading from thin history: the page used
// to say "insufficient samples" for both.
func TestStableScoreReportsWhyItIsNotEligible(t *testing.T) {
	for _, tc := range []struct {
		name     string
		aiN, trN int
		dir      string
		fresh    bool
		want     string
	}{
		{"eligible", 10, 10, "long", true, ""},
		{"stale with plenty of history", 200, 150, "long", false, "stale"},
		{"few AI samples", 2, 10, "short", true, "few_ai"},
		{"few trader samples", 10, 2, "short", true, "few_trader"},
		{"no direction", 10, 10, "neutral", true, "no_direction"},
	} {
		got := StableScore(tc.aiN/2, tc.aiN, tc.trN/2, tc.trN, tc.dir, 0, tc.fresh)
		if got.Reason != tc.want || got.Eligible != (tc.want == "") {
			t.Errorf("%s: reason=%q eligible=%v, want %q", tc.name, got.Reason, got.Eligible, tc.want)
		}
	}
}

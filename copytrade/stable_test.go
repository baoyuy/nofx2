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

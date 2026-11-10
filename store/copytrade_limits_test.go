package store

import "testing"

func TestCopyTradeConfidenceHasAFloor(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0, DefaultCopyTradeMinConfidence}, // unset
		{1, MinCopyTradeMinConfidence},     // one weak trader is not a signal
		{5, MinCopyTradeMinConfidence},
		{10, MinCopyTradeMinConfidence},
		{60, 60},
		{101, DefaultCopyTradeMinConfidence},
	}
	for _, c := range cases {
		src := CoinSourceConfig{SourceType: "copy_trade", CopyTradeMinConfidence: c.in}
		src.NormalizeCopyTrade()
		if src.CopyTradeMinConfidence != c.want {
			t.Errorf("min confidence %v -> %v, want %v", c.in, src.CopyTradeMinConfidence, c.want)
		}
	}
	// The context threshold has the same floor, clamped rather than reset.
	cfg := GetDefaultStrategyConfig("zh")
	cfg.CoinSource = CoinSourceConfig{SourceType: "static", CopyTradeContext: true, CopyTradeContextMinConfidence: 3}
	cfg.ClampLimits()
	if got := cfg.CoinSource.CopyTradeContextMinConfidence; got != MinCopyTradeMinConfidence {
		t.Errorf("context confidence 3 -> %v, want %v", got, MinCopyTradeMinConfidence)
	}
	cfg.CoinSource.CopyTradeContextMinConfidence = 500
	cfg.ClampLimits()
	if got := cfg.CoinSource.CopyTradeContextMinConfidence; got != 100 {
		t.Errorf("context confidence 500 -> %v, want 100", got)
	}
}

// A copy-trade strategy with no limit saved trades the default number of
// coins; the token estimate must not read that as zero.
func TestEffectiveCoinCountNormalizesCopyTradeLimit(t *testing.T) {
	cfg := GetDefaultStrategyConfig("zh")
	cfg.CoinSource = CoinSourceConfig{SourceType: "copy_trade"}
	if got, want := cfg.getEffectiveCoinCount(), DefaultCopyTradeLimit; got != want {
		t.Errorf("limit 0 -> %d coins estimated, want %d", got, want)
	}
	cfg.CoinSource.CopyTradeLimit = 8
	if got := cfg.getEffectiveCoinCount(); got != 8 {
		t.Errorf("limit 8 -> %d coins estimated", got)
	}
}

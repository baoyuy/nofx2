package store

import "testing"

func TestNormalizeKeepsExplosiveAndHybridSources(t *testing.T) {
	for _, src := range []string{"explosive", "hybrid"} {
		c := &StrategyConfig{CoinSource: CoinSourceConfig{SourceType: src}}
		c.NormalizeProductSchema()
		if c.CoinSource.SourceType != src {
			t.Fatalf("source %q was normalized to %q", src, c.CoinSource.SourceType)
		}
	}
	// hybrid defaults to including all three parts
	c := &StrategyConfig{CoinSource: CoinSourceConfig{SourceType: "hybrid"}}
	c.NormalizeProductSchema()
	if !c.CoinSource.HybridIncludeExplosive || !c.CoinSource.HybridIncludeHorn || !c.CoinSource.HybridIncludeCopyTrade {
		t.Fatalf("hybrid should default to all three: %+v", c.CoinSource)
	}
}

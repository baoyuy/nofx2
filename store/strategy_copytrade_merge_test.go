package store

import "testing"

// A save turning copy-trade gates off must not keep the stored values.
func TestMergeTurnsCopyTradeGatesOff(t *testing.T) {
	var base StrategyConfig
	base.CoinSource.CopyTradeMinOpeners = 2
	base.CoinSource.CopyTradeMinHitRate = 55
	base.CoinSource.CopyTradeMinVolumeM = 20
	base.CoinSource.CopyTradeTrendFilter = true
	merged, err := MergeStrategyConfig(base, map[string]any{"ai_config": map[string]any{"coin_source": map[string]any{
		"copy_trade_min_openers": 0, "copy_trade_min_hit_rate": 0,
		"copy_trade_min_volume_millions": 0, "copy_trade_trend_filter": false,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	cs := merged.CoinSource
	if cs.CopyTradeMinOpeners != 0 || cs.CopyTradeMinHitRate != 0 || cs.CopyTradeMinVolumeM != 0 || cs.CopyTradeTrendFilter {
		t.Fatalf("gates kept: %+v", cs)
	}
}

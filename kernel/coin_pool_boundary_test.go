package kernel

import (
	"errors"
	"nofx/store"
	"testing"
	"time"
)

func TestPoolBoundaryRefreshAndDirection(t *testing.T) {
	old, detail := CoinPoolResolver, CoinPoolDetailResolver
	defer func() { CoinPoolResolver = old; CoinPoolDetailResolver = detail }()
	src := store.CoinSourceConfig{SourceType: "coin_pool", CoinPoolID: "p", StaticCoins: []string{"BTCUSDT"}}
	CoinPoolResolver = func(string) ([]string, error) { return nil, errors.New("offline") }
	if len(coinPoolCandidates(src)) != 0 {
		t.Fatal("unavailable pool used stale snapshot")
	}
	CoinPoolResolver = func(string) ([]string, error) { return []string{"BTCUSDT"}, nil }
	CoinPoolDetailResolver = func(string) ([]store.CoinPoolCoin, error) {
		return []store.CoinPoolCoin{{Symbol: "BTCUSDT", Direction: "long", ExpiryAt: time.Now().Add(-time.Minute).UnixMilli()}}, nil
	}
	if len(coinPoolCandidates(src)) != 0 {
		t.Fatal("expired signal admitted")
	}
	CoinPoolDetailResolver = func(string) ([]store.CoinPoolCoin, error) {
		return []store.CoinPoolCoin{{Symbol: "BTCUSDT", Direction: "long", ExpiryAt: time.Now().Add(time.Minute).UnixMilli()}}, nil
	}
	coins := coinPoolCandidates(src)
	e := NewStrategyEngine(&store.StrategyConfig{CoinSource: src}, "")
	ctx := &Context{CandidateCoins: coins}
	if reason := e.OpenBlockReason(Decision{Symbol: "BTCUSDT", Action: "open_long"}, ctx); reason != "" {
		t.Fatal(reason)
	}
	if e.OpenBlockReason(Decision{Symbol: "BTCUSDT", Action: "open_short"}, ctx) == "" {
		t.Fatal("opposite direction admitted")
	}
	e.config.CoinSource.PoolDirectionMode = "free"
	if reason := e.OpenBlockReason(Decision{Symbol: "BTCUSDT", Action: "open_short"}, ctx); reason != "" {
		t.Fatal(reason)
	}
	if e.OpenBlockReason(Decision{Symbol: "ETHUSDT", Action: "open_long"}, ctx) == "" {
		t.Fatal("nonmember admitted in free mode")
	}
	e.config.CoinSource.AllowedSymbols = []string{"ETHUSDT"}
	if e.OpenBlockReason(Decision{Symbol: "BTCUSDT", Action: "open_long"}, ctx) == "" {
		t.Fatal("allowlist ignored")
	}
}

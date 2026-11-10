package kernel

import (
	"context"
	"errors"
	"testing"

	"nofx/store"
)

func TestHornCandidatesRescreenAndFallback(t *testing.T) {
	previous := HornResolver
	t.Cleanup(func() { HornResolver = previous })

	var gotInterval string
	var gotLimit int
	result := []string{"AUSDT", "BUSDT"}
	var fail error
	HornResolver = func(_ context.Context, interval string, limit int) ([]string, error) {
		gotInterval, gotLimit = interval, limit
		return result, fail
	}
	symbols := func(c []CandidateCoin) []string {
		out := make([]string, len(c))
		for i, x := range c {
			out[i] = x.Symbol
		}
		return out
	}

	got := hornCandidates(store.CoinSourceConfig{SourceType: "bollinger_horn", HornInterval: "4h"})
	if gotInterval != "4h" || gotLimit != store.HornCoinLimit || len(got) != 2 || got[0].Sources[0] != "bollinger_horn" {
		t.Fatalf("interval=%q limit=%d got=%v", gotInterval, gotLimit, symbols(got))
	}

	// An unknown interval falls back to the default.
	hornCandidates(store.CoinSourceConfig{HornInterval: "2h"})
	if gotInterval != store.DefaultHornInterval {
		t.Fatalf("interval = %q, want default %q", gotInterval, store.DefaultHornInterval)
	}

	// A failed screen reuses the last successful 4h screen.
	fail = errors.New("binance down")
	result = nil
	if got := symbols(hornCandidates(store.CoinSourceConfig{HornInterval: "4h"})); len(got) != 2 || got[0] != "AUSDT" {
		t.Fatalf("fallback = %v, want last screen", got)
	}

	// An empty successful screen is a real result, not a failure.
	fail = nil
	result = []string{}
	if got := hornCandidates(store.CoinSourceConfig{HornInterval: "4h"}); len(got) != 0 {
		t.Fatalf("empty screen = %v, want none", symbols(got))
	}
}

func TestStrategyNormalizesHornSource(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("zh")
	cfg.CoinSource = store.CoinSourceConfig{SourceType: "bollinger_horn", HornInterval: "bad", CoinPoolID: "x"}
	cfg.NormalizeProductSchema()
	if cfg.CoinSource.SourceType != "bollinger_horn" || cfg.CoinSource.HornInterval != store.DefaultHornInterval || cfg.CoinSource.CoinPoolID != "" {
		t.Fatalf("normalized source = %+v", cfg.CoinSource)
	}
}

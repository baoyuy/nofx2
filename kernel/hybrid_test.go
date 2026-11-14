package kernel

import (
	"context"
	"errors"
	"testing"

	"nofx/store"
)

func TestExplosiveCandidatesTagAndFallback(t *testing.T) {
	saved := ExplosiveResolver
	defer func() { ExplosiveResolver = saved }()
	ExplosiveResolver = func(ctx context.Context, limit int) ([]string, error) {
		return []string{"AAAUSDT", "BBBUSDT"}, nil
	}
	got := explosiveCandidates(store.CoinSourceConfig{SourceType: "explosive"})
	if len(got) != 2 || got[0].Symbol != "AAAUSDT" || got[0].Sources[0] != "explosive" {
		t.Fatalf("explosive candidates: %+v", got)
	}
	// A failed read must reuse the last good board instead of emptying the list.
	ExplosiveResolver = func(ctx context.Context, limit int) ([]string, error) {
		return nil, errors.New("binance down")
	}
	got = explosiveCandidates(store.CoinSourceConfig{SourceType: "explosive"})
	if len(got) != 2 {
		t.Fatalf("fallback should keep last board, got %+v", got)
	}
}

func TestHybridUnionsAndRanksAgreementFirst(t *testing.T) {
	savedE, savedH, savedC := ExplosiveResolver, HornResolver, CopyTradeResolver
	defer func() { ExplosiveResolver, HornResolver, CopyTradeResolver = savedE, savedH, savedC }()

	ExplosiveResolver = func(ctx context.Context, limit int) ([]string, error) {
		return []string{"AAAUSDT", "BBBUSDT"}, nil
	}
	HornResolver = func(ctx context.Context, interval string, limit int) ([]string, error) {
		return []string{"BBBUSDT", "CCCUSDT"}, nil
	}
	CopyTradeResolver = nil

	// All-false flags must default to including every part.
	got := hybridCandidates(store.CoinSourceConfig{SourceType: "hybrid", HornInterval: "1h"})
	if len(got) != 3 {
		t.Fatalf("hybrid should union to 3 coins, got %+v", got)
	}
	if got[0].Symbol != "BBBUSDT" || len(got[0].Sources) != 2 {
		t.Fatalf("the coin on two sources must rank first with both tags: %+v", got[0])
	}
	// Disabling explosive leaves horn only.
	got = hybridCandidates(store.CoinSourceConfig{SourceType: "hybrid", HornInterval: "1h", HybridIncludeHorn: true})
	if len(got) != 2 {
		t.Fatalf("horn-only hybrid should be 2, got %+v", got)
	}
	for _, c := range got {
		if c.Sources[0] != "bollinger_horn" {
			t.Fatalf("expected horn tag, got %+v", c.Sources)
		}
	}
}

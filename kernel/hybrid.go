package kernel

import (
	"context"
	"sort"
	"sync"
	"time"

	"nofx/logger"
	"nofx/store"
)

// ExplosiveResolver returns the symbols currently on the 爆发榜 (explosive
// board). It is set at startup (explosive.Shared) so the kernel stays free of
// a direct dependency on the screener.
var ExplosiveResolver func(ctx context.Context, limit int) ([]string, error)

var (
	explosiveLastMu sync.Mutex
	explosiveLast   []string // last successful board, reused on a failed read
)

// explosiveCandidates reads the live explosive board. A failed read reuses the
// last successful board so a Binance hiccup does not empty the universe.
func explosiveCandidates(source store.CoinSourceConfig) []CandidateCoin {
	if ExplosiveResolver == nil {
		logger.Warnf("⚠️  Explosive board resolver not configured")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	symbols, err := ExplosiveResolver(ctx, store.MaxCandidateCoins)
	explosiveLastMu.Lock()
	defer explosiveLastMu.Unlock()
	if err != nil {
		logger.Warnf("⚠️  Explosive board read failed (%v), reusing the last board (%d coins)", err, len(explosiveLast))
		return toCandidates(explosiveLast, "explosive")
	}
	explosiveLast = symbols
	logger.Infof("💥 Explosive board source: %d coins %v", len(symbols), symbols)
	return toCandidates(symbols, "explosive")
}

// hybridCandidates unions the explosive board, the Bollinger horn and the
// copy-trade coins into one universe. Coins that come from more than one
// source rank first, so the limited candidate slots favour agreement.
func hybridCandidates(source store.CoinSourceConfig) []CandidateCoin {
	incExp := source.HybridIncludeExplosive
	incHorn := source.HybridIncludeHorn
	incCopy := source.HybridIncludeCopyTrade
	if !incExp && !incHorn && !incCopy {
		incExp, incHorn, incCopy = true, true, true
	}

	order := make([]string, 0, store.MaxCandidateCoins*3)
	sourcesOf := make(map[string][]string)
	seenSrc := make(map[string]map[string]bool)
	add := func(cands []CandidateCoin) {
		for _, c := range cands {
			if _, ok := sourcesOf[c.Symbol]; !ok {
				sourcesOf[c.Symbol] = nil
				seenSrc[c.Symbol] = map[string]bool{}
				order = append(order, c.Symbol)
			}
			for _, src := range c.Sources {
				if !seenSrc[c.Symbol][src] {
					seenSrc[c.Symbol][src] = true
					sourcesOf[c.Symbol] = append(sourcesOf[c.Symbol], src)
				}
			}
		}
	}
	if incExp {
		add(explosiveCandidates(source))
	}
	if incHorn {
		add(hornCandidates(source))
	}
	if incCopy {
		add(copyTradeCandidates(source))
	}

	// Coins agreed on by more sources first; ties keep insertion order
	// (explosive, then horn, then copy-trade).
	sort.SliceStable(order, func(a, b int) bool {
		return len(sourcesOf[order[a]]) > len(sourcesOf[order[b]])
	})
	if len(order) > store.MaxCandidateCoins {
		order = order[:store.MaxCandidateCoins]
	}
	out := make([]CandidateCoin, 0, len(order))
	for _, sym := range order {
		out = append(out, CandidateCoin{Symbol: sym, Sources: sourcesOf[sym]})
	}
	logger.Infof("🧩 Hybrid source: %d coins %v", len(out), order)
	return out
}

package kernel

import (
	"context"
	"sync"
	"time"

	"nofx/logger"
	"nofx/market"
	"nofx/store"
)

// CoinPoolResolver returns the current symbols of a saved coin pool. It is
// set at startup; the engine has no database access of its own.
var CoinPoolResolver func(poolID string) ([]string, error)

// coinPoolCandidates reads the pool fresh each cycle, so edits on the coin
// pool page reach running strategies. A missing pool falls back to the
// snapshot saved with the strategy.
func coinPoolCandidates(source store.CoinSourceConfig) []CandidateCoin {
	symbols := source.StaticCoins
	origin := "coin_pool_snapshot"
	if CoinPoolResolver != nil && source.CoinPoolID != "" {
		if live, err := CoinPoolResolver(source.CoinPoolID); err == nil && len(live) > 0 {
			symbols, origin = live, "coin_pool"
		} else {
			logger.Warnf("⚠️  Coin pool %s unavailable (%v), using the saved snapshot", source.CoinPoolID, err)
		}
	}
	return toCandidates(symbols, origin)
}

func toCandidates(symbols []string, origin string) []CandidateCoin {
	if len(symbols) > store.MaxCandidateCoins {
		symbols = symbols[:store.MaxCandidateCoins]
	}
	out := make([]CandidateCoin, 0, len(symbols))
	for _, symbol := range symbols {
		out = append(out, CandidateCoin{Symbol: market.Normalize(symbol), Sources: []string{origin}})
	}
	return out
}

// HornResolver returns up to limit symbols currently matching the Bollinger
// horn on interval. It is set at startup (coinpool.Shared.HornSymbols).
var HornResolver func(ctx context.Context, interval string, limit int) ([]string, error)

var (
	hornLastMu sync.Mutex
	hornLast   = map[string][]string{} // last successful screen per interval
)

// hornCandidates re-screens the Binance top gainers every cycle. An empty
// screen is a real result (no horn right now); a failed screen reuses the
// last successful one so a Binance hiccup does not empty the universe.
// Open positions are managed regardless: their market data is always fetched.
func hornCandidates(source store.CoinSourceConfig) []CandidateCoin {
	interval := source.HornInterval
	if !store.HornIntervals[interval] {
		interval = store.DefaultHornInterval
	}
	if HornResolver == nil {
		logger.Warnf("⚠️  Bollinger horn screener not configured")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	symbols, err := HornResolver(ctx, interval, store.HornCoinLimit)
	hornLastMu.Lock()
	defer hornLastMu.Unlock()
	if err != nil {
		last := hornLast[interval]
		logger.Warnf("⚠️  Bollinger horn screen (%s) failed (%v), reusing the last screen (%d coins)", interval, err, len(last))
		return toCandidates(last, "bollinger_horn")
	}
	hornLast[interval] = symbols
	logger.Infof("📐 Bollinger horn screen (%s): %d matching coins %v", interval, len(symbols), symbols)
	return toCandidates(symbols, "bollinger_horn")
}

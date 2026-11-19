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

// CoinPoolDetailResolver returns the pool coins with their indicated direction.
var CoinPoolDetailResolver func(poolID string) ([]store.CoinPoolCoin, error)

// coinPoolCandidates reads the pool fresh each cycle, so edits on the coin
// pool page reach running strategies. A missing pool falls back to the
// snapshot saved with the strategy.
func coinPoolCandidates(source store.CoinSourceConfig) []CandidateCoin {
	if CoinPoolResolver != nil && source.CoinPoolID != "" {
		if live, err := CoinPoolResolver(source.CoinPoolID); err == nil && len(live) > 0 {
			dirs := map[string]string{}
			if CoinPoolDetailResolver != nil {
				if coins, derr := CoinPoolDetailResolver(source.CoinPoolID); derr == nil {
					now := time.Now().UnixMilli()
					fresh := make([]string, 0, len(coins))
					for _, c := range coins {
						if c.ExpiryAt > 0 && now > c.ExpiryAt {
							continue // stale AI pick: never trade it
						}
						dirs[market.Normalize(c.Symbol)] = c.Direction
						fresh = append(fresh, c.Symbol)
					}
					if len(fresh) > 0 {
						return toCandidatesDir(fresh, "coin_pool", dirs)
					}
				}
			}
			return toCandidatesDir(live, "coin_pool", dirs)
		} else {
			logger.Warnf("⚠️  Coin pool %s unavailable (%v), using the saved snapshot", source.CoinPoolID, err)
		}
	}
	return toCandidates(source.StaticCoins, "coin_pool_snapshot")
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

func toCandidatesDir(symbols []string, origin string, dirs map[string]string) []CandidateCoin {
	if len(symbols) > store.MaxCandidateCoins {
		symbols = symbols[:store.MaxCandidateCoins]
	}
	out := make([]CandidateCoin, 0, len(symbols))
	for _, symbol := range symbols {
		sym := market.Normalize(symbol)
		out = append(out, CandidateCoin{Symbol: sym, Sources: []string{origin}, Direction: dirs[sym]})
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

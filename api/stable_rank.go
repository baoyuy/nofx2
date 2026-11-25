package api

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"

	"nofx/copytrade"
	"nofx/kernel"
	"nofx/trader/binance"
)

// stableRanksForProgram builds the calibrated 4h "stable" ranking used by the
// program-stable (no-AI) strategy: only volume-in-scope coins with a fresh AI
// score and a clear long/short direction, scored by the same composite the
// 4h-stable ranking uses.
func jsonUnmarshalPayload(payload string, v any) error { return json.Unmarshal([]byte(payload), v) }

func (s *Server) stableRanksForProgram(userID string) []kernel.StableRank {
	if s.copyTrade == nil {
		return nil
	}
	uid := userID
	if uid == "" {
		return nil
	}
	cfg := s.loadCoinScoreAuto()
	minVol := cfg.minVolume()
	// One shared definition of "this reading is still current" (see
	// scoreFreshMs): the program, pool, rankings and status pages must agree,
	// or a coin is tradeable by one and expired by another.
	ttlMs := cfg.scoreFreshMs()
	freshWindow := time.Duration(ttlMs) * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	now := time.Now()
	nowMs := now.UnixMilli()
	sums := copytrade.Symbols(s.copyTrade.Snapshots())
	vols, _ := binance.PublicQuoteVolumes(ctx)
	if len(vols) > 0 {
		copytrade.AttachRanks(sums, vols)
	}
	rows, _ := s.store.CopyTrade().ListCoinScores(uid)
	type ai struct {
		at    int64
		bias  string
		score float64
	}
	am := map[string]ai{}
	for _, r := range rows {
		var res coinScoreResult
		if jsonUnmarshalPayload(r.Payload, &res) != nil || res.Analysis == nil {
			continue
		}
		if _, ok := am[r.Symbol]; ok {
			continue
		}
		am[r.Symbol] = ai{r.AnalyzedAt, res.Analysis.Bias, res.Analysis.Score}
	}
	out := make([]kernel.StableRank, 0, len(sums))
	for i := range sums {
		sum := &sums[i]
		if len(vols) > 0 {
			if _, ok := vols[sum.Symbol]; !ok {
				continue // keep only Binance USDT-M perpetuals
			}
		}
		if minVol > 0 && (sum.Volume24h <= 0 || sum.Volume24h < minVol) {
			continue
		}
		a, ok := am[sum.Symbol]
		if !ok || nowMs-a.at > ttlMs {
			continue
		}
		dir := a.bias
		if dir != "long" && dir != "short" {
			continue
		}
		// stable components (same as the 4h-stable ranking)
		aiRows, _ := s.store.CopyTrade().RecentMatureScoreOutcomes(uid, sum.Symbol, now.Add(-30*24*time.Hour).UnixMilli())
		tradeRows, _ := s.store.CopyTrade().RecentMatureSignals(sum.Symbol, now.Add(-30*24*time.Hour).Unix())
		// Shared with the coin list and detail, so every page counts the
		// same samples the same way: one call per run, only the side
		// being shown, and no BTC self-benchmark.
		ah, an, th, tn := stableCountsFor(aiRows, tradeRows, dir)
		risk := float64(sum.Liquidated+sum.Long.Trapped+sum.Short.Trapped) / float64(max(1, sum.Long.Traders+sum.Short.Traders))
		fresh := copytrade.StableFresh(a.at, now, freshWindow)
		sd := copytrade.StableScore(ah, an, th, tn, dir, risk, fresh)
		// The one shared composite (see stableComposite): the crowd only
		// helps when it agrees with the AI side, the AI rate counts once,
		// and freshness is the gate above rather than a bonus.
		comp := stableComposite(stableInputs{Dir: dir, Accuracy: sd.Accuracy, AIRate: sd.AIAccuracy,
			AISample: sd.AI_Samples, Rank: sum.Rank, Conf: sum.Confidence, Risk: sd.Risk})
		out = append(out, kernel.StableRank{Symbol: sum.Symbol, Direction: dir, Stable: comp, AI: clampPct(a.score)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Stable > out[j].Stable })
	if len(out) > 40 {
		out = out[:40]
	}
	for i := range out {
		out[i].Price = progPrice(ctx, out[i].Symbol)
	}
	return out
}

// progPrice caches public tickers briefly so several traders / coins in the
// same cycle do not hammer the exchange.
var (
	progTickerMu    sync.Mutex
	progTickerCache = map[string]struct {
		p float64
		t int64
	}{}
)

func progPrice(ctx context.Context, sym string) float64 {
	now := time.Now().UnixMilli()
	progTickerMu.Lock()
	c, ok := progTickerCache[sym]
	progTickerMu.Unlock()
	if ok && now-c.t < 5000 {
		return c.p
	}
	tk, err := binance.GetPublicTicker(ctx, sym)
	if err != nil || tk == nil {
		return 0
	}
	progTickerMu.Lock()
	progTickerCache[sym] = struct {
		p float64
		t int64
	}{tk.LastPrice, now}
	progTickerMu.Unlock()
	return tk.LastPrice
}

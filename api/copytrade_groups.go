package api

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"nofx/copytrade"
	"nofx/store"
)

// Measured-history layer for the copy-trade coin pool.
//
// The pool used to be ranked on the model's self-reported conviction, which the
// scoring prompt itself calls an evidence ranking rather than a calibrated win
// probability. Here, past readings are grouped by (side, conviction band) and
// each group is summarised by the cost-adjusted forward return it actually
// produced, so the pool is ordered by measured history instead.
//
// Both the group statistics and the breadth reading come from rows this app
// already stores and evaluates (copytrade_score_outcomes), so neither adds an
// exchange request.

const (
	// groupStatsTTL is how long a measured-history snapshot is reused. The
	// underlying outcomes only change when the evaluation loop runs (every 10
	// minutes), so a short cache costs nothing in freshness.
	groupStatsTTL = 5 * time.Minute
	// breadthWindowMs is the length of the completed window a breadth reading
	// describes.
	breadthWindowMs = int64(4 * 60 * 60 * 1000)
	// breadthMaxAgeMs is how far back a usable breadth window may start. Beyond
	// this the reading is treated as unknown and vetoes nothing.
	breadthMaxAgeMs = int64(8 * 60 * 60 * 1000)
)

type groupSnapshot struct {
	stats   map[copytrade.GroupKey]copytrade.GroupStat
	breadth copytrade.Breadth
	// Samples is how many mature, de-duplicated readings the stats rest on.
	Samples int
	takenAt time.Time
}

var (
	groupCacheMu sync.Mutex
	groupCache   = map[string]groupSnapshot{}
)

// groupHistory returns the measured per-group history and the current breadth
// reading for one user, recomputing at most once per groupStatsTTL.
// A read failure is returned, never turned into an empty snapshot: an empty
// one reads as "every group has too little history", which ranks every
// candidate at the provisional default, vetoes nothing, and labels it all
// "样本不足" — a silent fallback to conviction ranking presented as data.
func (s *Server) groupHistory(userID string) (groupSnapshot, error) {
	if userID == "" {
		return groupSnapshot{}, errors.New("no user for group history")
	}
	groupCacheMu.Lock()
	if snap, ok := groupCache[userID]; ok && time.Since(snap.takenAt) < groupStatsTTL {
		groupCacheMu.Unlock()
		return snap, nil
	}
	groupCacheMu.Unlock()

	rows, _, err := s.store.CopyTrade().ListScoreOutcomesSince(userID, time.Now().UnixMilli()-outcomeStatsWindowMs)
	if err != nil {
		return groupSnapshot{}, fmt.Errorf("read score history: %w", err)
	}
	snap := buildGroupSnapshot(currentVersionOnly(rows), time.Now().UnixMilli())
	snap.takenAt = time.Now()
	groupCacheMu.Lock()
	groupCache[userID] = snap
	groupCacheMu.Unlock()
	return snap, nil
}

// currentVersionOnly keeps the readings produced by the scoring version in use.
// A group's history is evidence about how THIS prompt's readings perform;
// readings from an older prompt measure a different thing. Rows recorded
// before versions were stored are kept only while no versioned history exists,
// so the pool is not left with nothing during the switch-over.
func currentVersionOnly(rows []store.CopyTradeScoreOutcome) []store.CopyTradeScoreOutcome {
	current := make([]store.CopyTradeScoreOutcome, 0, len(rows))
	for _, o := range rows {
		if o.ConfigVersion == coinScoreConfigVersion {
			current = append(current, o)
		}
	}
	if len(current) == 0 {
		return rows
	}
	return current
}

// buildGroupSnapshot is the pure part: de-duplicate the stored outcomes the
// same way the statistics page does, measure every group, and read breadth off
// the most recent completed 4h window.
func buildGroupSnapshot(rows []store.CopyTradeScoreOutcome, nowMs int64) groupSnapshot {
	ded := dedupOutcomes(rows)
	bench := buildBench(ded)
	obs := make([]copytrade.GroupObservation, 0, len(ded))
	for _, o := range ded {
		if o.Ret4h == nil || (o.Bias != "long" && o.Bias != "short") {
			continue
		}
		ob := copytrade.GroupObservation{Symbol: o.Symbol, AnalyzedAt: o.AnalyzedAt, Side: o.Bias,
			Conviction: absf(o.Bull - o.Bear), Return: *o.Ret4h}
		// The other coins in its cohort, never the reading itself.
		ob.MarketReturn, ob.HasMarket = benchFor(bench, o, "4h")
		obs = append(obs, ob)
	}
	return groupSnapshot{stats: copytrade.GroupStats(obs), breadth: breadthFrom(ded, nowMs), Samples: len(obs)}
}

// breadthFrom reads market breadth off the most recent COMPLETED 4h window: the
// readings analysed one window ago have, by now, a realised 4h return, so the
// share of coins that rose over that window is known without any new request.
// Each coin counts once (its earliest reading in the window) and the return is
// taken on the long side, so the measure is side-agnostic.
func breadthFrom(ded []store.CopyTradeScoreOutcome, nowMs int64) copytrade.Breadth {
	// The window that just finished starts breadthWindowMs ago; readings taken
	// then are mature now. Widen the start by one window so a gap in scoring
	// does not leave breadth permanently unknown.
	from, to := nowMs-2*breadthWindowMs, nowMs-breadthWindowMs
	if from < nowMs-breadthMaxAgeMs {
		from = nowMs - breadthMaxAgeMs
	}
	type pick struct {
		at  int64
		ret float64
	}
	bySym := map[string]pick{}
	for _, o := range ded {
		if o.AnalyzedAt < from || o.AnalyzedAt > to {
			continue
		}
		r, ok := rawLong(o, "4h")
		if !ok {
			continue
		}
		if cur, seen := bySym[o.Symbol]; seen && cur.at <= o.AnalyzedAt {
			continue
		}
		bySym[o.Symbol] = pick{o.AnalyzedAt, r}
	}
	rets := make([]float64, 0, len(bySym))
	for _, p := range bySym {
		rets = append(rets, p.ret)
	}
	return copytrade.MeasureBreadth(rets)
}

// resetGroupCache drops the measured-history cache. Used by tests, and after a
// settings change that alters how the pool is ranked.
func resetGroupCache() {
	groupCacheMu.Lock()
	groupCache = map[string]groupSnapshot{}
	groupCacheMu.Unlock()
}

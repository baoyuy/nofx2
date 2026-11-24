package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"nofx/copytrade"
	"nofx/logger"
	"nofx/store"
)

// Selection layer: turn the per-coin scores into a small coin pool (<=10, long
// or short) that a strategy can consume. Rules + constraints, with hysteresis
// so a coin does not enter/leave on a single noisy reading.

const (
	poolMaxTotal  = 10
	poolPerSide   = 7
	poolEntryConv = 20.0 // a new coin must be at least this strong to enter
	poolExitConv  = 10.0 // an existing coin is kept down to this (hysteresis)
	// Measured-rank thresholds. On the measured scale (see copytrade.RankCandidates)
	// 40 is a zero lower bound and 30 is the provisional rank of a thin group,
	// so entry at 28 admits thin groups while exit at 20 keeps a member whose
	// group has since weakened slightly.
	poolEntryRank = 28.0
	poolExitRank  = 20.0
	poolScreener  = "copytrade_ai"
	poolName      = "带单AI币池"
)

// lastPoolBuild guards the auto-refresh cadence across manual and timer builds.
var lastPoolBuild atomic.Int64

// poolBuildMu serializes builds (timer, post-analysis, manual button) so two
// concurrent builds cannot both create a pool or interleave their diffs.
var poolBuildMu sync.Mutex

type poolCandidate struct {
	symbol   string
	side     string
	conv     float64
	smoothed float64
}

// poolRankMode is how the pool orders candidates.
//   - "measured" (default): order by the cost-adjusted forward return each
//     (side, conviction band) group actually produced, veto candidates taken
//     against a lopsided market, and drop groups whose measured bound is
//     informative and non-positive.
//   - "conviction": the previous behaviour, ordering on the model's
//     self-reported conviction. Kept so the two can be compared.
const (
	poolRankMeasured   = "measured"
	poolRankConviction = "conviction"
)

// buildCoinPool rebuilds the user's AI coin pool in place. The pool is an
// ordinary coin pool (screener copytrade_ai) with a stable ID: strategies trade
// it by choosing coin source "coin_pool" with that ID, and the dashboard shows
// it through the strategy's candidates. An empty result keeps the pool (with
// no coins) so referencing strategies stop trading instead of falling back to
// the snapshot saved with the strategy.
func (s *Server) buildCoinPool(userID string) (*store.CoinPool, error) {
	poolBuildMu.Lock()
	defer poolBuildMu.Unlock()
	cfg := s.loadCoinScoreAuto()
	refreshMin := cfg.PoolRefreshMinutes
	if refreshMin < 1 {
		refreshMin = 5
	}
	ttlMin := cfg.PoolTTLMinutes
	if ttlMin < 1 {
		ttlMin = 12
	}
	if ttlMin < refreshMin {
		ttlMin = refreshMin * 2
	}

	// Freshness window for keeping a SCORE in the pool must be at least the
	// analysis interval + one refresh margin, otherwise scores near the end of
	// each analysis cycle would be dropped and the pool would flap/empty.
	freshMin := cfg.IntervalMinutes + refreshMin
	if freshMin < ttlMin {
		freshMin = ttlMin
	}
	now := time.Now()
	nowMs := now.UnixMilli()
	freshMs := int64(freshMin) * 60 * 1000

	rows, err := s.store.CopyTrade().ListCoinScores(userID)
	if err != nil {
		return nil, err
	}
	var cands []poolCandidate
	for _, r := range rows {
		// Only fresh scores (within the freshness window) may enter the pool.
		if r.AnalyzedAt > 0 && nowMs-r.AnalyzedAt > freshMs {
			continue
		}
		var res coinScoreResult
		if json.Unmarshal([]byte(r.Payload), &res) != nil || res.Analysis == nil {
			continue
		}
		a := res.Analysis
		if a.Bias != "long" && a.Bias != "short" {
			continue
		}
		conv := a.Conviction
		if a.SmoothedEdge != 0 {
			// Rank on the smoothed edge, but only while it agrees with the
			// latest side: a fresh "long" whose smoothed edge is still negative
			// would otherwise enter as a strong long on short-side strength.
			signed := a.SmoothedEdge
			if a.Bias == "short" {
				signed = -signed
			}
			if signed <= 0 {
				continue
			}
			conv = signed
		}
		cands = append(cands, poolCandidate{symbol: r.Symbol, side: a.Bias, conv: conv, smoothed: a.SmoothedEdge})
	}
	// Existing pool members get the lower exit threshold (hysteresis).
	existing := map[string]bool{}
	prev, err := s.store.CoinPool().GetAuto(userID, poolScreener)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if prev != nil {
		for _, c := range prev.Coins {
			existing[c.Symbol] = true
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].conv > cands[j].conv })

	expiry := now.Add(time.Duration(ttlMin) * time.Minute).UnixMilli()
	var picks []store.CoinPoolCoin
	if cfg.rankMode() == poolRankMeasured {
		picks = s.pickMeasured(userID, cands, existing, expiry)
	} else {
		picks = pickByConviction(cands, existing, expiry)
	}

	// Diff vs previous membership for the change log.
	newSet := map[string]bool{}
	var added []string
	for _, c := range picks {
		newSet[c.Symbol] = true
		if !existing[c.Symbol] {
			added = append(added, c.Symbol)
		}
	}
	var removed []string
	for sym := range existing {
		if !newSet[sym] {
			removed = append(removed, sym)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)

	out := &store.CoinPool{Name: poolName, Screener: poolScreener, Coins: picks}
	if len(picks) == 0 && prev == nil {
		// Nothing to trade and no pool yet: don't create an empty one.
		lastPoolBuild.Store(nowMs)
		return out, nil
	}
	// Update in place (same ID) even when empty, so strategies that use this
	// pool see "no coins now" rather than losing the pool.
	if err := s.store.CoinPool().SaveAuto(userID, out); err != nil {
		return nil, err
	}
	if len(added) > 0 || len(removed) > 0 {
		_ = s.store.CoinPool().AppendLog(userID, added, removed)
	}
	lastPoolBuild.Store(nowMs)
	return out, nil
}

// pickByConviction is the previous selection: order on the model's
// self-reported conviction, with per-side caps and entry/exit hysteresis.
func pickByConviction(cands []poolCandidate, existing map[string]bool, expiry int64) []store.CoinPoolCoin {
	longN, shortN := 0, 0
	picks := make([]store.CoinPoolCoin, 0, poolMaxTotal)
	for _, c := range cands {
		thr := poolEntryConv
		if existing[c.symbol] {
			thr = poolExitConv
		}
		if c.conv < thr {
			continue
		}
		if len(picks) >= poolMaxTotal {
			break
		}
		if c.side == "long" {
			if longN >= poolPerSide {
				continue
			}
			longN++
		} else {
			if shortN >= poolPerSide {
				continue
			}
			shortN++
		}
		picks = append(picks, store.CoinPoolCoin{Symbol: c.symbol, Direction: c.side, Score: c.conv,
			Note: fmt.Sprintf("edge %+.0f conv %.0f", c.smoothed, c.conv), ExpiryAt: expiry})
	}
	return picks
}

// pickMeasured orders candidates by the measured history of their (side,
// conviction band) group and applies the breadth veto.
//
// Two gates in front of the ranking, both one-sided:
//   - breadth: a candidate taken against a broadly lopsided market is dropped.
//     A mixed or unknown reading drops nothing.
//   - group history: a group whose cost-adjusted lower bound is informative and
//     non-positive is dropped. A group with too little history is kept at a
//     provisional rank and marked "样本不足", so a side with no samples yet is
//     not shut out for good.
//
// Hysteresis still applies, on the measured rank rather than on conviction, so
// a coin already in the pool is not dropped by a small change.
func (s *Server) pickMeasured(userID string, cands []poolCandidate, existing map[string]bool, expiry int64) []store.CoinPoolCoin {
	snap := s.groupHistory(userID)
	inputs := make([]copytrade.GroupRankInput, 0, len(cands))
	smoothed := make(map[string]float64, len(cands))
	for _, c := range cands {
		if snap.breadth.BreadthVetoes(c.side) {
			continue
		}
		inputs = append(inputs, copytrade.GroupRankInput{Symbol: c.symbol, Side: c.side, Conviction: c.conv})
		smoothed[c.symbol] = c.smoothed
	}
	ranked := copytrade.RankCandidates(inputs, snap.stats)

	longN, shortN := 0, 0
	picks := make([]store.CoinPoolCoin, 0, poolMaxTotal)
	for _, r := range ranked {
		if r.Blocked {
			continue
		}
		thr := poolEntryRank
		if existing[r.Symbol] {
			thr = poolExitRank
		}
		if r.Rank < thr {
			continue
		}
		if len(picks) >= poolMaxTotal {
			break
		}
		if r.Side == "long" {
			if longN >= poolPerSide {
				continue
			}
			longN++
		} else {
			if shortN >= poolPerSide {
				continue
			}
			shortN++
		}
		picks = append(picks, store.CoinPoolCoin{Symbol: r.Symbol, Direction: r.Side, Score: r.Rank,
			Note: measuredNote(r, snap.breadth, smoothed[r.Symbol]), ExpiryAt: expiry})
	}
	return picks
}

// measuredNote explains a pick in terms of what was measured, never as a
// prediction: the group's sample count and cost-adjusted lower bound, or an
// explicit "样本不足" when the group is too thin to judge.
func measuredNote(r copytrade.GroupRanked, b copytrade.Breadth, smoothedEdge float64) string {
	if r.Thin {
		return fmt.Sprintf("样本不足(%s %s, n=%d) 暂用保守默认分 · 大盘%s · conv %.0f",
			r.Side, r.Band, r.Stat.Samples, b.Label(), r.Conviction)
	}
	bound, metric := r.Stat.NetLower, "净"
	if r.Stat.HasExcess {
		bound, metric = r.Stat.ExcessLower, "超额"
	}
	return fmt.Sprintf("历史%s下限 %+.2f%%/4h (n=%d, 胜率%.0f%%, 扣成本%.1f%%) · 组 %s %s · 大盘%s · conv %.0f · edge %+.0f",
		metric, bound*100, r.Stat.Samples, r.Stat.WinRate*100, copytrade.GroupCost*100,
		r.Side, r.Band, b.Label(), r.Conviction, smoothedEdge)
}

func (s *Server) handleCoinPoolGenerate(c *gin.Context) {
	pool, err := s.buildCoinPool(c.GetString("user_id"))
	if err != nil {
		SafeInternalError(c, "Generate coin pool", err)
		return
	}
	history, _ := s.store.CoinPool().ListLogs(c.GetString("user_id"), 20)
	updated := time.Now().UnixMilli()
	if !pool.UpdatedAt.IsZero() {
		updated = pool.UpdatedAt.UnixMilli()
	}
	c.JSON(http.StatusOK, gin.H{"id": pool.ID, "name": pool.Name, "coins": pool.Coins, "count": len(pool.Coins),
		"updated_at": updated, "history": history})
}

func (s *Server) handleCoinPoolGet(c *gin.Context) {
	userID := c.GetString("user_id")
	history, _ := s.store.CoinPool().ListLogs(userID, 20)
	p, err := s.store.CoinPool().GetAuto(userID, poolScreener)
	if err == nil {
		c.JSON(http.StatusOK, gin.H{"id": p.ID, "name": p.Name, "coins": p.Coins, "count": len(p.Coins),
			"updated_at": p.UpdatedAt.UnixMilli(), "history": history})
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		SafeInternalError(c, "Load AI coin pool", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": "", "coins": []store.CoinPoolCoin{}, "count": 0, "updated_at": 0, "history": history})
}

// StartCoinPoolAuto refreshes the auto coin pool every configured interval.
func (s *Server) StartCoinPoolAuto(ctx context.Context) { go s.coinPoolAutoLoop(ctx) }

func (s *Server) coinPoolAutoLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.tickCoinPool(ctx)
	}
}

func (s *Server) tickCoinPool(ctx context.Context) {
	cfg := s.loadCoinScoreAuto()
	refresh := cfg.PoolRefreshMinutes
	if refresh < 1 {
		refresh = 5
	}
	now := time.Now().UnixMilli()
	if now-lastPoolBuild.Load() < int64(refresh)*60*1000 {
		return
	}
	// The pool belongs to the account the scheduled scoring runs for (see
	// resolveScoreModelAny), so it lands next to that account's scores.
	_, uid, err := s.resolveScoreModelAny("")
	if err != nil {
		return
	}
	lastPoolBuild.Store(now)
	if _, err := s.buildCoinPool(uid); err != nil {
		logger.Warnf("coin-pool auto refresh: %v", err)
	}
}

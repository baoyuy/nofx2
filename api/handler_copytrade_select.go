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

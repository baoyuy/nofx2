package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"

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

type poolCandidate struct {
	symbol  string
	side    string
	conv    float64
	smoothed float64
}

func (s *Server) buildCoinPool(userID string) (*store.CoinPool, error) {
	rows, err := s.store.CopyTrade().ListCoinScores(userID)
	if err != nil {
		return nil, err
	}
	var cands []poolCandidate
	for _, r := range rows {
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
			conv = absf(a.SmoothedEdge)
		}
		cands = append(cands, poolCandidate{symbol: r.Symbol, side: a.Bias, conv: conv, smoothed: a.SmoothedEdge})
	}
	// Existing pool members get the lower exit threshold (hysteresis).
	existing := map[string]bool{}
	poolID := ""
	if list, err := s.store.CoinPool().List(userID); err == nil {
		for _, p := range list {
			if p.Screener == poolScreener {
				poolID = p.ID
				for _, c := range p.Coins {
					existing[c.Symbol] = true
				}
			}
		}
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].conv > cands[j].conv })

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
			Note: fmt.Sprintf("edge %+.0f conv %.0f", c.smoothed, c.conv)})
	}

	p := &store.CoinPool{Name: poolName, Screener: poolScreener, Coins: picks}
	if poolID != "" {
		p.ID = poolID
		if err := s.store.CoinPool().Update(userID, p); err != nil {
			return nil, err
		}
		return p, nil
	}
	if err := s.store.CoinPool().Create(userID, p); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Server) handleCoinPoolGenerate(c *gin.Context) {
	pool, err := s.buildCoinPool(c.GetString("user_id"))
	if err != nil {
		SafeInternalError(c, "Generate coin pool", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": pool.ID, "name": pool.Name, "coins": pool.Coins, "count": len(pool.Coins)})
}

func (s *Server) handleCoinPoolGet(c *gin.Context) {
	userID := c.GetString("user_id")
	list, err := s.store.CoinPool().List(userID)
	if err != nil {
		SafeInternalError(c, "List coin pools", err)
		return
	}
	for _, p := range list {
		if p.Screener == poolScreener {
			c.JSON(http.StatusOK, gin.H{"id": p.ID, "name": p.Name, "coins": p.Coins, "count": len(p.Coins)})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"id": "", "coins": []store.CoinPoolCoin{}, "count": 0})
}

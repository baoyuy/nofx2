package api

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"nofx/planning/core"
	"nofx/planning/market"
)

// planningTrendCache holds the free trend board. It is built from public
// Binance candles, so one board is shared by every user.
type planningTrendCache struct {
	mu    sync.Mutex
	board market.TrendBoard
	at    time.Time
	err   error
}

const planningTrendTTL = 5 * time.Minute

// Markets on the current trend board get the long-only trend policy. Only an
// already built board is consulted; planning never triggers a new scan.
func (p *planningServices) planningEntryPolicy(venue, symbol, group string, limits core.Constraints) core.Constraints {
	if venue != "binance" {
		return limits
	}
	if group == "ai500" {
		limits.EntryPolicy = core.EntryAI500TrendLong
		return limits
	}
	cached := &p.trend
	cached.mu.Lock()
	defer cached.mu.Unlock()
	if cached.err != nil || cached.at.IsZero() || time.Since(cached.at) >= planningTrendTTL {
		return limits
	}
	for _, coin := range cached.board.Coins {
		if strings.EqualFold(coin.Pair, strings.TrimSpace(symbol)) {
			limits.EntryPolicy = core.EntryAI500TrendLong
			break
		}
	}
	return limits
}

// handlePlanningAI500 serves the free trend board on the legacy "ai500" path
// so saved plans and the long-only policy keep working.
func (s *Server) handlePlanningAI500(c *gin.Context) {
	p, err := s.planningService()
	if err != nil {
		planningError(c, err)
		return
	}
	cached := &p.trend
	cached.mu.Lock()
	defer cached.mu.Unlock()
	// Free data: a failed scan may be retried after 30 seconds.
	if cached.at.IsZero() || time.Since(cached.at) >= planningTrendTTL || (cached.err != nil && time.Since(cached.at) >= 30*time.Second) {
		read := p.trendRead
		if read == nil {
			read = p.source.TrendBoard
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
		board, e := read(ctx)
		cancel()
		cached.board, cached.err, cached.at = board, e, time.Now().UTC()
	}
	if cached.err != nil {
		planningError(c, errors.New("Trend board unavailable: Binance public data could not be read. Retry in a few minutes."))
		return
	}
	c.JSON(200, gin.H{"coins": cached.board.Coins, "scanned": cached.board.Scanned, "sources": cached.board.Sources, "fetched_at": cached.board.FetchedAt, "cache_seconds": int(planningTrendTTL.Seconds())})
}

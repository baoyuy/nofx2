package api

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nofx/copytrade"
	"nofx/trader/binance"
)

// Forward-4h accuracy over VALID AI samples only:
//   valid sample = in AI scope + has a score + 4h window complete + market data
//   present + |4h move| >= neutral threshold.
// Neutral moves, unfinished windows, and out-of-scope coins are NOT misses.

type accBucket struct {
	Scored    int     `json:"scored"`     // in-scope coins that have a score
	Matured   int     `json:"matured"`    // 4h window completed (has an outcome)
	Valid     int     `json:"valid"`      // hit + miss (|move| >= neutral)
	Hit       int     `json:"hit"`
	Miss      int     `json:"miss"`
	Invalid   int     `json:"invalid"`    // matured but neutral/incomplete
	Pending   int     `json:"pending"`    // scored but 4h not finished yet
	HitRate   float64 `json:"hit_rate"`   // hit / valid * 100
}

func (b *accBucket) finalize() {
	b.Valid = b.Hit + b.Miss
	b.Pending = b.Scored - b.Matured
	if b.Pending < 0 {
		b.Pending = 0
	}
	if b.Valid > 0 {
		b.HitRate = float64(b.Hit) / float64(b.Valid) * 100
	}
}

func (s *Server) handleCoinAccuracy(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	userID := c.GetString("user_id")
	cfg := s.loadCoinScoreAuto()
	minVol := cfg.minVolume()
	// Reported only; the shared freshness window (see scoreFreshMs).
	ttlMin := int(cfg.scoreFreshMs() / 60000)
	neutral := coinScoreNeutralMovePct / 100.0

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	sums := copytrade.Symbols(s.copyTrade.Snapshots())
	if vols, err := binance.PublicQuoteVolumes(ctx); err == nil && len(vols) > 0 {
		copytrade.AttachRanks(sums, vols)
	}
	inScope := map[string]bool{}
	for _, x := range sums {
		if minVol <= 0 || (x.Volume24h > 0 && x.Volume24h >= minVol) {
			inScope[x.Symbol] = true
		}
	}

	// in-scope coins that have a saved score (counts toward "scored").
	scoreRows, _ := s.store.CopyTrade().ListCoinScores(userID)
	scoredSet := map[string]bool{}
	for _, r := range scoreRows {
		if inScope[r.Symbol] {
			scoredSet[r.Symbol] = true
		}
	}

	rows, _, _ := s.store.CopyTrade().ListScoreOutcomesSince(userID, time.Now().UnixMilli()-outcomeStatsWindowMs)
	// dedup: one coin+direction per 4h (rows are already oldest first).
	seen := map[string]int64{}
	overall := accBucket{}
	bySide := map[string]*accBucket{"long": {}, "short": {}}
	maturedSet := map[string]bool{}
	for _, o := range rows {
		if !inScope[o.Symbol] {
			continue
		}
		// A neutral reading made no call. Its stored return is the raw long
		// return, so counting it here scored every neutral as a long call
		// (by_side already skipped it, so the two rows disagreed).
		if o.Bias != "long" && o.Bias != "short" {
			continue
		}
		k := o.Symbol + "|" + o.Bias
		if last, ok := seen[k]; ok && o.AnalyzedAt-last < 4*3600*1000 {
			continue
		}
		seen[k] = o.AnalyzedAt
		if o.Ret4h == nil || !o.Done4h {
			continue // window not finished yet
		}
		maturedSet[o.Symbol] = true
		overall.Matured++
		side := bySide[o.Bias]
		if side != nil {
			side.Matured++
		}
		if math.Abs(*o.Ret4h) < neutral {
			overall.Invalid++
			if side != nil {
				side.Invalid++
			}
			continue
		}
		if *o.Ret4h > 0 {
			overall.Hit++
			if side != nil {
				side.Hit++
			}
		} else {
			overall.Miss++
			if side != nil {
				side.Miss++
			}
		}
	}
	// scored = in-scope with a score; matured recomputed above.
	overall.Scored = len(scoredSet)
	overall.finalize()
	for _, side := range bySide {
		// side.Scored is not tracked per side; leave 0 (shown as --).
		side.Valid = side.Hit + side.Miss
		if side.Valid > 0 {
			side.HitRate = float64(side.Hit) / float64(side.Valid) * 100
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"snapshot": gin.H{
			"volume_source":  "binance_usdm_24h",
			"taken_at":       time.Now().UnixMilli(),
			"min_volume":     minVol,
			"neutral_pct":    coinScoreNeutralMovePct,
			"ttl_minutes":    ttlMin,
			"config_version": coinScoreConfigVersion,
		},
		"overall": overall,
		"by_side": bySide,
	})
}

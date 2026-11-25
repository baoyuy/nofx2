package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nofx/copytrade"
	"nofx/trader/binance"
)

// Unified AI scoring scope/status view. This is the single source of truth for
// "why does this coin have no AI score": every copy-trade coin is classified as
// out_of_scope (volume below the floor), waiting, scoring, failed, expired or
// scored — computed from ONE 24h-volume snapshot and the saved thresholds.

type coinStatusCounts struct {
	All        int `json:"all"`
	InScope    int `json:"in_scope"`
	Scored     int `json:"scored"`
	Pending    int `json:"pending"` // in_scope - scored (waiting/scoring/failed/expired)
	Waiting    int `json:"waiting"`
	Scoring    int `json:"scoring"`
	Failed     int `json:"failed"`
	Expired    int `json:"expired"`
	OutOfScope int `json:"out_of_scope"`
}

type coinStatusRow struct {
	Symbol    string  `json:"symbol"`
	Status    string  `json:"status"` // scored|scoring|waiting|failed|expired|out_of_scope
	Volume24h float64 `json:"volume_24h"`
	MinVolume float64 `json:"min_volume"`
	AgeMs     int64   `json:"age_ms"` // score age; -1 if never scored
	Bias      string  `json:"bias,omitempty"`
	Score     float64 `json:"score,omitempty"`
	Reason    string  `json:"reason,omitempty"`
}

const coinScoreFailTTLMs = int64(30 * 60 * 1000) // a failure older than this is just "waiting"

func (s *Server) handleCoinStatus(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	userID := c.GetString("user_id")
	cfg := s.loadCoinScoreAuto()
	minVol := cfg.minVolume()
	// One shared definition of "this reading is still current" (see
	// scoreFreshMs): the program, pool, rankings and status pages must agree,
	// or a coin is tradeable by one and expired by another.
	ttlMs := cfg.scoreFreshMs()
	ttlMin := int(ttlMs / 60000)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	sums := copytrade.Symbols(s.copyTrade.Snapshots())
	if vols, err := binance.PublicQuoteVolumes(ctx); err == nil && len(vols) > 0 {
		copytrade.AttachRanks(sums, vols)
	}
	takenAt := time.Now().UnixMilli()

	type sc struct {
		at    int64
		bias  string
		score float64
	}
	rows, _ := s.store.CopyTrade().ListCoinScores(userID)
	scoreMap := map[string]sc{}
	for _, r := range rows {
		var res coinScoreResult
		if json.Unmarshal([]byte(r.Payload), &res) != nil || res.Analysis == nil {
			continue
		}
		scoreMap[r.Symbol] = sc{r.AnalyzedAt, res.Analysis.Bias, res.Analysis.Score}
	}

	s.coinScore.mu.Lock()
	inflight := make(map[string]bool, len(s.coinScore.inflight))
	for k, v := range s.coinScore.inflight {
		inflight[k] = v
	}
	failing := make(map[string]int64, len(s.coinScore.lastFail))
	for k, v := range s.coinScore.lastFail {
		failing[k] = v
	}
	retry := make(map[string]int64, len(s.coinScore.retryAfter))
	for k, v := range s.coinScore.retryAfter {
		retry[k] = v
	}
	s.coinScore.mu.Unlock()

	var counts coinStatusCounts
	out := make([]coinStatusRow, 0, len(sums))
	for _, x := range sums {
		vol := x.Volume24h
		row := coinStatusRow{Symbol: x.Symbol, Volume24h: vol, MinVolume: minVol, AgeMs: -1}
		counts.All++
		if minVol > 0 && (vol <= 0 || vol < minVol) {
			row.Status = "out_of_scope"
			if vol <= 0 {
				row.Reason = "volume_unknown"
			} else {
				row.Reason = "volume_below_min"
			}
			counts.OutOfScope++
			out = append(out, row)
			continue
		}
		counts.InScope++
		if scv, ok := scoreMap[x.Symbol]; ok {
			row.AgeMs = takenAt - scv.at
			row.Bias = scv.bias
			row.Score = scv.score
			if row.AgeMs <= ttlMs {
				row.Status = "scored"
				counts.Scored++
			} else {
				row.Status = "expired"
				row.Reason = "score_expired"
				counts.Expired++
			}
			out = append(out, row)
			continue
		}
		if inflight[x.Symbol] {
			row.Status = "scoring"
			counts.Scoring++
		} else if ra, ok := retry[x.Symbol]; ok && ra > takenAt {
			row.Status = "failed"
			row.Reason = "retry_backoff"
			counts.Failed++
		} else if fa, ok := failing[x.Symbol]; ok && takenAt-fa < coinScoreFailTTLMs {
			row.Status = "failed"
			row.Reason = "last_attempt_failed"
			counts.Failed++
		} else {
			row.Status = "waiting"
			row.Reason = "queued"
			counts.Waiting++
		}
		out = append(out, row)
	}
	counts.Pending = counts.InScope - counts.Scored

	c.JSON(http.StatusOK, gin.H{
		"snapshot": gin.H{
			"volume_source":  "binance_usdm_24h",
			"taken_at":       takenAt,
			"min_volume":     minVol,
			"ttl_minutes":    ttlMin,
			"config_version": coinScoreConfigVersion,
		},
		"counts": counts,
		"coins":  out,
	})
}

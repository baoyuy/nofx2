package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"nofx/copytrade"
	"nofx/trader/binance"
)

// Two rankings with distinct scopes:
//   mode=stable : all copy-trade coins, ranked by the calibrated 4h "stable"
//                 score (AI only contributes for coins that HAVE scores; an
//                 unscored coin is NOT treated as zero).
//   mode=ai     : only coins that passed the volume filter AND have a fresh AI
//                 score AND enough data, ranked by the AI composite score.

type rankRow struct {
	Symbol         string  `json:"symbol"`
	Status         string  `json:"status"`
	Direction      string  `json:"direction"`
	Volume24h      float64 `json:"volume_24h"`
	MinVolume      float64 `json:"min_volume"`
	AgeMs          int64   `json:"age_ms"`
	AIScore        float64 `json:"ai_score"`
	Composite      float64 `json:"composite"`
	Reliability    string  `json:"reliability"`
	StableScore    float64 `json:"stable_score"`
	StableAccuracy float64 `json:"stable_accuracy"`
	AIAccuracy     float64 `json:"ai_accuracy"`
	TraderAccuracy float64 `json:"trader_accuracy"`
	AISamples      int     `json:"ai_samples"`
	TraderSamples  int     `json:"trader_samples"`
	Traders        int     `json:"traders"`
	Risk           float64 `json:"risk"`
	Fresh          bool    `json:"fresh"`
	Eligible       bool    `json:"eligible"`
	RankScore      float64 `json:"rank_score"`
	Reason         string  `json:"reason,omitempty"`
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// reliability grades a score by sample size, hit rate, freshness and consistency.
func reliabilityOf(samples int, hitPct float64, fresh bool, consistency float64) string {
	switch {
	case samples >= 30 && hitPct >= 55 && fresh && consistency >= 55:
		return "A"
	case samples >= 10 && hitPct >= 50:
		return "B"
	case samples >= 4:
		return "C"
	default:
		return "D"
	}
}

func (s *Server) handleCoinRanking(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	mode := c.DefaultQuery("mode", "stable")
	if mode != "ai" {
		mode = "stable"
	}
	userID := c.GetString("user_id")
	cfg := s.loadCoinScoreAuto()
	minVol := cfg.MinQuoteVolume
	if minVol <= 0 {
		minVol = coinScoreDefaultMinVolume
	}
	ttlMin := cfg.PoolTTLMinutes
	if ttlMin < 1 {
		ttlMin = 12
	}
	ttlMs := int64(ttlMin) * 60 * 1000
	neutral := coinScoreNeutralMovePct / 100.0

	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	now := time.Now()
	sums := copytrade.Symbols(s.copyTrade.Snapshots())
	if vols, err := binance.PublicQuoteVolumes(ctx); err == nil && len(vols) > 0 {
		copytrade.AttachRanks(sums, vols)
	}
	scoreRows, _ := s.store.CopyTrade().ListCoinScores(userID)
	type ai struct {
		at    int64
		bias  string
		score float64
	}
	am := map[string]ai{}
	for _, r := range scoreRows {
		var res coinScoreResult
		if json.Unmarshal([]byte(r.Payload), &res) != nil || res.Analysis == nil {
			continue
		}
		if _, ok := am[r.Symbol]; ok {
			continue
		}
		am[r.Symbol] = ai{r.AnalyzedAt, res.Analysis.Bias, res.Analysis.Score}
	}

	// Calibrated stable score per coin (AI counts only when it has mature outcomes).
	nowMs := now.UnixMilli()
	for i := range sums {
		sum := &sums[i]
		aiRows, _ := s.store.CopyTrade().RecentMatureScoreOutcomes(userID, sum.Symbol, now.Add(-30*24*time.Hour).UnixMilli())
		tradeRows, _ := s.store.CopyTrade().RecentMatureSignals(sum.Symbol, now.Add(-30*24*time.Hour).Unix())
		var ah, an, th, tn int
		for _, o := range aiRows {
			if (o.Bias != "long" && o.Bias != "short") || o.Ret4h == nil {
				continue
			}
			if math.Abs(*o.Ret4h) < neutral {
				continue
			}
			an++
			if *o.Ret4h > 0 {
				ah++
			}
		}
		for _, sig := range tradeRows {
			if (sig.Direction != "long" && sig.Direction != "short") || sig.Ret4h == nil {
				continue
			}
			ex := *sig.Ret4h
			if sig.Bench4h != nil {
				ex -= *sig.Bench4h
			}
			tn++
			if (sig.Direction == "long" && ex > 0) || (sig.Direction == "short" && ex < 0) {
				th++
			}
		}
		dir := sum.StyleReading.Direction
		if dir == "neutral" {
			dir = sum.Rank.Direction
		}
		if dir == "neutral" {
			dir = sum.Shadow.Direction
		}
		risk := float64(sum.Liquidated+sum.Long.Trapped+sum.Short.Trapped) / float64(max(1, sum.Long.Traders+sum.Short.Traders))
		fresh := len(copytrade.Fresh(s.copyTrade.Snapshots(), copyTradeFreshness, now)) > 0 || copytrade.StableFresh(am[sum.Symbol].at, now)
		sum.Stable = copytrade.StableScore(ah, an, th, tn, dir, risk, fresh)
		sum.Stable.Traders = sum.Long.Traders + sum.Short.Traders
	}

	counts := coinStatusCounts{}
	rows := make([]rankRow, 0, len(sums))
	for i := range sums {
		sum := &sums[i]
		counts.All++
		row := rankRow{
			Symbol: sum.Symbol, Volume24h: sum.Volume24h, MinVolume: minVol, AgeMs: -1,
			StableScore: sum.Stable.Score, StableAccuracy: sum.Stable.Accuracy,
			AIAccuracy: sum.Stable.AIAccuracy, TraderAccuracy: sum.Stable.TraderAccuracy,
			AISamples: sum.Stable.AI_Samples, TraderSamples: sum.Stable.TraderSamples,
			Traders: sum.Stable.Traders, Risk: sum.Stable.Risk, Fresh: sum.Stable.Fresh,
			Eligible: sum.Stable.Eligible, RankScore: sum.Rank.Score, Direction: sum.Stable.Direction,
		}
		outOfScope := minVol > 0 && (sum.Volume24h <= 0 || sum.Volume24h < minVol)
		if outOfScope {
			row.Status = "out_of_scope"
			row.Reason = "volume_below_min"
			counts.OutOfScope++
		} else {
			counts.InScope++
			if a, ok := am[sum.Symbol]; ok {
				row.AgeMs = nowMs - a.at
				row.AIScore = a.score
				if a.bias != "neutral" {
					row.Direction = a.bias
				}
				if row.AgeMs <= ttlMs {
					row.Status = "scored"
					counts.Scored++
					// AI composite: 25/25/20/15/10/5 + risk deduction.
					aiStrength := clampPct(a.score)
					dirQuality := clampPct(sum.Rank.Score)
					fourH := clampPct(sum.Stable.Accuracy * 100)
					histHit := clampPct(sum.Stable.AIAccuracy * 100)
					consensus := clampPct(sum.Confidence.Score)
					freshPct := 0.0
					if sum.Stable.Fresh {
						freshPct = 100
					}
					ded := 20 * sum.Stable.Risk
					if sum.Stable.AI_Samples < 4 {
						ded += 10
					}
					if sum.Confidence.Agreement < 0.5 {
						ded += 10
					}
					if sum.Long.Traders+sum.Short.Traders <= 1 {
						ded += 10
					}
					if minVol > 0 && sum.Volume24h > 0 && sum.Volume24h < minVol*1.2 {
						ded += 5
					}
					row.Composite = clampPct(.25*aiStrength + .25*dirQuality + .20*fourH + .15*histHit + .10*consensus + .05*freshPct - ded)
					row.Reliability = reliabilityOf(sum.Stable.AI_Samples, sum.Stable.AIAccuracy*100, sum.Stable.Fresh, sum.Stable.Accuracy*100)
				} else {
					row.Status = "expired"
					row.Reason = "score_expired"
					counts.Expired++
				}
			} else {
				row.Status = "waiting"
				row.Reason = "queued"
				counts.Waiting++
			}
		}
		rows = append(rows, row)
	}
	counts.Pending = counts.InScope - counts.Scored

	// BOTH rankings now use the SAME universe: coins that passed the volume
	// filter AND have a FRESH AI score (status=scored) — i.e. exactly the AI
	// scoring list (currently ~28). Only the ordering differs:
	//   stable -> by the calibrated 4h stable score
	//   ai     -> by the AI composite score
	scored := rows[:0:0]
	for _, r := range rows {
		if r.Status == "scored" {
			scored = append(scored, r)
		}
	}
	rows = scored
	if mode == "ai" {
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Composite != rows[j].Composite {
				return rows[i].Composite > rows[j].Composite
			}
			return rows[i].StableScore > rows[j].StableScore
		})
	} else {
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].StableScore != rows[j].StableScore {
				return rows[i].StableScore > rows[j].StableScore
			}
			return rows[i].RankScore > rows[j].RankScore
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"mode": mode,
		"snapshot": gin.H{
			"volume_source":  "binance_usdm_24h",
			"taken_at":       nowMs,
			"min_volume":     minVol,
			"neutral_pct":    coinScoreNeutralMovePct,
			"ttl_minutes":    ttlMin,
			"config_version": coinScoreConfigVersion,
		},
		"counts": counts,
		"items":  rows,
	})
}

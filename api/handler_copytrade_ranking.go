package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"nofx/copytrade"
	"nofx/trader/binance"
)

// Ranking modes:
//   mode=stable : "四小时稳定排行" — a LOCKED board (slot snapshot refreshed
//                 hourly) of the coins that pass the volume filter. Scored
//                 coins use their current score; coins whose score is not
//                 currently fresh keep their LAST known score (marked
//                 expired/waiting) so the board does not flicker. Order is
//                 hysteretic (small score moves don't re-shuffle).
//   mode=ai     : "AI评分排行" — only coins with a CURRENT valid AI score.

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

// Locked 4h-stable board state (shared across requests).
var (
	stableBoardMu sync.Mutex
	stableBoard   = struct {
		symbols   []string
		takenAt   int64
		dropCount map[string]int
		prevRank  map[string]int
	}{dropCount: map[string]int{}, prevRank: map[string]int{}}
)

const (
	stableBoardRefreshMs = 60 * 60 * 1000 // refresh the slot set hourly
	stableBoardGraceMul  = 0.9            // keep a coin until volume < 0.9x min
	stableBoardPlaceholderMs = 24 * 60 * 60 * 1000
)

func (s *Server) handleCoinRanking(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	mode := c.DefaultQuery("mode", "stable")
	if mode != "ai" {
		mode = "stable"
	}
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
	userID := c.GetString("user_id")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	now := time.Now()
	nowMs := now.UnixMilli()
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

	counts := coinStatusCounts{}
	baseBySym := make(map[string]rankRow, len(sums))
	rowOrder := make([]string, 0, len(sums))
	for i := range sums {
		sum := &sums[i]
		counts.All++
		row := rankRow{
			Symbol: sum.Symbol, Volume24h: sum.Volume24h, MinVolume: minVol, AgeMs: -1,
			Traders: sum.Long.Traders + sum.Short.Traders, RankScore: sum.Rank.Score,
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
				dir := a.bias
				if dir != "long" && dir != "short" {
					dir = ""
					row.Reason = "neutral_direction"
				} else {
					row.Direction = dir
				}
				// Compute calibrated stable components (used even if the score is
				// stale, so a coin keeps its last known value while waiting).
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
				risk := float64(sum.Liquidated+sum.Long.Trapped+sum.Short.Trapped) / float64(max(1, sum.Long.Traders+sum.Short.Traders))
				fresh := copytrade.StableFresh(a.at, now)
				sd := copytrade.StableScore(ah, an, th, tn, dir, risk, fresh)
				row.StableAccuracy = sd.Accuracy
				row.AIAccuracy = sd.AIAccuracy
				row.TraderAccuracy = sd.TraderAccuracy
				row.AISamples = sd.AI_Samples
				row.TraderSamples = sd.TraderSamples
				row.Risk = sd.Risk
				row.Fresh = sd.Fresh
				row.Eligible = sd.Eligible
				if dir != "" {
					fourH := clampPct(sd.Accuracy * 100)
					histHit := clampPct(sd.AIAccuracy * 100)
					dirQ := clampPct(sum.Rank.Score)
					consensus := clampPct(sum.Confidence.Score)
					freshPct := 0.0
					if sd.Fresh {
						freshPct = 100
					}
					ded := 20 * sd.Risk
					if sd.AI_Samples < 4 {
						ded += 10
					}
					if sum.Confidence.Agreement < 0.5 {
						ded += 10
					}
					if minVol > 0 && sum.Volume24h > 0 && sum.Volume24h < minVol*1.2 {
						ded += 5
					}
					row.Composite = clampPct(.25*clampPct(a.score) + .25*dirQ + .20*fourH + .15*histHit + .10*consensus + .05*freshPct - ded)
					row.StableScore = clampPct(.30*fourH + .25*dirQ + .20*histHit + .15*freshPct + .10*consensus - ded)
					row.Reliability = reliabilityOf(sd.AI_Samples, sd.AIAccuracy*100, sd.Fresh, sd.Accuracy*100)
				}
				if row.AgeMs <= ttlMs {
					row.Status = "scored"
					counts.Scored++
				} else {
					row.Status = "expired"
					if row.Reason == "" {
						row.Reason = "score_expired"
					}
					counts.Expired++
				}
			} else {
				row.Status = "waiting"
				row.Reason = "queued"
				counts.Waiting++
			}
		}
		baseBySym[sum.Symbol] = row
		rowOrder = append(rowOrder, sum.Symbol)
	}
	counts.Pending = counts.InScope - counts.Scored

	var items []rankRow
	if mode == "ai" {
		// AI ranking: ONLY current valid (fresh, in-scope, directional) scores.
		for _, sym := range rowOrder {
			r := baseBySym[sym]
			if r.Status == "scored" && (r.Direction == "long" || r.Direction == "short") {
				items = append(items, r)
			}
		}
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Composite != items[j].Composite {
				return items[i].Composite > items[j].Composite
			}
			return items[i].StableScore > items[j].StableScore
		})
	} else {
		// Stable ranking: LOCKED board of in-scope slots (refreshed hourly),
		// including waiting/expired coins with their last known score.
		stableBoardMu.Lock()
		if len(stableBoard.symbols) == 0 || nowMs-stableBoard.takenAt > stableBoardRefreshMs {
			prev := map[string]bool{}
			for _, s := range stableBoard.symbols {
				prev[s] = true
			}
			next := make([]string, 0, len(rowOrder))
			for _, sym := range rowOrder {
				r := baseBySym[sym]
				if r.Volume24h <= 0 {
					continue
				}
				inPrev := prev[sym]
				if minVol <= 0 || r.Volume24h >= minVol {
					next = append(next, sym)
					delete(stableBoard.dropCount, sym)
				} else if inPrev {
					if r.Volume24h >= minVol*stableBoardGraceMul {
						next = append(next, sym) // grace keep near the threshold
					} else {
						stableBoard.dropCount[sym]++
						if stableBoard.dropCount[sym] < 2 {
							next = append(next, sym)
						}
					}
				}
			}
			stableBoard.symbols = next
			stableBoard.takenAt = nowMs
		}
		boardSymbols := append([]string(nil), stableBoard.symbols...)
		prevRank := make(map[string]int, len(stableBoard.prevRank))
		for k, v := range stableBoard.prevRank {
			prevRank[k] = v
		}
		stableBoardMu.Unlock()

		for _, sym := range boardSymbols {
			r, ok := baseBySym[sym]
			if !ok {
				continue
			}
			// Never scored / no recent score within placeholder window => keep
			// at the bottom labelled 待评分.
			if r.Status == "waiting" || (r.Status == "expired" && r.AgeMs > stableBoardPlaceholderMs) {
				if r.Status == "expired" {
					r.Status = "waiting"
					r.Reason = "score_stale"
				}
				r.StableScore = 0
				r.Composite = 0
			}
			items = append(items, r)
		}
		// Hysteretic sort: ignore differences smaller than 2 points and keep the
		// previous relative order in that case.
		sort.SliceStable(items, func(i, j int) bool {
			si, sj := items[i].StableScore, items[j].StableScore
			if math.Abs(si-sj) >= 2 {
				return si > sj
			}
			pi, oki := prevRank[items[i].Symbol]
			pj, okj := prevRank[items[j].Symbol]
			if oki && okj {
				return pi < pj
			}
			return si > sj
		})
		stableBoardMu.Lock()
		stableBoard.prevRank = make(map[string]int, len(items))
		for i := range items {
			stableBoard.prevRank[items[i].Symbol] = i
		}
		stableBoardMu.Unlock()
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
		"items":  items,
	})
}

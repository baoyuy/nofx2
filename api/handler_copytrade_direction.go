package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"nofx/copytrade"
	"nofx/store"
	"nofx/trader/binance"
)

const directionRuleVersion = "direction-v1-research"

type directionPick struct {
	EntryPrice   float64                     `json:"entry_price"`
	Symbol       string                      `json:"symbol"`
	Direction    string                      `json:"direction"`
	AI           copytrade.DirectionEvidence `json:"ai_history"`
	Trader       copytrade.DirectionEvidence `json:"trader_history"`
	Strength     float64                     `json:"strength"`
	Calls        int                         `json:"calls"`
	Move1h       float64                     `json:"move_1h"`
	Move4h       float64                     `json:"move_4h"`
	Invalidation float64                     `json:"invalidation_price"`
	AsOf         int64                       `json:"as_of"`
}

// Histories contain only completed windows available at decision time, use
// absolute direction returns (not BTC excess), and count each side separately.
func directionHistory(ai []store.CopyTradeScoreOutcome, signals []store.CopyTradeSignal, dir string, now int64) (copytrade.DirectionEvidence, copytrade.DirectionEvidence) {
	ar, tr := []float64{}, []float64{}
	for _, o := range dedupOutcomes(ai) {
		if o.Bias == dir && o.Done4h && o.Ret4h != nil && o.AnalyzedAt+outcomeDedupMs <= now {
			ar = append(ar, *o.Ret4h)
		}
	}
	sort.Slice(signals, func(i, j int) bool { return signals[i].Hour < signals[j].Hour })
	var last int64
	sign := 1.0
	if dir == "short" {
		sign = -1
	}
	for _, o := range signals {
		if o.Direction != dir || !o.Done4h || o.Ret4h == nil || o.Hour*1000+outcomeDedupMs > now {
			continue
		}
		if last != 0 && o.Hour-last < 4*3600 {
			continue
		}
		last = o.Hour
		tr = append(tr, sign**o.Ret4h)
	}
	return copytrade.MeasureDirection(ar), copytrade.MeasureDirection(tr)
}

// Confirm on closed 15m candles only. A reversal through the previous four
// hours' extreme invalidates the thesis; this is not an automated stop order.
func directionTrend(bars []binance.MarketKline, dir string, now int64) (float64, float64, float64, bool) {
	closed := []binance.MarketKline{}
	for _, b := range bars {
		if b.Time+900 <= now {
			closed = append(closed, b)
		}
	}
	if len(closed) < 17 {
		return 0, 0, 0, false
	}
	b := closed[len(closed)-17:]
	end := b[16]
	if now-(end.Time+900) > 900 {
		return 0, 0, 0, false
	}
	for i, c := range b {
		if c.Close <= 0 || c.High < c.Low || (i > 0 && c.Time-b[i-1].Time != 900) {
			return 0, 0, 0, false
		}
	}
	m1, m4 := end.Close/b[12].Close-1, end.Close/b[0].Close-1
	high, low := b[1].High, b[1].Low
	for _, c := range b[1:] {
		if c.High > high {
			high = c.High
		}
		if c.Low < low {
			low = c.Low
		}
	}
	if dir == "short" {
		return m1, m4, high, m1 < 0 && m4 < 0
	}
	return m1, m4, low, dir == "long" && m1 > 0 && m4 > 0
}

func (s *Server) handleDirectionShortlist(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
	defer cancel()
	now := time.Now()
	uid := c.GetString("user_id")
	cfg := s.loadCoinScoreAuto()
	minVol := cfg.minVolume()
	// The shared freshness window (see scoreFreshMs), in minutes.
	ttl := int(cfg.scoreFreshMs() / 60000)
	vols, err := binance.PublicQuoteVolumes(ctx)
	if err != nil || len(vols) == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "行情成交额暂不可用，方向榜暂停生成"})
		return
	}
	scores, err := s.store.CopyTrade().ListCoinScores(uid)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "评分历史读取失败"})
		return
	}
	latest := map[string]store.CopyTradeCoinScore{}
	for _, r := range scores {
		if _, ok := latest[r.Symbol]; !ok {
			latest[r.Symbol] = r
		}
	}
	picks := []directionPick{}
	excluded := map[string]int{}
	candidates := 0
	// Reject stale position/fill sources before deriving directional calls.
	freshSnapshots := []copytrade.Snapshot{}
	for _, snap := range s.copyTrade.Snapshots() {
		if snap.PositionsUpdatedAt.IsZero() || snap.FillsUpdatedAt.IsZero() || now.Sub(snap.PositionsUpdatedAt) > 15*time.Minute || now.Sub(snap.FillsUpdatedAt) > 15*time.Minute {
			excluded["stale_trader_source"]++
			continue
		}
		freshSnapshots = append(freshSnapshots, snap)
	}
	for _, sum := range copytrade.Symbols(freshSnapshots) {
		if v, ok := vols[sum.Symbol]; !ok || v < minVol {
			excluded["volume"]++
			continue
		}
		candidates++
		a, ok := latest[sum.Symbol]
		var res coinScoreResult
		if !ok || a.AnalyzedAt > now.UnixMilli() || now.UnixMilli()-a.AnalyzedAt > int64(ttl)*60000 || json.Unmarshal([]byte(a.Payload), &res) != nil || res.Analysis == nil {
			excluded["ai_not_fresh"]++
			continue
		}
		dir := res.Analysis.Bias
		if (dir != "long" && dir != "short") || sum.Shadow.Direction != dir || sum.StyleReading.Direction != dir {
			excluded["direction_conflict"]++
			continue
		}
		calls := sum.Shadow.LongCalls
		if dir == "short" {
			calls = sum.Shadow.ShortCalls
		}
		if calls < 2 || sum.Shadow.Effective < 2 {
			excluded["few_independent_calls"]++
			continue
		}
		ai, ae := s.store.CopyTrade().RecentMatureScoreOutcomes(uid, sum.Symbol, now.Add(-30*24*time.Hour).UnixMilli())
		signals, se := s.store.CopyTrade().RecentMatureSignals(sum.Symbol, now.Add(-30*24*time.Hour).Unix())
		if ae != nil || se != nil {
			excluded["history_unavailable"]++
			continue
		}
		ah, th := directionHistory(ai, signals, dir, now.UnixMilli())
		if !copytrade.DirectionReady(ah) || !copytrade.DirectionReady(th) {
			excluded["insufficient_evidence"]++
			continue
		}
		// Order conservatively by the weaker source's lower confidence bound.
		strength := ah.Lower95
		if th.Lower95 < strength {
			strength = th.Lower95
		}
		picks = append(picks, directionPick{Symbol: sum.Symbol, Direction: dir, AI: ah, Trader: th, Strength: strength, Calls: calls, AsOf: now.UnixMilli()})
	}
	sort.Slice(picks, func(i, j int) bool {
		if picks[i].Strength == picks[j].Strength {
			return picks[i].Symbol < picks[j].Symbol
		}
		return picks[i].Strength > picks[j].Strength
	})
	// Bound exchange work per read; expose deferred candidates explicitly.
	if len(picks) > 20 {
		excluded["market_check_deferred"] = len(picks) - 20
		picks = picks[:20]
	}
	longs, shorts := []directionPick{}, []directionPick{}
	for _, p := range picks {
		if len(longs)+len(shorts) >= 10 {
			break
		}
		bars, e := binance.GetPublicKlines(ctx, p.Symbol, "15m", 20)
		if e != nil {
			excluded["market_unavailable"]++
			continue
		}
		m1, m4, invalid, ok := directionTrend(bars, p.Direction, now.Unix())
		if !ok {
			excluded["trend_unconfirmed"]++
			continue
		}
		p.Move1h, p.Move4h, p.Invalidation = m1, m4, invalid
		p.EntryPrice = bars[len(bars)-1].Close
		if p.Direction == "long" && len(longs) < 10 {
			longs = append(longs, p)
		}
		if p.Direction == "short" && len(shorts) < 10 {
			shorts = append(shorts, p)
		}
	}
	payload := gin.H{"version": directionRuleVersion, "as_of": now.UnixMilli(), "min_volume": minVol, "candidates": candidates, "long": longs, "short": shorts, "limit": 10, "excluded": excluded, "research_only": true, "note": "历史频率不代表下一次概率；四小时期间反向波动尚未验证。最多十个，不补足名额。"}
	encoded, err := json.Marshal(payload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "方向快照编码失败"})
		return
	}
	// One immutable observation per user/hour, including empty boards. This
	// prevents refresh frequency from inflating the evaluation sample count.
	record := store.DirectionResearch{UserID: uid, Slot: now.Unix() / 3600, At: now.UnixMilli(), Payload: string(encoded)}
	if err := s.store.CopyTrade().SaveDirectionResearch(&record); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "方向榜记录失败，暂停发布"})
		return
	}
	c.JSON(http.StatusOK, payload)
}

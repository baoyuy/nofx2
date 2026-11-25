package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"nofx/copytrade"
	"nofx/logger"
	"nofx/store"
	"nofx/trader/binance"
)

// Forward-return evaluation of every score: measured at 4h/24h/72h, both in
// absolute terms and vs a MARKET benchmark = the average return of all coins
// scored in the same time window (not BTC). Also de-duplicated so the same
// coin+direction counts at most once per 4h.

const (
	outcomeBucketMs = int64(30 * 60 * 1000)     // cohort window for the benchmark
	outcomeDedupMs  = int64(4 * 60 * 60 * 1000) // same coin+side counts once per 4h
	// outcomeRetentionMs is how long evaluated readings are kept. The previous
	// 7 days could never accumulate enough samples to judge a rule across
	// different market conditions, which is the whole point of storing them.
	outcomeRetentionMs = int64(90 * 24 * 60 * 60 * 1000)
	// outcomeStatsWindowMs is how far back the statistics endpoints look.
	outcomeStatsWindowMs = int64(30 * 24 * 60 * 60 * 1000)
	// outcomeExitInterval is the candle size used to price the horizon exit.
	// 1h candles priced a "4h" result up to an hour late; 15m candles bound
	// that error to 15 minutes.
	outcomeExitInterval = "15m"
	// outcomeExitTolMs is how far past the due time an exit candle may sit
	// before the horizon is left pending rather than mispriced.
	outcomeExitTolMs = int64(30 * 60 * 1000)
	// outcomeRoundTripCost is the assumed round-trip execution cost applied to
	// the net figures. It is an assumption, not a measured fill cost, and does
	// not include funding.
	outcomeRoundTripCost = 0.002
	// outcomeNeutralMove: a move smaller than this is not a directional
	// result; it counts as neither a hit nor a miss.
	outcomeNeutralMove = coinScoreNeutralMovePct / 100.0
)

func (s *Server) StartScoreOutcomeLoop(ctx context.Context) {
	go s.scoreOutcomeLoop(ctx)
}

func (s *Server) scoreOutcomeLoop(ctx context.Context) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.evaluateScoreOutcomes(ctx)
		s.evaluateDirectionResearch(ctx)
	}
}

// priceAtKh returns the close of the last candle at or before ms, and how far
// before ms that candle opened. The gap lets callers reject a price that is too
// old to represent the horizon.
func priceAtKh(k []binance.MarketKline, ms int64) (price float64, gapMs int64, ok bool) {
	sec := ms / 1000
	for _, c := range k {
		if c.Time <= sec {
			price, gapMs, ok = c.Close, (sec-c.Time)*1000, true
		} else {
			break
		}
	}
	return price, gapMs, ok
}

func (s *Server) evaluateScoreOutcomes(ctx context.Context) {
	now := time.Now().UnixMilli()
	if err := s.store.CopyTrade().PruneScoreOutcomes(now - outcomeRetentionMs); err != nil {
		logger.Warnf("copy-trade outcomes: prune: %v", err)
	}
	pend, err := s.store.CopyTrade().PendingScoreOutcomes(now)
	if err != nil {
		logger.Warnf("copy-trade outcomes: pending: %v", err)
		return
	}
	if len(pend) == 0 {
		return
	}
	cache := map[string][]binance.MarketKline{}
	for i := range pend {
		o := &pend[i]
		k, ok := cache[o.Symbol]
		if !ok {
			// 15m candles, enough of them to cover the 72h horizon plus the
			// entry lookback, so an exit is priced within 15 minutes of its
			// due time rather than up to an hour late.
			k, _ = binance.GetPublicKlines(ctx, o.Symbol, outcomeExitInterval, 400)
			cache[o.Symbol] = k
		}
		if len(k) == 0 {
			continue
		}
		entry := o.EntryPrice
		if entry <= 0 {
			if p, gap, ok := priceAtKh(k, o.AnalyzedAt); ok && gap <= outcomeExitTolMs {
				entry = p
			}
		}
		if entry <= 0 {
			continue
		}
		changed := false
		set := func(hours int, retP **float64, done *bool) {
			if *done {
				return
			}
			due := o.AnalyzedAt + int64(hours)*3600*1000
			if now < due {
				return
			}
			if due < k[0].Time*1000 {
				// The due time is older than the candles we can fetch (for
				// example the app was down). It can never be priced; close the
				// horizon with no result so the row stops being re-fetched and
				// can be pruned. Statistics skip a nil return.
				*done = true
				changed = true
				return
			}
			exit, gap, ok := priceAtKh(k, due)
			if !ok || gap > outcomeExitTolMs {
				// No candle close enough to the due time yet: leave the
				// horizon pending rather than recording a mispriced result.
				return
			}
			r := exit/entry - 1
			if o.Bias == "short" {
				r = -r
			}
			*retP = &r
			*done = true
			changed = true
		}
		// Benchmark is computed from the cohort at read time, not stored here.
		set(4, &o.Ret4h, &o.Done4h)
		set(24, &o.Ret24h, &o.Done24h)
		set(72, &o.Ret72h, &o.Done72h)
		if changed {
			if err := s.store.CopyTrade().UpdateScoreOutcome(o); err != nil {
				logger.Warnf("copy-trade outcomes: update %s: %v", o.Symbol, err)
			}
		}
	}
}

type outcomeStat struct {
	Horizon   string  `json:"horizon"`
	Side      string  `json:"side"`
	Band      string  `json:"band"`
	Samples   int     `json:"samples"`
	HitRate   float64 `json:"hit_rate"`
	BeatRate  float64 `json:"beat_market"` // % beating the market-average benchmark
	AvgRet    float64 `json:"avg_ret"`
	AvgExcess float64 `json:"avg_excess"` // vs market-average benchmark
	// Cost-adjusted figures. These are the ones to judge a rule by: the gross
	// numbers above ignore fees and slippage entirely.
	//
	// DirSamples counts only samples whose move cleared the neutral threshold,
	// so a flat market is not scored as a near-miss either way.
	DirSamples  int     `json:"dir_samples"`
	NetAvgRet   float64 `json:"net_avg_ret"`   // AvgRet minus the assumed round-trip cost
	NetWinRate  float64 `json:"net_win_rate"`  // % of samples profitable after cost
	NetExcess   float64 `json:"net_excess"`    // cost-adjusted excess over the market
	NetExcessLB float64 `json:"net_excess_lb"` // one-sided 90% lower bound on NetExcess
	CostPct     float64 `json:"cost_pct"`      // the cost assumption applied, in %
}

// baselineStat is a reference strategy measured over the same samples, so an AI
// number can be read against what a rule with no model would have produced.
type baselineStat struct {
	Name string `json:"name"`
	// Label is a short human description of the rule.
	Label      string  `json:"label"`
	Samples    int     `json:"samples"`
	HitRate    float64 `json:"hit_rate"`
	NetAvgRet  float64 `json:"net_avg_ret"`
	NetWinRate float64 `json:"net_win_rate"`
}

func bandOf(bull, bear float64) string {
	s := bull
	if bear > s {
		s = bear
	}
	switch {
	case s >= 70:
		return "high"
	case s >= 40:
		return "mid"
	default:
		return "low"
	}
}

func retOf(o store.CopyTradeScoreOutcome, h string) (*float64, bool) {
	switch h {
	case "4h":
		return o.Ret4h, o.Ret4h != nil
	case "24h":
		return o.Ret24h, o.Ret24h != nil
	case "72h":
		return o.Ret72h, o.Ret72h != nil
	}
	return nil, false
}

// rawLong is the coin's LONG-side return (undo the short inversion) so the
// market benchmark is side-agnostic (average long return of the cohort).
func rawLong(o store.CopyTradeScoreOutcome, h string) (float64, bool) {
	rp, ok := retOf(o, h)
	if !ok {
		return 0, false
	}
	if o.Bias == "short" {
		return -*rp, true
	}
	return *rp, true
}

func cohortBucket(ms int64) int64 { return ms / outcomeBucketMs }

// cohortKey is the benchmark map key for one horizon and cohort.
func cohortKey(h string, ms int64) string {
	return fmt.Sprintf("%s|%d", h, cohortBucket(ms))
}

// dedupOutcomes keeps the first occurrence of each (symbol, bias) within any 4h
// window, so one coin+direction is only counted once per 4h.
func dedupOutcomes(rows []store.CopyTradeScoreOutcome) []store.CopyTradeScoreOutcome {
	sorted := append([]store.CopyTradeScoreOutcome(nil), rows...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].AnalyzedAt < sorted[j].AnalyzedAt })
	seen := map[string]int64{}
	out := make([]store.CopyTradeScoreOutcome, 0, len(sorted))
	for _, o := range sorted {
		k := o.Symbol + "|" + o.Bias
		if last, ok := seen[k]; ok && o.AnalyzedAt-last < outcomeDedupMs {
			continue
		}
		seen[k] = o.AnalyzedAt
		out = append(out, o)
	}
	return out
}

// buildBench computes, per horizon, the average raw long return of each cohort
// (30-min bucket) and an overall fallback ("h|all").
func buildBench(rows []store.CopyTradeScoreOutcome) map[string]float64 {
	sums := map[string]float64{}
	cnts := map[string]int{}
	for _, o := range rows {
		for _, h := range []string{"4h", "24h", "72h"} {
			r, ok := rawLong(o, h)
			if !ok {
				continue
			}
			k := cohortKey(h, o.AnalyzedAt)
			sums[k] += r
			cnts[k]++
			ka := h + "|all"
			sums[ka] += r
			cnts[ka]++
		}
	}
	out := map[string]float64{}
	for k, n := range cnts {
		if n > 0 {
			out[k] = sums[k] / float64(n)
		}
	}
	return out
}

func benchAt(bench map[string]float64, h string, analyzedAt int64) float64 {
	if v, ok := bench[cohortKey(h, analyzedAt)]; ok {
		return v
	}
	return bench[h+"|all"] // fallback
}

func statFor(h, side, band string, rows []store.CopyTradeScoreOutcome, bench map[string]float64) outcomeStat {
	st := outcomeStat{Horizon: h, Side: side, Band: band, CostPct: outcomeRoundTripCost * 100}
	var sum, exSum float64
	var netEx []float64
	hit, beat, n, dirN, netWins := 0, 0, 0, 0, 0
	for _, o := range rows {
		r, ok := retOf(o, h)
		if !ok {
			continue
		}
		n++
		sum += *r
		// A move inside the neutral band is not a directional result: it is
		// counted in Samples but is neither a hit nor a miss.
		if math.Abs(*r) >= outcomeNeutralMove {
			dirN++
			if *r > 0 {
				hit++
			}
		}
		net := *r - outcomeRoundTripCost
		if net > 0 {
			netWins++
		}
		bm := benchAt(bench, h, o.AnalyzedAt)
		// Returns are signed in the predicted direction, while the cohort
		// benchmark is stored as a raw long return. Flip it for shorts before
		// comparing; otherwise the page compares a short's PnL to a long return.
		if o.Bias == "short" {
			bm = -bm
		}
		exSum += *r - bm
		netEx = append(netEx, net-bm)
		if *r > bm {
			beat++
		}
	}
	st.Samples = n
	st.DirSamples = dirN
	if n == 0 {
		return st
	}
	if dirN > 0 {
		st.HitRate = float64(hit) / float64(dirN) * 100
	}
	st.BeatRate = float64(beat) / float64(n) * 100
	st.AvgRet = sum / float64(n) * 100
	st.AvgExcess = exSum / float64(n) * 100
	st.NetAvgRet = st.AvgRet - outcomeRoundTripCost*100
	st.NetWinRate = float64(netWins) / float64(n) * 100
	mean, lb := copytrade.MeanLower90(netEx)
	st.NetExcess, st.NetExcessLB = mean*100, lb*100
	return st
}

// horizonMillis is the length of a statistics horizon label.
func horizonMillis(h string) int64 {
	switch h {
	case "24h":
		return 24 * 3600 * 1000
	case "72h":
		return 72 * 3600 * 1000
	}
	return 4 * 3600 * 1000
}

// baselinesFor measures reference rules over the same de-duplicated readings,
// so the AI's numbers can be compared with what needed no model:
//   - short_all / long_all: take every scored coin on one side. In a trending
//     market these alone can look good, which is exactly why they are shown.
//   - momentum: follow the coin's own direction over the preceding window,
//     using the previous reading of the same coin as the reference point. No
//     model, just continuation.
//
// A baseline is measured on the coin's raw long return, so it is independent of
// what the AI chose for that coin.
func baselinesFor(h string, rows []store.CopyTradeScoreOutcome) []baselineStat {
	type acc struct {
		rets []float64
	}
	sides := map[string]*acc{"long_all": {}, "short_all": {}, "momentum": {}}
	horizonMs := horizonMillis(h)
	// Previous reading per coin, for the momentum rule: its raw long return
	// and when it was taken.
	type prevReading struct {
		at  int64
		ret float64
	}
	prev := map[string]prevReading{}
	for _, o := range rows {
		raw, ok := rawLong(o, h)
		if !ok {
			continue
		}
		sides["long_all"].rets = append(sides["long_all"].rets, raw)
		sides["short_all"].rets = append(sides["short_all"].rets, -raw)
		// Continue the coin's own previous direction, but only when that
		// previous window had FINISHED by now. Its forward return is only
		// knowable once its horizon has elapsed; using it earlier reads the
		// future. This used to be unchecked: 44% of pairs overlapped, which
		// inflated the baseline from +0.19% to +0.91% per 4h after cost.
		if p, seen := prev[o.Symbol]; seen && o.AnalyzedAt-p.at >= horizonMs {
			if p.ret > 0 {
				sides["momentum"].rets = append(sides["momentum"].rets, raw)
			} else if p.ret < 0 {
				sides["momentum"].rets = append(sides["momentum"].rets, -raw)
			}
		}
		// Keep the earliest reading whose window is still open as the
		// reference until it matures, rather than overwriting it with a
		// later, still-unknowable one.
		if p, seen := prev[o.Symbol]; !seen || o.AnalyzedAt-p.at >= horizonMs {
			prev[o.Symbol] = prevReading{o.AnalyzedAt, raw}
		}
	}
	labels := map[string]string{
		"long_all":  "全部做多（不用AI）",
		"short_all": "全部做空（不用AI）",
		"momentum":  "延续上一次方向（不用AI）",
	}
	order := []string{"short_all", "long_all", "momentum"}
	out := make([]baselineStat, 0, len(order))
	for _, name := range order {
		rets := sides[name].rets
		b := baselineStat{Name: name, Label: labels[name], Samples: len(rets)}
		if len(rets) == 0 {
			out = append(out, b)
			continue
		}
		var sum float64
		dirN, hits, netWins := 0, 0, 0
		for _, r := range rets {
			sum += r
			if math.Abs(r) >= outcomeNeutralMove {
				dirN++
				if r > 0 {
					hits++
				}
			}
			if r-outcomeRoundTripCost > 0 {
				netWins++
			}
		}
		if dirN > 0 {
			b.HitRate = float64(hits) / float64(dirN) * 100
		}
		b.NetAvgRet = sum/float64(len(rets))*100 - outcomeRoundTripCost*100
		b.NetWinRate = float64(netWins) / float64(len(rets)) * 100
		out = append(out, b)
	}
	return out
}

// directionalOnly keeps the readings that made a long or short call.
func directionalOnly(rows []store.CopyTradeScoreOutcome) []store.CopyTradeScoreOutcome {
	out := make([]store.CopyTradeScoreOutcome, 0, len(rows))
	for _, o := range rows {
		if o.Bias == "long" || o.Bias == "short" {
			out = append(out, o)
		}
	}
	return out
}

// outcomeVersion labels a row's scoring version; rows recorded before the
// version was stored are "legacy".
func outcomeVersion(o store.CopyTradeScoreOutcome) string {
	if o.ConfigVersion == "" {
		return "legacy"
	}
	return o.ConfigVersion
}

func (s *Server) handleCoinScoreOutcomes(c *gin.Context) {
	now := time.Now().UnixMilli()
	since := now - outcomeStatsWindowMs
	all, truncated, err := s.store.CopyTrade().ListScoreOutcomesSince(c.GetString("user_id"), since)
	if err != nil {
		SafeInternalError(c, "Read score outcomes", err)
		return
	}
	version := c.Query("version") // optional: one scoring version only
	rows := all
	if version != "" {
		rows = make([]store.CopyTradeScoreOutcome, 0, len(all))
		for _, o := range all {
			if outcomeVersion(o) == version {
				rows = append(rows, o)
			}
		}
	}
	rows = dedupOutcomes(rows)
	// The market benchmark and the no-model baselines describe every scored
	// coin, neutral readings included: that is "the market".
	bench := buildBench(rows)
	// The AI's own rows count only the calls it actually made. A neutral
	// reading is not a call; its stored return is the raw long return, so
	// counting it in "all" scored every neutral as a long, which in a falling
	// market dragged the AI's net from +0.50% to +0.07% per 4h.
	calls := directionalOnly(rows)
	var out []outcomeStat
	baselines := map[string][]baselineStat{}
	for _, h := range []string{"4h", "24h", "72h"} {
		out = append(out, statFor(h, "all", "all", calls, bench))
		for _, side := range []string{"long", "short"} {
			var sub []store.CopyTradeScoreOutcome
			for _, o := range calls {
				if o.Bias == side {
					sub = append(sub, o)
				}
			}
			out = append(out, statFor(h, side, "all", sub, bench))
		}
		for _, band := range []string{"high", "mid", "low"} {
			var sub []store.CopyTradeScoreOutcome
			for _, o := range calls {
				if bandOf(o.Bull, o.Bear) == band {
					sub = append(sub, o)
				}
			}
			out = append(out, statFor(h, "all", band, sub, bench))
		}
		baselines[h] = baselinesFor(h, rows)
	}
	// Versions present in the window, newest first, so the page can offer a
	// per-version view instead of mixing prompt generations.
	seenV := map[string]int64{}
	for _, o := range all {
		v := outcomeVersion(o)
		if o.AnalyzedAt > seenV[v] {
			seenV[v] = o.AnalyzedAt
		}
	}
	versions := make([]string, 0, len(seenV))
	for v := range seenV {
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return seenV[versions[i]] > seenV[versions[j]] })
	var oldest int64
	coverageDays := 0.0
	if len(all) > 0 {
		oldest = all[0].AnalyzedAt
		coverageDays = float64(now-oldest) / float64(24*3600*1000)
	}
	c.JSON(http.StatusOK, gin.H{
		"rows":      out,
		"baselines": baselines,
		"window": gin.H{
			"since":          since,
			"oldest":         oldest,
			"coverage_days":  coverageDays,
			"days":           outcomeStatsWindowMs / (24 * 3600 * 1000),
			"retention_days": outcomeRetentionMs / (24 * 3600 * 1000),
			"truncated":      truncated,
			"cost_pct":       outcomeRoundTripCost * 100,
			"neutral_pct":    coinScoreNeutralMovePct,
			"exit_interval":  outcomeExitInterval,
		},
		"version":  version,
		"versions": versions,
	})
}

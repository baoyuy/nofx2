package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

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
	}
}

func priceAtKh(k []binance.MarketKline, ms int64) (float64, bool) {
	sec := ms / 1000
	var p float64
	ok := false
	for _, c := range k {
		if c.Time <= sec {
			p, ok = c.Close, true
		} else {
			break
		}
	}
	return p, ok
}

func (s *Server) evaluateScoreOutcomes(ctx context.Context) {
	now := time.Now().UnixMilli()
	if err := s.store.CopyTrade().PruneScoreOutcomes(now - 7*24*3600*1000); err != nil {
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
			k, _ = binance.GetPublicKlines(ctx, o.Symbol, "1h", 400)
			cache[o.Symbol] = k
		}
		if len(k) == 0 {
			continue
		}
		entry := o.EntryPrice
		if entry <= 0 {
			if p, ok := priceAtKh(k, o.AnalyzedAt); ok {
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
			exit, ok := priceAtKh(k, due)
			if !ok {
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
			k := fmt.Sprintf("%s|%d", h, cohortBucket(o.AnalyzedAt))
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
	if v, ok := bench[fmt.Sprintf("%s|%d", h, cohortBucket(analyzedAt))]; ok {
		return v
	}
	return bench[h+"|all"] // fallback
}

func statFor(h, side, band string, rows []store.CopyTradeScoreOutcome, bench map[string]float64) outcomeStat {
	st := outcomeStat{Horizon: h, Side: side, Band: band}
	var sum, exSum float64
	hit, beat, n := 0, 0, 0
	for _, o := range rows {
		r, ok := retOf(o, h)
		if !ok {
			continue
		}
		n++
		sum += *r
		if *r > 0 {
			hit++
		}
		bm := benchAt(bench, h, o.AnalyzedAt)
		// Returns are signed in the predicted direction, while the cohort
		// benchmark is stored as a raw long return. Flip it for shorts before
		// comparing; otherwise the page compares a short's PnL to a long return.
		if o.Bias == "short" {
			bm = -bm
		}
		exSum += *r - bm
		if *r > bm {
			beat++
		}
	}
	st.Samples = n
	if n == 0 {
		return st
	}
	st.HitRate = float64(hit) / float64(n) * 100
	st.BeatRate = float64(beat) / float64(n) * 100
	st.AvgRet = sum / float64(n) * 100
	st.AvgExcess = exSum / float64(n) * 100
	return st
}

func (s *Server) handleCoinScoreOutcomes(c *gin.Context) {
	rows, err := s.store.CopyTrade().ListScoreOutcomes(c.GetString("user_id"))
	if err != nil {
		SafeInternalError(c, "Read score outcomes", err)
		return
	}
	rows = dedupOutcomes(rows)
	bench := buildBench(rows)
	var out []outcomeStat
	for _, h := range []string{"4h", "24h", "72h"} {
		out = append(out, statFor(h, "all", "all", rows, bench))
		for _, side := range []string{"long", "short"} {
			var sub []store.CopyTradeScoreOutcome
			for _, o := range rows {
				if o.Bias == side {
					sub = append(sub, o)
				}
			}
			out = append(out, statFor(h, side, "all", sub, bench))
		}
		for _, band := range []string{"high", "mid", "low"} {
			var sub []store.CopyTradeScoreOutcome
			for _, o := range rows {
				if bandOf(o.Bull, o.Bear) == band {
					sub = append(sub, o)
				}
			}
			out = append(out, statFor(h, "all", band, sub, bench))
		}
	}
	c.JSON(http.StatusOK, gin.H{"rows": out})
}

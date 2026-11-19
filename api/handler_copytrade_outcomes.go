package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nofx/logger"
	"nofx/store"
	"nofx/trader/binance"
)

// Forward-return evaluation of every score: was the coin (and the side) right at
// 4h/24h/72h, versus the same side of BTC? This is what decides whether the
// scoring and the shorts are worth anything.

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
	// Keep ~90 days of evaluated history; prune older completed rows.
	if err := s.store.CopyTrade().PruneScoreOutcomes(now - 90*24*3600*1000); err != nil {
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
	btc, _ := binance.GetPublicKlines(ctx, "BTCUSDT", "1h", 400)
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
		set := func(hours int, retP, benchP **float64, done *bool) {
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
			if len(btc) > 0 {
				if be, ok := priceAtKh(btc, o.AnalyzedAt); ok {
					if bx, ok2 := priceAtKh(btc, due); ok2 && be > 0 {
						b := bx/be - 1
						if o.Bias == "short" {
							b = -b
						}
						*benchP = &b
					}
				}
			}
			*done = true
			changed = true
		}
		set(4, &o.Ret4h, &o.Bench4h, &o.Done4h)
		set(24, &o.Ret24h, &o.Bench24h, &o.Done24h)
		set(72, &o.Ret72h, &o.Bench72h, &o.Done72h)
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
	BeatRate  float64 `json:"beat_btc"`
	AvgRet    float64 `json:"avg_ret"`
	AvgExcess float64 `json:"avg_excess"`
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

func moveOutcome(h string, rows []store.CopyTradeScoreOutcome) (ret, bench []float64) {
	for _, o := range rows {
		var rp, bp *float64
		switch h {
		case "4h":
			rp, bp = o.Ret4h, o.Bench4h
		case "24h":
			rp, bp = o.Ret24h, o.Bench24h
		case "72h":
			rp, bp = o.Ret72h, o.Bench72h
		}
		if rp == nil {
			continue
		}
		ret = append(ret, *rp)
		if bp != nil {
			bench = append(bench, *bp)
		}
	}
	return
}

func statFor(h, side, band string, rows []store.CopyTradeScoreOutcome) outcomeStat {
	ret, bench := moveOutcome(h, rows)
	st := outcomeStat{Horizon: h, Side: side, Band: band, Samples: len(ret)}
	if len(ret) == 0 {
		return st
	}
	hit, beat := 0, 0
	var sum, exSum float64
	for i, r := range ret {
		sum += r
		if r > 0 {
			hit++
		}
		if i < len(bench) {
			exSum += r - bench[i]
			if r > bench[i] {
				beat++
			}
		}
	}
	st.HitRate = float64(hit) / float64(len(ret)) * 100
	st.AvgRet = sum / float64(len(ret)) * 100
	if len(bench) > 0 {
		st.BeatRate = float64(beat) / float64(len(bench)) * 100
		st.AvgExcess = exSum / float64(len(bench)) * 100
	}
	return st
}

func (s *Server) handleCoinScoreOutcomes(c *gin.Context) {
	rows, err := s.store.CopyTrade().ListScoreOutcomes(c.GetString("user_id"))
	if err != nil {
		SafeInternalError(c, "Read score outcomes", err)
		return
	}
	var out []outcomeStat
	for _, h := range []string{"4h", "24h", "72h"} {
		out = append(out, statFor(h, "all", "all", rows))
		for _, side := range []string{"long", "short"} {
			var sub []store.CopyTradeScoreOutcome
			for _, o := range rows {
				if o.Bias == side {
					sub = append(sub, o)
				}
			}
			out = append(out, statFor(h, side, "all", sub))
		}
		for _, band := range []string{"high", "mid", "low"} {
			var sub []store.CopyTradeScoreOutcome
			for _, o := range rows {
				if bandOf(o.Bull, o.Bear) == band {
					sub = append(sub, o)
				}
			}
			out = append(out, statFor(h, "all", band, sub))
		}
	}
	c.JSON(http.StatusOK, gin.H{"rows": out})
}

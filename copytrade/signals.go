package copytrade

import (
	"context"
	"sort"
	"time"

	"nofx/logger"
	"nofx/store"
)

// Bar is one closed 1h candle: open time (unix seconds) and close.
type Bar struct {
	Time  int64
	Close float64
}

// BarsFunc returns the latest limit 1h candles of a Binance USDT-M symbol,
// oldest first. It uses Binance's public market API, not the copy-trade
// web endpoints, so it does not count against their rate limit.
type BarsFunc func(ctx context.Context, symbol string, limit int) ([]Bar, error)

const (
	maxSignalsPerHour = 30
	// maxSignalRows bounds one hour's rows, and with them the candle requests
	// spent pricing and scoring them: the ranked ones plus the coins with a
	// fresh-call signal. A coin with neither is in no hit-rate group.
	maxSignalRows   = 90
	signalRetention = 120 * 24 * time.Hour
	signalTick      = 5 * time.Minute
	maxBarsFetch    = 1500
)

// signalLoop records each hour's coin readings and fills in what the price
// did 4h, 24h and 72h later.
func (m *Monitor) signalLoop(ctx context.Context) {
	t := time.NewTicker(signalTick)
	defer t.Stop()
	lastPrune := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now()
		// Let the hour's candle close on Binance first.
		if now.Minute() >= 1 {
			if err := m.RecordSignals(ctx, now); err != nil {
				logger.Warnf("copy-trade: record signals: %v", err)
			}
		}
		if err := m.EvaluateSignals(ctx, now); err != nil {
			logger.Warnf("copy-trade: evaluate signals: %v", err)
		}
		if now.Sub(lastPrune) > 24*time.Hour {
			lastPrune = now
			_ = m.store.PruneSignals(now.Add(-signalRetention).Unix())
		}
	}
}

// benchSymbol is the market every call is measured against.
const benchSymbol = "BTCUSDT"

// RecordSignals saves this hour's reading of the maxSignalsPerHour most
// confident coins (marked Ranked) and of every coin with a fresh-call signal,
// priced at the close of the hour that just ended, with what the reading was
// made of. It records each hour once.
func (m *Monitor) RecordSignals(ctx context.Context, now time.Time) error {
	if m.Bars == nil {
		return nil
	}
	hour := now.Truncate(time.Hour).Unix()
	if done, err := m.store.HasSignals(hour); err != nil || done {
		return err
	}
	// Use the same complete-data contract as AI context, not a position-only
	// update timestamp that says nothing about trade history/profile quality.
	fresh := Fresh(m.Snapshots(), time.Hour, now)
	coins := Symbols(fresh)
	sort.SliceStable(coins, func(a, b int) bool { return coins[a].Confidence.Score > coins[b].Confidence.Score })
	before, err := m.store.SignalsAt(hour - 3600)
	if err != nil {
		return err
	}
	prevShare := make(map[string]float64, len(before))
	for _, s := range before {
		if s.Features > 0 {
			prevShare[s.Symbol] = s.LongShare
		}
	}
	var rows []store.CopyTradeSignal
	ranked := 0
	for _, c := range coins {
		if len(rows) >= maxSignalRows {
			break
		}
		called := c.Confidence.Direction != "neutral" && c.Confidence.Score > 0
		shadowed := c.Shadow.Direction != "neutral" && c.Shadow.Score > 0
		// Each row costs candle requests now and when it is scored: keep the
		// ones a hit-rate group counts.
		if !(called && ranked < maxSignalsPerHour) && !shadowed {
			continue
		}
		bars, err := m.Bars(ctx, c.Symbol, 3)
		if err != nil {
			continue // e.g. a contract Binance's market API does not list
		}
		price := closeAt(bars, hour-3600)
		if price <= 0 {
			continue
		}
		row := SignalRow(c, hour, price)
		if called && ranked < maxSignalsPerHour {
			ranked++
			row.Ranked = true
		}
		if p, ok := prevShare[c.Symbol]; ok {
			d := row.LongShare - p
			row.LongShareDelta = &d
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil
	}
	return m.store.SaveSignals(rows)
}

// SignalRow is the stored form of one coin's reading at hour, priced at price.
func SignalRow(c SymbolSummary, hour int64, price float64) store.CopyTradeSignal {
	lead := c.Long
	gap := 0.0
	switch c.Confidence.Direction {
	case "short":
		lead = c.Short
		if lead.AvgEntry > 0 {
			gap = (lead.AvgEntry - price) / lead.AvgEntry * 100
		}
	case "long":
		if lead.AvgEntry > 0 {
			gap = (price - lead.AvgEntry) / lead.AvgEntry * 100
		}
	}
	calls, exits := c.Shadow.LongCalls, c.Shadow.ShortExits
	if c.Shadow.Direction == "short" {
		calls, exits = c.Shadow.ShortCalls, c.Shadow.LongExits
	}
	direction := c.Confidence.Direction
	if direction == "" {
		direction = "neutral"
	}
	return store.CopyTradeSignal{Hour: hour, Symbol: c.Symbol, Direction: direction,
		Confidence: c.Confidence.Score, Traders: c.Confidence.Traders, Price: price,
		Features: 1, LongTraders: c.Long.Traders, ShortTraders: c.Short.Traders, Openers: lead.Openers, Adds: lead.Added, Trapped: lead.Trapped,
		LongShare: c.Confidence.LongShare, HoldHours: lead.HoldHours, EntryGapPct: gap,
		ShadowDirection: c.Shadow.Direction, ShadowScore: c.Shadow.Score, ShadowCalls: calls, ShadowExits: exits,
		ShadowBurst:   c.Shadow.Burst,
		RankDirection: c.Rank.Direction, RankScore: c.Rank.Score, RankStrength: c.Rank.Strength,
		RankIndependence: c.Rank.Independence}
}

func closeAt(bars []Bar, openTime int64) float64 {
	for _, b := range bars {
		if b.Time == openTime {
			return b.Close
		}
	}
	return 0
}

// EvaluateSignals fills in the 4h, 24h and 72h price change of recorded
// signals whose horizon has passed, with BTC's change over the same hours.
func (m *Monitor) EvaluateSignals(ctx context.Context, now time.Time) error {
	if m.Bars == nil {
		return nil
	}
	pending, err := m.store.PendingSignals(now.Unix())
	if err != nil || len(pending) == 0 {
		return err
	}
	bySymbol := map[string][]store.CopyTradeSignal{}
	oldest := pending[0].Hour
	for _, s := range pending {
		bySymbol[s.Symbol] = append(bySymbol[s.Symbol], s)
		oldest = min(oldest, s.Hour)
	}
	closesOf := func(symbol string, from int64) map[int64]float64 {
		// One bar before the signal hour: that close is the entry price.
		limit := int((now.Unix()-from)/3600) + 3
		if limit > maxBarsFetch {
			return nil
		}
		bars, err := m.Bars(ctx, symbol, limit)
		if err != nil {
			return nil
		}
		closes := make(map[int64]float64, len(bars))
		for _, b := range bars {
			closes[b.Time] = b.Close
		}
		return closes
	}
	bench := closesOf(benchSymbol, oldest)
	for symbol, sigs := range bySymbol {
		closes := closesOf(symbol, sigs[0].Hour)
		if closes == nil && int((now.Unix()-sigs[0].Hour)/3600)+3 <= maxBarsFetch {
			continue // try again next tick
		}
		for i := range sigs {
			s := &sigs[i]
			// The candle closing at hour+h opened at hour+h-1h.
			settle := func(h int64, done *bool, ret, mkt **float64) {
				if *done || s.Hour+h*3600 > now.Unix() {
					return
				}
				*done = true
				if c := closes[s.Hour+(h-1)*3600]; c > 0 && s.Price > 0 {
					r := c/s.Price - 1
					*ret = &r
				}
				if from, to := bench[s.Hour-3600], bench[s.Hour+(h-1)*3600]; from > 0 && to > 0 {
					r := to/from - 1
					*mkt = &r
				}
			}
			settle(4, &s.Done4h, &s.Ret4h, &s.Bench4h)
			settle(24, &s.Done24h, &s.Ret24h, &s.Bench24h)
			settle(72, &s.Done72h, &s.Ret72h, &s.Bench72h)
			if err := m.store.UpdateSignal(s); err != nil {
				return err
			}
		}
	}
	return nil
}

// HitRate is how often signals of one group (and direction) moved the called
// way.
type HitRate struct {
	// Group is what the rows are split by: "confidence" (bands high | mid |
	// low | all), "openers" (fresh calls on the leading side: 0 | 1 | 2 | 3+),
	// "shadow" (bands of the fresh-call score, its own direction), "calls"
	// (the same signals by independent callers: 1 | 2 | 3+).
	Group   string `json:"group"`
	Band    string `json:"band"`
	Horizon string `json:"horizon"` // 4h | 24h | 72h
	// Direction is the called side: long | short, or all.
	Direction string  `json:"direction"`
	Samples   int     `json:"samples"`
	Hits      int     `json:"hits"`
	Rate      float64 `json:"rate"` // hits / samples
	// AvgReturn is the mean price change in the called direction.
	AvgReturn float64 `json:"avg_return"`
	// Excess is the same against BTC over the same hours: ExcessSamples calls
	// had a benchmark, ExcessRate of them beat it, by AvgExcess on average.
	// In a falling market every short "hits"; this is what the call added.
	ExcessSamples int     `json:"excess_samples"`
	ExcessRate    float64 `json:"excess_rate"`
	AvgExcess     float64 `json:"avg_excess"`
}

// ConfidenceBand names the hit-rate band of a confidence score.
func ConfidenceBand(score float64) string { return confidenceBand(score) }

func confidenceBand(score float64) string {
	switch {
	case score >= 70:
		return "high"
	case score >= 40:
		return "mid"
	default:
		return "low"
	}
}

// HitRates summarizes evaluated signals since sinceHour.
func (m *Monitor) HitRates(sinceHour int64) ([]HitRate, error) {
	// One hour earlier too, to know whether the first ones continue a run.
	sigs, err := m.store.SignalsSince(sinceHour - 3600)
	if err != nil {
		return nil, err
	}
	return hitRates(sigs, sinceHour), nil
}

// hitRateCacheFor: the AI asks every cycle; the record changes hourly.
const hitRateCacheFor = 10 * time.Minute

// RecentHitRates is the 30-day 24h record per band and direction (rows of
// one band and one side only), for the strategy engine, cached briefly.
func (m *Monitor) RecentHitRates(now time.Time) []HitRate {
	m.hitMu.Lock()
	defer m.hitMu.Unlock()
	if !m.hitAt.IsZero() && now.Sub(m.hitAt) < hitRateCacheFor {
		return m.hitCache
	}
	rows, err := m.HitRates(now.Add(-30 * 24 * time.Hour).Unix())
	if err != nil {
		logger.Warnf("copy-trade: hit rates: %v", err)
		return m.hitCache
	}
	var out []HitRate
	for _, r := range rows {
		if r.Group == GroupConfidence && r.Horizon == "24h" && r.Band != "all" && r.Direction != "all" {
			out = append(out, r)
		}
	}
	m.hitCache, m.hitAt = out, now
	return out
}

// Hit-rate groups.
const (
	GroupConfidence = "confidence"
	GroupOpeners    = "openers"
	GroupShadow     = "shadow"
	// GroupCalls splits the fresh-call signals by how many independent
	// traders called: the fresh-call score rarely leaves its low band while
	// few traders are followed, so the count says more than the band.
	GroupCalls = "calls"
	// GroupRank is the cross-coin score (copytrade.Rank) on its own direction.
	// It weighs fresh calls over holdings, independent voices over accounts,
	// and discounts crowded, thin and chased coins; this group is what says
	// whether that ordering predicts better than Confidence.
	GroupRank = "rank"
)

func openersBand(n int) string {
	switch {
	case n >= 3:
		return "3+"
	case n == 2:
		return "2"
	case n == 1:
		return "1"
	}
	return "0"
}

type rateAcc struct {
	n, hits, en, ehits int
	sum, esum          float64
}

type rateTable map[string]*rateAcc

// add counts one call's result at one horizon. ret and bench are raw price
// changes of the coin and of BTC; dir is the called side.
func (t rateTable) add(group, band, horizon, dir string, ret, bench *float64) {
	if ret == nil {
		return
	}
	sign := 1.0
	if dir == "short" {
		sign = -1
	}
	r := sign * *ret
	for _, b := range []string{band, "all"} {
		for _, d := range []string{dir, "all"} {
			k := group + "|" + b + "|" + horizon + "|" + d
			a := t[k]
			if a == nil {
				a = &rateAcc{}
				t[k] = a
			}
			a.n++
			a.sum += r
			if r > 0 {
				a.hits++
			}
			if bench != nil {
				e := sign * (*ret - *bench)
				a.en++
				a.esum += e
				if e > 0 {
					a.ehits++
				}
			}
		}
	}
}

func (t rateTable) rows(group string, bands []string) []HitRate {
	var out []HitRate
	for _, band := range bands {
		for _, h := range []string{"4h", "24h", "72h"} {
			for _, d := range []string{"all", "long", "short"} {
				row := HitRate{Group: group, Band: band, Horizon: h, Direction: d}
				if a := t[group+"|"+band+"|"+h+"|"+d]; a != nil && a.n > 0 {
					row.Samples, row.Hits = a.n, a.hits
					row.Rate = float64(a.hits) / float64(a.n)
					row.AvgReturn = a.sum / float64(a.n)
					if a.en > 0 {
						row.ExcessSamples = a.en
						row.ExcessRate = float64(a.ehits) / float64(a.en)
						row.AvgExcess = a.esum / float64(a.en)
					}
				}
				out = append(out, row)
			}
		}
	}
	return out
}

// hitRates counts each run of hourly signals once: a coin called the same way
// hour after hour is one call, scored from its first hour. A run starts when
// the coin had no signal the hour before or was called the other way.
// Confidence and the fresh-call score run separately, each on its own
// direction. sigs must be sorted by hour.
func hitRates(sigs []store.CopyTradeSignal, sinceHour int64) []HitRate {
	t := rateTable{}
	type last struct {
		hour int64
		dir  string
	}
	prevConf, prevShadow := map[string]last{}, map[string]last{}
	prevRank := map[string]last{}
	// starts tells whether a signal begins a run of dir, and notes it.
	starts := func(prev map[string]last, s store.CopyTradeSignal, dir string) bool {
		p, seen := prev[s.Symbol]
		prev[s.Symbol] = last{s.Hour, dir}
		return !(seen && p.hour == s.Hour-3600 && p.dir == dir) && s.Hour >= sinceHour
	}
	each := func(s store.CopyTradeSignal, f func(horizon string, ret, bench *float64)) {
		if s.Symbol == benchSymbol {
			// The benchmark cannot beat itself: its excess is not a result.
			s.Bench4h, s.Bench24h, s.Bench72h = nil, nil, nil
		}
		f("4h", s.Ret4h, s.Bench4h)
		f("24h", s.Ret24h, s.Bench24h)
		f("72h", s.Ret72h, s.Bench72h)
	}
	for _, s := range sigs {
		// Ranked rows are the record Confidence always had; rows beyond the
		// hourly top were not recorded before and would dilute it.
		if s.Ranked && (s.Direction == "long" || s.Direction == "short") {
			if starts(prevConf, s, s.Direction) {
				band := confidenceBand(s.Confidence)
				each(s, func(h string, ret, bench *float64) {
					t.add(GroupConfidence, band, h, s.Direction, ret, bench)
					if s.Features > 0 {
						t.add(GroupOpeners, openersBand(s.Openers), h, s.Direction, ret, bench)
					}
				})
			}
		}
		if s.ShadowDirection == "long" || s.ShadowDirection == "short" {
			if starts(prevShadow, s, s.ShadowDirection) {
				band := confidenceBand(s.ShadowScore)
				each(s, func(h string, ret, bench *float64) {
					t.add(GroupShadow, band, h, s.ShadowDirection, ret, bench)
					t.add(GroupCalls, openersBand(s.ShadowCalls), h, s.ShadowDirection, ret, bench)
				})
			}
		}
		if s.RankDirection == "long" || s.RankDirection == "short" {
			if starts(prevRank, s, s.RankDirection) {
				band := confidenceBand(s.RankScore)
				each(s, func(h string, ret, bench *float64) {
					t.add(GroupRank, band, h, s.RankDirection, ret, bench)
				})
			}
		}
	}
	bands := []string{"high", "mid", "low", "all"}
	out := t.rows(GroupConfidence, bands)
	out = append(out, t.rows(GroupOpeners, []string{"3+", "2", "1", "0", "all"})...)
	out = append(out, t.rows(GroupShadow, bands)...)
	out = append(out, t.rows(GroupRank, bands)...)
	return append(out, t.rows(GroupCalls, []string{"3+", "2", "1", "all"})...)
}

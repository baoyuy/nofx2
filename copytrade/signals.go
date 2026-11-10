package copytrade

import (
	"context"
	"math"
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
	signalRetention   = 120 * 24 * time.Hour
	signalTick        = 5 * time.Minute
	maxBarsFetch      = 1500
)

// signalLoop records each hour's coin confidences and fills in what the
// price did 4h and 24h later.
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

// RecordSignals saves this hour's direction confidence of the most
// confident coins, priced at the close of the hour that just ended. It
// records each hour once.
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
	var rows []store.CopyTradeSignal
	for _, c := range coins {
		if len(rows) >= maxSignalsPerHour {
			break
		}
		if c.Confidence.Direction == "neutral" || c.Confidence.Score <= 0 {
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
		rows = append(rows, store.CopyTradeSignal{Hour: hour, Symbol: c.Symbol, Direction: c.Confidence.Direction,
			Confidence: c.Confidence.Score, Traders: c.Confidence.Traders, Price: price})
	}
	if len(rows) == 0 {
		return nil
	}
	return m.store.SaveSignals(rows)
}

func closeAt(bars []Bar, openTime int64) float64 {
	for _, b := range bars {
		if b.Time == openTime {
			return b.Close
		}
	}
	return 0
}

// EvaluateSignals fills in the 4h and 24h price change of recorded signals
// whose horizon has passed.
func (m *Monitor) EvaluateSignals(ctx context.Context, now time.Time) error {
	if m.Bars == nil {
		return nil
	}
	pending, err := m.store.PendingSignals(now.Unix())
	if err != nil {
		return err
	}
	bySymbol := map[string][]store.CopyTradeSignal{}
	for _, s := range pending {
		bySymbol[s.Symbol] = append(bySymbol[s.Symbol], s)
	}
	for symbol, sigs := range bySymbol {
		oldest := sigs[0].Hour
		limit := int((now.Unix()-oldest)/3600) + 2
		var closes map[int64]float64
		if limit <= maxBarsFetch {
			bars, err := m.Bars(ctx, symbol, limit)
			if err != nil {
				continue // try again next tick
			}
			closes = make(map[int64]float64, len(bars))
			for _, b := range bars {
				closes[b.Time] = b.Close
			}
		}
		for i := range sigs {
			s := &sigs[i]
			// The candle closing at hour+h opened at hour+h-1h.
			settle := func(h int64, done *bool, ret **float64) {
				if *done || s.Hour+h*3600 > now.Unix() {
					return
				}
				*done = true
				if c := closes[s.Hour+(h-1)*3600]; c > 0 && s.Price > 0 {
					r := c/s.Price - 1
					*ret = &r
				}
			}
			settle(4, &s.Done4h, &s.Ret4h)
			settle(24, &s.Done24h, &s.Ret24h)
			if err := m.store.UpdateSignal(s); err != nil {
				return err
			}
		}
	}
	return nil
}

// HitRate is how often signals of one confidence band (and direction) moved
// the called way.
type HitRate struct {
	Band    string `json:"band"`    // high | mid | low | all
	Horizon string `json:"horizon"` // 4h | 24h
	// Direction is the called side: long | short, or all.
	Direction string  `json:"direction"`
	Samples   int     `json:"samples"`
	Hits      int     `json:"hits"`
	Rate      float64 `json:"rate"` // hits / samples
	// AvgReturn is the mean price change in the called direction.
	AvgReturn float64 `json:"avg_return"`
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
		if r.Horizon == "24h" && r.Band != "all" && r.Direction != "all" {
			out = append(out, r)
		}
	}
	m.hitCache, m.hitAt = out, now
	return out
}

// hitRates counts each run of hourly signals once: a coin called the same way
// hour after hour is one call, scored from its first hour. A run starts when
// the coin had no signal the hour before or was called the other way.
// sigs must be sorted by hour.
func hitRates(sigs []store.CopyTradeSignal, sinceHour int64) []HitRate {
	type acc struct {
		n, hits int
		sum     float64
	}
	sums := map[string]*acc{}
	add := func(band, horizon string, ret *float64, dir string) {
		if ret == nil {
			return
		}
		r := *ret
		if dir == "short" {
			r = -r
		}
		for _, b := range []string{band, "all"} {
			for _, d := range []string{dir, "all"} {
				k := b + "|" + horizon + "|" + d
				if sums[k] == nil {
					sums[k] = &acc{}
				}
				a := sums[k]
				a.n++
				a.sum += r
				if r > 0 {
					a.hits++
				}
			}
		}
	}
	prev := map[string]store.CopyTradeSignal{}
	for _, s := range sigs {
		p, seen := prev[s.Symbol]
		prev[s.Symbol] = s
		if seen && p.Hour == s.Hour-3600 && p.Direction == s.Direction {
			continue // same call, another hour
		}
		if s.Hour < sinceHour {
			continue
		}
		band := confidenceBand(s.Confidence)
		add(band, "4h", s.Ret4h, s.Direction)
		add(band, "24h", s.Ret24h, s.Direction)
	}
	var out []HitRate
	for _, band := range []string{"high", "mid", "low", "all"} {
		for _, h := range []string{"4h", "24h"} {
			for _, d := range []string{"all", "long", "short"} {
				row := HitRate{Band: band, Horizon: h, Direction: d}
				if a := sums[band+"|"+h+"|"+d]; a != nil && a.n > 0 {
					row.Samples, row.Hits = a.n, a.hits
					row.Rate = float64(a.hits) / float64(a.n)
					row.AvgReturn = a.sum / float64(a.n)
				}
				if math.IsNaN(row.AvgReturn) {
					row.AvgReturn = 0
				}
				out = append(out, row)
			}
		}
	}
	return out
}

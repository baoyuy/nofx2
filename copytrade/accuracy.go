package copytrade

import (
	"context"
	"math"
	"sort"
	"time"

	"nofx/logger"
)

// Accuracy is how often a lead trader's own calls BEAT THE MARKET: each new
// open or add on a coin and side (repeats within callGap count once), scored
// by the price accuracyHorizon later against the fill price, minus the
// benchmark's move over the same window. A rate near 50% therefore means "no
// better than holding the market", not "no better than flat".
type Accuracy struct {
	Calls int     `json:"calls"`
	Hits  int     `json:"hits"`
	Rate  float64 `json:"rate"` // hits / calls
	// AvgReturn is the mean market-relative move in the called direction.
	AvgReturn float64 `json:"avg_return"`
}

const (
	accuracyHorizon  = 24 * time.Hour
	callGap          = 24 * time.Hour
	accuracyInterval = 6 * time.Hour
	// accuracyBars covers the 30 days of fills plus the horizon, hourly.
	accuracyBars = 31*24 + 2
	// Fetching hourly bars per coin; the market API is not the copy-trade
	// one, but the same IP trades: stay slow.
	accuracySpacing = 2 * time.Second
	// minAccuracyCalls: below it a trader's accuracy does not move the vote.
	minAccuracyCalls = 10
	// Accuracy drop rule: enough calls and clearly worse than a coin flip.
	dropAccuracyCalls = 30
	dropAccuracyRate  = 0.45
	// accuracyPrior pulls a few calls towards 50%: rate' = (hits+k/2)/(calls+k).
	accuracyPrior = 10.0
)

// RuleInaccurate: the trader's own calls went the wrong way too often.
const RuleInaccurate = "inaccurate"

// call is one scored-to-be open or add.
type call struct {
	symbol string
	side   string
	at     int64 // ms
	price  float64
}

// callsOf picks the calls from oldest-first events.
func callsOf(events []Event) []call {
	last := map[string]int64{}
	var out []call
	for _, e := range events {
		if (e.Action != "open" && e.Action != "add") || e.Price <= 0 {
			continue
		}
		k := e.Symbol + "|" + e.Side
		if t, ok := last[k]; ok && e.Time-t < callGap.Milliseconds() {
			last[k] = e.Time // a run of adds is one call
			continue
		}
		last[k] = e.Time
		out = append(out, call{symbol: e.Symbol, side: e.Side, at: e.Time, price: e.Price})
	}
	return out
}

// scoreCalls rates calls against hourly bars per coin (open time in s, sorted).
// Calls whose horizon has not passed, or with no bar, are left out.
func scoreCalls(calls []call, bars map[string][]Bar, now time.Time) Accuracy {
	return scoreCallsOver(calls, bars, now, accuracyHorizon)
}

// scoreCallsOver is scoreCalls with the price read horizon after each call.
//
// A call is scored on its move RELATIVE TO THE MARKET (benchSymbol) over the
// same window, not on the raw price move. Scoring the raw move measured market
// direction, not the trader: in a falling month every short-biased trader
// scored high and every long-biased one low, whatever their skill. That rate
// then became a vote weight of up to 8x across Confidence, Shadow and Style,
// so the consensus became a lagging echo of the market — which is exactly why
// it showed no excess edge over BTC, and why it flipped worst at a turn.
//
// The benchmark's own calls are scored on their absolute move: the market
// cannot beat itself.
func scoreCallsOver(calls []call, bars map[string][]Bar, now time.Time, horizon time.Duration) Accuracy {
	var a Accuracy
	var sum float64
	for _, c := range calls {
		target := time.UnixMilli(c.at).Add(horizon)
		if target.After(now) {
			continue
		}
		price := barCloseAt(bars[c.symbol], target)
		if price <= 0 {
			continue
		}
		r := price/c.price - 1
		// Subtract the market's move over the same window. Without a usable
		// benchmark reading the call is skipped rather than scored on beta:
		// a wrong weight is worse than a missing sample.
		if c.symbol != benchSymbol {
			ex, ok := benchMove(bars, c.at, target)
			if !ok {
				continue
			}
			r -= ex
		}
		if c.side == "short" {
			r = -r
		}
		a.Calls++
		sum += r
		if r > 0 {
			a.Hits++
		}
	}
	if a.Calls > 0 {
		a.Rate = float64(a.Hits) / float64(a.Calls)
		a.AvgReturn = sum / float64(a.Calls)
	}
	return a
}

// benchMove is the market's fractional move between the call and its horizon,
// read from the same hourly bars. ok is false when either end is missing.
func benchMove(bars map[string][]Bar, atMs int64, target time.Time) (float64, bool) {
	b := bars[benchSymbol]
	if len(b) == 0 {
		return 0, false
	}
	from := barCloseAt(b, time.UnixMilli(atMs))
	to := barCloseAt(b, target)
	if from <= 0 || to <= 0 {
		return 0, false
	}
	return to/from - 1, true
}

// barCloseAt is the close of the hourly bar holding t.
func barCloseAt(bars []Bar, t time.Time) float64 {
	sec := t.Unix()
	i := sort.Search(len(bars), func(i int) bool { return bars[i].Time > sec }) - 1
	if i < 0 || sec-bars[i].Time >= 3600 {
		return 0
	}
	return bars[i].Close
}

// shrunk pulls the rate of few calls towards 50%.
func (a Accuracy) shrunk() float64 {
	return (float64(a.Hits) + accuracyPrior/2) / (float64(a.Calls) + accuracyPrior)
}

// accuracyFactor scales a trader's vote: 1 until minAccuracyCalls, then
// 0.5 for a shrunk rate of 30% or worse up to 1.5 at 70% or better.
func accuracyFactor(a Accuracy) float64 {
	if a.Calls < minAccuracyCalls {
		return 1
	}
	return math.Max(0.5, math.Min(1.5, 1+(a.shrunk()-0.5)*2.5))
}

// inaccurate is the accuracy drop rule.
func (a Accuracy) inaccurate() bool {
	return a.Calls >= dropAccuracyCalls && a.shrunk() < dropAccuracyRate
}

// accuracyLoop rescores every followed trader's calls every few hours.
func (m *Monitor) accuracyLoop(ctx context.Context) {
	if m.Bars == nil {
		return
	}
	first := time.NewTimer(10 * time.Minute)
	defer first.Stop()
	t := time.NewTicker(accuracyInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		case <-t.C:
		}
		m.ScoreAccuracy(ctx, time.Now())
	}
}

// ScoreAccuracy fetches hourly bars for every coin the traders called and
// stores each trader's accuracy.
func (m *Monitor) ScoreAccuracy(ctx context.Context, now time.Time) {
	m.mu.RLock()
	byTrader := make(map[string][]call, len(m.snaps))
	kinds := make(map[string]string, len(m.snaps))
	symbols := map[string]bool{}
	for id, s := range m.snaps {
		byTrader[id] = s.calls
		kinds[id] = horizonKind(*s)
		for _, c := range s.calls {
			symbols[c.symbol] = true
		}
	}
	m.mu.RUnlock()
	// Every call is scored against the market's move over the same window, so
	// the benchmark's bars are always needed, called or not.
	symbols[benchSymbol] = true
	bars := make(map[string][]Bar, len(symbols))
	for sym := range symbols {
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.accuracySpacing()):
		}
		b, err := m.Bars(ctx, sym, accuracyBars)
		if err != nil {
			continue // e.g. delisted
		}
		bars[sym] = b
	}
	out := make(map[string]Accuracy, len(byTrader))
	outStyle := make(map[string]Accuracy, len(byTrader))
	outBy := make(map[string]map[string]Accuracy, len(byTrader))
	for id, calls := range byTrader {
		out[id] = scoreCalls(calls, bars, now)
		outStyle[id] = scoreCallsOver(calls, bars, now, styleHorizon(kinds[id]))
		classes := map[string][]call{}
		for _, c := range calls {
			classes[ClassOf(c.symbol)] = append(classes[ClassOf(c.symbol)], c)
		}
		by := make(map[string]Accuracy, len(classes))
		for class, list := range classes {
			if a := scoreCalls(list, bars, now); a.Calls > 0 {
				by[class] = a
			}
		}
		outBy[id] = by
	}
	m.accMu.Lock()
	m.accuracy, m.accuracyBy = out, outBy
	m.styleAccuracy = outStyle
	m.accMu.Unlock()
	logger.Infof("copy-trade: scored the calls of %d traders over %d coins", len(out), len(bars))
}

func (m *Monitor) accuracySpacing() time.Duration {
	if m.Client != nil && m.Client.Spacing == 0 {
		return 0 // tests
	}
	return accuracySpacing
}

func (m *Monitor) accuracyOf(id string) (Accuracy, map[string]Accuracy) {
	m.accMu.RLock()
	defer m.accMu.RUnlock()
	return m.accuracy[id], m.accuracyBy[id]
}

func (m *Monitor) styleAccuracyOf(id string) Accuracy {
	m.accMu.RLock()
	defer m.accMu.RUnlock()
	return m.styleAccuracy[id]
}

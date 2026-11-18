package copytrade

import (
	"sort"
	"strings"
	"time"
)

// Following calls: instead of asking who holds a coin, take each trader's
// open as the event and ask what the price did afterwards, against BTC, over
// the horizon of the trader's style. The backtest collects the opens hour by
// hour, with only what was known at that hour (style, high-frequency mark,
// the trader's earlier record), and scores them here.

// agreeWindow is how far back another trader's open on the same coin and
// side counts as agreeing.
const agreeWindow = 4 * time.Hour

// callEvent is one open, as known at the close of the hour it happened in.
type callEvent struct {
	trader string
	kind   string // style group of the trader at that hour (styleKindOf)
	symbol string
	side   string
	at     int64 // ms, the fill
	hour   int64 // unix s, close of the hourly bar holding the fill
	price  float64
	// own is the trader's style horizon in hours; prior their record at it
	// before this open; agree the traders who opened the same way lately.
	own   int
	prior string
	agree int
}

// rawOpen is an open before repeats are dropped.
type rawOpen struct {
	callEvent
	trader string
	votes  bool
}

// priorBand names a trader's earlier record at their own horizon.
func priorBand(a Accuracy) string {
	switch r := a.shrunk(); {
	case a.Calls < minAccuracyCalls:
		return "unrated"
	case r >= 0.55:
		return "55%+"
	case r >= 0.45:
		return "45-55%"
	}
	return "<45%"
}

// callCollector turns the opens of each hour into events: an open repeated
// by the same trader on the same coin and side within their own horizon is
// one event, and each event knows how many traders opened that way lately.
type callCollector struct {
	last   map[string]int64            // trader|symbol|side → ms of the last open
	recent map[string]map[string]int64 // symbol|side → trader → ms
	events []callEvent
}

func newCallCollector() *callCollector {
	return &callCollector{last: map[string]int64{}, recent: map[string]map[string]int64{}}
}

func (c *callCollector) add(opens []rawOpen) {
	sort.SliceStable(opens, func(a, b int) bool {
		if opens[a].at != opens[b].at {
			return opens[a].at < opens[b].at
		}
		return opens[a].trader < opens[b].trader
	})
	for _, o := range opens {
		key := o.symbol + "|" + o.side
		if o.votes {
			if c.recent[key] == nil {
				c.recent[key] = map[string]int64{}
			}
			c.recent[key][o.trader] = o.at
		}
		tk := o.trader + "|" + key
		last, seen := c.last[tk]
		c.last[tk] = o.at
		if seen && o.at-last < int64(o.own)*int64(time.Hour/time.Millisecond) {
			continue
		}
		e := o.callEvent
		e.trader = o.trader
		for _, at := range c.recent[key] {
			if o.at-at <= agreeWindow.Milliseconds() {
				e.agree++
			}
		}
		if !o.votes {
			e.agree++ // itself: it is not among the voters
		}
		c.events = append(c.events, e)
	}
}

// CallRow is the record of one group of followed opens at one horizon.
type CallRow struct {
	Table, Label string
	Hours        int
	// Calls with a price at the horizon, and how many moved the called way.
	Calls int
	Hit   float64
	// Against BTC over the same hours (BTC's own opens left out).
	Excess    int
	Beat      float64
	AvgExcess float64
	MedExcess float64
}

type callAcc struct {
	n, hits, en, ehits int
	esum               float64
	ex                 []float64
}

func (a *callAcc) row(table, label string, hours int) CallRow {
	r := CallRow{Table: table, Label: label, Hours: hours, Calls: a.n, Excess: a.en}
	if a.n > 0 {
		r.Hit = float64(a.hits) / float64(a.n)
	}
	if a.en > 0 {
		r.Beat = float64(a.ehits) / float64(a.en)
		r.AvgExcess = a.esum / float64(a.en)
		sort.Float64s(a.ex)
		r.MedExcess = a.ex[len(a.ex)/2]
		if len(a.ex)%2 == 0 {
			r.MedExcess = (a.ex[len(a.ex)/2-1] + r.MedExcess) / 2
		}
	}
	return r
}

// followOutcome is what following one open returned over hours. Entered at
// the close of the hour the open happened in (what a follower polling and
// deciding within the hour gets), or, when atFill, at the trader's own price.
func followOutcome(e callEvent, hours int, atFill bool, closes map[string]map[int64]float64, bars map[string][]Bar, end int64) (ret, excess float64, ok, benched bool) {
	sign := 1.0
	if e.side == "short" {
		sign = -1
	}
	var entry, exit, benchIn, benchOut float64
	if atFill {
		target := time.UnixMilli(e.at).Add(time.Duration(hours) * time.Hour)
		if target.Unix() > end {
			return 0, 0, false, false
		}
		entry, exit = e.price, barCloseAt(bars[e.symbol], target)
		// BTC at the fill: between the closes around it.
		c0, c1 := closes[benchSymbol][e.hour-7200], closes[benchSymbol][e.hour-3600]
		if c0 > 0 && c1 > 0 {
			benchIn = c0 + (c1-c0)*float64(e.at/1000-(e.hour-3600))/3600
		}
		benchOut = barCloseAt(bars[benchSymbol], target)
	} else {
		if e.hour+int64(hours)*3600 > end {
			return 0, 0, false, false
		}
		entry, exit = closes[e.symbol][e.hour-3600], closes[e.symbol][e.hour+int64(hours-1)*3600]
		benchIn, benchOut = closes[benchSymbol][e.hour-3600], closes[benchSymbol][e.hour+int64(hours-1)*3600]
	}
	if entry <= 0 || exit <= 0 {
		return 0, 0, false, false
	}
	ret = sign * (exit/entry - 1)
	if e.symbol == benchSymbol || benchIn <= 0 || benchOut <= 0 {
		return ret, 0, true, false
	}
	return ret, ret - sign*(benchOut/benchIn-1), true, true
}

// followStats scores the collected opens. from and end bound the replay
// (unix s); the halves split it in two, to see whether a result holds twice.
func followStats(events []callEvent, closes map[string]map[int64]float64, bars map[string][]Bar, from, end int64) []CallRow {
	accs := map[string]*callAcc{}
	var order []string
	meta := map[string][3]any{}
	add := func(table, label string, hours int, e callEvent, atFill bool) {
		ret, excess, ok, benched := followOutcome(e, hours, atFill, closes, bars, end)
		if !ok {
			return
		}
		k := table + "|" + label + "|" + time.Duration(hours).String()
		a := accs[k]
		if a == nil {
			a = &callAcc{}
			accs[k] = a
			order = append(order, k)
			meta[k] = [3]any{table, label, hours}
		}
		a.n++
		if ret > 0 {
			a.hits++
		}
		if benched {
			a.en++
			a.esum += excess
			a.ex = append(a.ex, excess)
			if excess > 0 {
				a.ehits++
			}
		}
	}
	mid := from + (end-from)/2
	// The busiest trader of each style, to see what is left without them.
	perTrader := map[string]map[string]int{}
	for _, e := range events {
		if perTrader[e.kind] == nil {
			perTrader[e.kind] = map[string]int{}
		}
		perTrader[e.kind][e.trader]++
	}
	busiest := map[string]string{}
	for kind, m := range perTrader {
		for id, n := range m {
			if b := busiest[kind]; b == "" || n > m[b] || (n == m[b] && id < b) {
				busiest[kind] = id
			}
		}
	}
	for _, e := range events {
		for _, h := range []int{4, 24, 72} {
			add("style", e.kind, h, e, false)
		}
		add("entry", e.kind+" at the fill price", e.own, e, true)
		add("entry", e.kind+" an hour-close later", e.own, e, false)
		if e.kind != StyleScalp && e.kind != StyleSwing {
			continue
		}
		add("side", e.kind+" "+e.side, e.own, e, false)
		add("prior", e.kind+" "+e.prior, e.own, e, false)
		agree := "alone"
		if e.agree >= 2 {
			agree = "2+ traders"
		}
		add("agree", e.kind+" "+agree, e.own, e, false)
		// Placebo: the same coin and side three days away from the open. If
		// it scores like the open itself, the result is the coin's drift over
		// the period, not the trader's timing.
		for _, shift := range []struct {
			label string
			hours int64
		}{{"3 days earlier", -72}, {"3 days later", 72}} {
			p := e
			p.hour += shift.hours * 3600
			if p.hour-3600 >= from {
				add("placebo", e.kind+" "+shift.label, e.own, p, false)
			}
		}
		add("placebo", e.kind+" the open itself", e.own, e, false)
		if e.trader != busiest[e.kind] {
			add("placebo", e.kind+" the open, without the busiest trader", e.own, e, false)
		}
		add("trader:"+e.kind, e.trader, e.own, e, false)
		half := "first half"
		if e.hour > mid {
			half = "second half"
		}
		add("half", e.kind+" "+half, e.own, e, false)
	}
	sort.SliceStable(order, func(a, b int) bool {
		ma, mb := meta[order[a]], meta[order[b]]
		if ma[0] != mb[0] {
			return ma[0].(string) < mb[0].(string)
		}
		if ma[1] != mb[1] {
			return ma[1].(string) < mb[1].(string)
		}
		return ma[2].(int) < mb[2].(int)
	})
	out := make([]CallRow, 0, len(order))
	// Per-trader rows fold into one line per style: how many traders the
	// result rests on, and how many of them beat BTC more often than not.
	type spread struct{ traders, ahead, opens, top, hours int }
	spreads := map[string]*spread{}
	for _, k := range order {
		m := meta[k]
		table := m[0].(string)
		if kind, ok := strings.CutPrefix(table, "trader:"); ok {
			sp := spreads[kind]
			if sp == nil {
				sp = &spread{hours: m[2].(int)}
				spreads[kind] = sp
			}
			a := accs[k]
			sp.traders++
			sp.opens += a.en
			sp.top = max(sp.top, a.en)
			if a.en > 0 && a.ehits*2 > a.en {
				sp.ahead++
			}
			continue
		}
		out = append(out, accs[k].row(table, m[1].(string), m[2].(int)))
	}
	for _, kind := range []string{StyleScalp, StyleSwing} {
		if sp := spreads[kind]; sp != nil && sp.opens > 0 {
			out = append(out, CallRow{Table: "spread", Label: kind, Hours: sp.hours, Calls: sp.traders, Excess: sp.ahead,
				Hit: float64(sp.top) / float64(sp.opens)})
		}
	}
	return out
}

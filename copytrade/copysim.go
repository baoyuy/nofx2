package copytrade

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Direct copying: no score at all. Every position a lead trader opens is
// taken with the same stake and left when they leave it, at the prices a
// follower gets a few minutes later, with fees. It answers what mirroring
// the board would have returned, and which simple rules on top of it help.

// CopyInput is what a copy simulation replays.
type CopyInput struct {
	Fills map[string][]Fill
	// Names are the traders' nicknames, for the per-trader rows.
	Names map[string]string
	// Bars are closed candles of Step seconds per symbol, oldest first.
	Bars     map[string][]Bar
	Step     int64
	From, To time.Time
	// Fee is charged on the value of every fill of the follower (0.0005 =
	// the usual taker rate). Funding is not modelled.
	Fee float64
}

// CopyRow is the record of one group of copied positions.
type CopyRow struct {
	Table, Label string
	Traders      int
	Trips        int
	Win          float64 // share of positions that made money after fees
	Avg, Med     float64 // return per position, on the opening stake
	Total        float64 // sum of the returns: in stakes
	// Peak is the median of how many times its opening fill a position grew
	// to (1 = never added to).
	Peak float64
	// MaxOpen is the most positions open at once, and ROI the total over
	// it: the return on the capital needed to take every position.
	MaxOpen int
	ROI     float64
	// MaxDD is the deepest fall of the running total, in stakes.
	MaxDD float64
	// Against the same side of BTC over each position's life: the share
	// that beat it and the mean difference. A short in a falling market
	// wins on its own; this is what the trader added.
	Beat, AvgExcess float64
	// Skipped positions had no candle to price them with.
	Skipped int
}

type copyLeg struct {
	at    int64   // ms
	qty   float64 // signed: > 0 grows the position
	price float64 // the trader's own
}

// copyTrip is one position of one trader, from its open to its close (or
// still open at the end).
type copyTrip struct {
	trader, kind, symbol, side string
	openAt, closedAt           int64 // ms; closedAt 0 = still open
	legs                       []copyLeg
	// agree counts the traders (itself included) who opened the same coin
	// and side within agreeWindow before it.
	agree int
}

type copyResult struct {
	ret, peak    float64
	openAt, exit int64
	ok           bool
	// bench is what the same side of BTC returned from the open to the exit
	// (benched tells it is known). With adds the exposure varies, so for
	// mirrored positions it is an approximation.
	bench   float64
	benched bool
}

// withBench adds BTC's move over the position's life.
func (in CopyInput) withBench(r copyResult, t copyTrip, held bool) copyResult {
	if !r.ok || t.symbol == benchSymbol {
		return r
	}
	bars := in.Bars[benchSymbol]
	from := fineClose(bars, in.Step, r.openAt, 0)
	to := fineClose(bars, in.Step, r.exit, 0)
	if held || to <= 0 {
		to = lastClose(bars, in.Step, r.exit)
	}
	if from <= 0 || to <= 0 {
		return r
	}
	r.bench, r.benched = to/from-1, true
	if t.side == "short" {
		r.bench = -r.bench
	}
	return r
}

// fineClose is the close of the candle holding ms, extra candles later; 0
// when there is none.
func fineClose(bars []Bar, step, ms int64, extra int) float64 {
	sec := ms / 1000
	i := sort.Search(len(bars), func(i int) bool { return bars[i].Time > sec }) - 1
	if i < 0 || sec-bars[i].Time >= step {
		return 0
	}
	j := i + extra
	if j >= len(bars) || bars[j].Time != bars[i].Time+int64(extra)*step {
		return 0
	}
	return bars[j].Close
}

// lastClose is the newest close at or before ms.
func lastClose(bars []Bar, step, ms int64) float64 {
	sec := ms / 1000
	i := sort.Search(len(bars), func(i int) bool { return bars[i].Time+step > sec }) - 1
	if i < 0 {
		return 0
	}
	return bars[i].Close
}

// mirror copies every action of the trip in proportion, sized so that the
// position at its largest is one stake: the return is on the most capital
// the position ever tied up. Sizing by the opening fill instead would make a
// trader who probes with a small order and then adds a thousand times that
// decide the whole result. delay < 0 uses the trader's own prices (what they
// got, not a follower).
func (in CopyInput) mirror(t copyTrip, delay int) copyResult {
	bars := in.Bars[t.symbol]
	px := func(l copyLeg) float64 {
		if delay < 0 {
			return l.price
		}
		return fineClose(bars, in.Step, l.at, delay)
	}
	entry := px(t.legs[0])
	if entry <= 0 || t.legs[0].qty <= 0 {
		return copyResult{}
	}
	first := t.legs[0].qty * entry
	sign := 1.0
	if t.side == "short" {
		sign = -1
	}
	var pos, cash, traded, peak float64
	exit := t.openAt
	for _, l := range t.legs {
		p := px(l)
		if p <= 0 {
			return copyResult{}
		}
		q := l.qty
		if q < 0 && -q > pos {
			q = -pos // closes more than was seen opened
		}
		pos += q
		cash -= sign * q * p
		traded += math.Abs(q) * p
		peak = math.Max(peak, pos*p)
		exit = l.at
	}
	if peak <= 0 {
		return copyResult{}
	}
	if pos > 1e-12 {
		// Still held at the end: valued at the last price, exit fee included.
		p := lastClose(bars, in.Step, in.To.UnixMilli())
		if p <= 0 {
			return copyResult{}
		}
		cash += sign * pos * p
		traded += pos * p
		exit = in.To.UnixMilli()
	}
	// peak is reported against the opening fill: how far adds grew it.
	return in.withBench(copyResult{ret: (cash - in.Fee*traded) / peak, peak: peak / first, openAt: t.openAt, exit: exit, ok: true},
		t, pos > 1e-12)
}

// openOnly takes the open with one stake, ignores adds, and leaves when the
// trader has closed the position or after maxHold (0 = no limit).
func (in CopyInput) openOnly(t copyTrip, delay int, maxHold time.Duration) copyResult {
	bars := in.Bars[t.symbol]
	entry := fineClose(bars, in.Step, t.openAt, delay)
	if entry <= 0 {
		return copyResult{}
	}
	exitAt, held := t.closedAt, false
	if exitAt == 0 {
		exitAt, held = in.To.UnixMilli(), true
	}
	if limit := t.openAt + maxHold.Milliseconds(); maxHold > 0 && limit < exitAt {
		exitAt, held = limit, false
	}
	var out float64
	if held {
		out = lastClose(bars, in.Step, exitAt)
	} else {
		out = fineClose(bars, in.Step, exitAt, delay)
	}
	if out <= 0 {
		return copyResult{}
	}
	r := out/entry - 1
	if t.side == "short" {
		r = -r
	}
	return in.withBench(copyResult{ret: r - 2*in.Fee, peak: 1, openAt: t.openAt, exit: exitAt, ok: true}, t, held)
}

type copyAcc struct {
	res     []copyResult
	traders map[string]bool
	skipped int
}

func (a *copyAcc) row(table, label string) CopyRow {
	r := CopyRow{Table: table, Label: label, Trips: len(a.res), Traders: len(a.traders), Skipped: a.skipped}
	if len(a.res) == 0 {
		return r
	}
	rets := make([]float64, len(a.res))
	wins := 0
	for i, x := range a.res {
		rets[i] = x.ret
		r.Total += x.ret
		if x.ret > 0 {
			wins++
		}
	}
	n := float64(len(a.res))
	r.Win, r.Avg = float64(wins)/n, r.Total/n
	benched, beat := 0, 0
	for _, x := range a.res {
		if x.benched {
			benched++
			r.AvgExcess += x.ret - x.bench
			if x.ret > x.bench {
				beat++
			}
		}
	}
	if benched > 0 {
		r.Beat, r.AvgExcess = float64(beat)/float64(benched), r.AvgExcess/float64(benched)
	}
	peaks := make([]float64, len(a.res))
	for i, x := range a.res {
		peaks[i] = x.peak
	}
	sort.Float64s(peaks)
	r.Peak = peaks[len(peaks)/2]
	sort.Float64s(rets)
	r.Med = rets[len(rets)/2]
	if len(rets)%2 == 0 {
		r.Med = (rets[len(rets)/2-1] + r.Med) / 2
	}
	// Positions open at once, and the running total by exit time.
	type tick struct {
		at int64
		d  int
	}
	ticks := make([]tick, 0, 2*len(a.res))
	for _, x := range a.res {
		ticks = append(ticks, tick{x.openAt, 1}, tick{x.exit, -1})
	}
	sort.Slice(ticks, func(i, j int) bool {
		if ticks[i].at != ticks[j].at {
			return ticks[i].at < ticks[j].at
		}
		return ticks[i].d < ticks[j].d
	})
	open := 0
	for _, t := range ticks {
		open += t.d
		r.MaxOpen = max(r.MaxOpen, open)
	}
	if r.MaxOpen > 0 {
		r.ROI = r.Total / float64(r.MaxOpen)
	}
	byExit := append([]copyResult(nil), a.res...)
	sort.SliceStable(byExit, func(i, j int) bool { return byExit[i].exit < byExit[j].exit })
	var run, top float64
	for _, x := range byExit {
		run += x.ret
		top = math.Max(top, run)
		r.MaxDD = math.Max(r.MaxDD, top-run)
	}
	return r
}

// copyTrips rebuilds every trader's positions opened in the window.
func copyTrips(in CopyInput) []copyTrip {
	var trips []copyTrip
	ids := make([]string, 0, len(in.Fills))
	for id := range in.Fills {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	from, to := in.From.UnixMilli(), in.To.UnixMilli()
	for _, id := range ids {
		fills := in.Fills[id]
		_, events := Replay(fills) // oldest first
		if len(events) == 0 {
			continue
		}
		kind := StyleOf(fills).Kind
		if days := float64(events[len(events)-1].Time-events[0].Time) / 86400000; float64(len(events)) >= highFreqOrders*math.Max(1, days) {
			kind = "high_freq"
		}
		open := map[string]*copyTrip{}
		done := func(k string) {
			if t := open[k]; t != nil {
				trips = append(trips, *t)
				delete(open, k)
			}
		}
		for _, e := range events {
			if e.Untracked || e.Price <= 0 || e.Qty <= 0 || e.Time > to {
				continue
			}
			k := e.Symbol + "|" + e.Side
			switch e.Action {
			case "open":
				done(k)
				if e.Time >= from {
					open[k] = &copyTrip{trader: id, kind: kind, symbol: e.Symbol, side: e.Side, openAt: e.Time,
						legs: []copyLeg{{e.Time, e.Qty, e.Price}}}
				}
			case "add":
				if t := open[k]; t != nil {
					t.legs = append(t.legs, copyLeg{e.Time, e.Qty, e.Price})
				}
			case "reduce", "close":
				if t := open[k]; t != nil {
					t.legs = append(t.legs, copyLeg{e.Time, -e.Qty, e.Price})
					if e.Action == "close" {
						t.closedAt = e.Time
						done(k)
					}
				}
			}
		}
		keys := make([]string, 0, len(open))
		for k := range open {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			done(k)
		}
	}
	// Who else opened the same way lately.
	sort.SliceStable(trips, func(a, b int) bool { return trips[a].openAt < trips[b].openAt })
	recent := map[string]map[string]int64{}
	for i := range trips {
		t := &trips[i]
		k := t.symbol + "|" + t.side
		if recent[k] == nil {
			recent[k] = map[string]int64{}
		}
		recent[k][t.trader] = t.openAt
		for _, at := range recent[k] {
			if t.openAt-at <= agreeWindow.Milliseconds() {
				t.agree++
			}
		}
	}
	return trips
}

// CopyBacktest simulates copying every position opened between From and To.
func CopyBacktest(in CopyInput) []CopyRow {
	trips := copyTrips(in)
	accs := map[string]*copyAcc{}
	var order []string
	add := func(table, label string, t copyTrip, r copyResult) {
		k := table + "\x00" + label
		a := accs[k]
		if a == nil {
			a = &copyAcc{traders: map[string]bool{}}
			accs[k] = a
			order = append(order, k)
		}
		if !r.ok {
			a.skipped++
			return
		}
		a.res = append(a.res, r)
		a.traders[t.trader] = true
	}
	mid := in.From.UnixMilli() + (in.To.UnixMilli()-in.From.UnixMilli())/2
	// How each trader did in the first half, from positions closed in it.
	type record struct {
		n   int
		sum float64
	}
	early := map[string]*record{}
	main := make([]copyResult, len(trips))
	for i, t := range trips {
		main[i] = in.mirror(t, 0)
		if main[i].ok && t.closedAt > 0 && t.closedAt < mid {
			r := early[t.trader]
			if r == nil {
				r = &record{}
				early[t.trader] = r
			}
			r.n++
			r.sum += main[i].ret
		}
	}
	// Rank the traders with enough of a first half, best average first.
	var ranked []string
	for id, r := range early {
		if r.n >= 5 {
			ranked = append(ranked, id)
		}
	}
	sort.Slice(ranked, func(a, b int) bool {
		ra, rb := early[ranked[a]], early[ranked[b]]
		if x, y := ra.sum/float64(ra.n), rb.sum/float64(rb.n); x != y {
			return x > y
		}
		return ranked[a] < ranked[b]
	})
	group := map[string]string{}
	for i, id := range ranked {
		switch {
		case i < len(ranked)/3:
			group[id] = "1 best third of the first half"
		case i >= len(ranked)-len(ranked)/3:
			group[id] = "3 worst third of the first half"
		default:
			group[id] = "2 middle third of the first half"
		}
	}
	for i, t := range trips {
		m := main[i]
		add("how", "1 at the trader's own prices (not reachable)", t, in.mirror(t, -1))
		add("how", "2 every action, up to 5 min late", t, m)
		add("how", "3 every action, 5-10 min late", t, in.mirror(t, 1))
		add("how", "4 every action, 15-20 min late", t, in.mirror(t, 3))
		add("how", "5 open only, out when they close", t, in.openOnly(t, 0, 0))
		add("how", "6 open only, out at close or 4h", t, in.openOnly(t, 0, 4*time.Hour))
		add("how", "7 open only, out at close or 24h", t, in.openOnly(t, 0, 24*time.Hour))
		add("style", t.kind, t, m)
		add("style-open", t.kind, t, in.openOnly(t, 0, 0))
		add("side", t.side, t, m)
		agree := "alone"
		if t.agree >= 2 {
			agree = "2+ traders within 4h"
		}
		add("agree", agree, t, in.openOnly(t, 0, 0))
		if t.kind != StyleMartingale && t.kind != "high_freq" {
			add("agree-clean", agree, t, in.openOnly(t, 0, 0))
		}
		half := "first half"
		if t.openAt >= mid {
			half = "second half"
			if g := group[t.trader]; g != "" {
				add("select", g, t, m)
			} else {
				add("select", "4 too few positions in the first half", t, m)
			}
		}
		add("half", half, t, m)
		add("trader", t.trader, t, m)
	}
	sort.SliceStable(order, func(a, b int) bool {
		ta, _, _ := strings.Cut(order[a], "\x00")
		tb, _, _ := strings.Cut(order[b], "\x00")
		if ta != tb {
			return ta < tb
		}
		return order[a] < order[b]
	})
	out := make([]CopyRow, 0, len(order))
	for _, k := range order {
		table, label, _ := strings.Cut(k, "\x00")
		row := accs[k].row(table, label)
		if table == "trader" {
			if name := in.Names[label]; name != "" {
				row.Label = name
			}
		}
		out = append(out, row)
	}
	return out
}

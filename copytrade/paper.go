package copytrade

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"nofx/logger"
	"nofx/store"
)

// Paper copying records what following the board would have returned, without
// placing orders. A fixed roster is captured when recording starts; from then
// on every action of each roster member is recorded as one simulated fill at the
// mark price seen when we noticed it, with the copying delay kept. Positions are
// reconstructed and valued the same way as the backtest (copysim.go), so the
// live record is comparable to it.

const (
	// paperFee is the taker fee charged on every simulated fill's value.
	paperFee = 0.0005
	// paperReload is how often the roster is re-read as a safety net.
	paperReload = 5 * time.Minute
)

// ReloadPaperRoster loads the newest roster into memory for the recorder.
func (m *Monitor) ReloadPaperRoster() error {
	r, err := m.store.ActivePaperRoster()
	if err != nil {
		return err
	}
	m.paperMu.Lock()
	m.paperRoster = r
	m.paperMembers = map[string]bool{}
	m.paperCursor = map[string]int64{}
	if r != nil {
		for _, id := range strings.Split(r.Members, ",") {
			if id = strings.TrimSpace(id); id != "" {
				m.paperMembers[id] = true
			}
		}
	}
	m.paperMu.Unlock()
	return nil
}

// StartPaperRoster fixes the recording roster to the given traders (the current
// board follows, normally) and starts a new record from now. Recording later
// members' changes does not move the roster.
func (m *Monitor) StartPaperRoster(ids []string, note string, now time.Time) (*store.CopyTradePaperRoster, error) {
	clean := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id == "" || seen[id] {
			continue
		}
		seen[id] = true
		clean = append(clean, id)
	}
	sort.Strings(clean)
	r := &store.CopyTradePaperRoster{
		ID:        "roster-" + now.Format("20060102-150405"),
		StartedAt: now.UnixMilli(),
		Members:   strings.Join(clean, ","),
		Note:      note,
	}
	if err := m.store.SavePaperRoster(r); err != nil {
		return nil, err
	}
	if err := m.ReloadPaperRoster(); err != nil {
		return nil, err
	}
	logger.Infof("copy-trade paper: started roster %s with %d traders", r.ID, len(clean))
	return r, nil
}

func (m *Monitor) paperLoop(ctx context.Context) {
	t := time.NewTicker(paperReload)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := m.ReloadPaperRoster(); err != nil {
				logger.Warnf("copy-trade paper: reload roster: %v", err)
			}
		}
	}
}

// recordPaper records any new action of a roster member from its rebuilt
// snapshot. It runs while the caller holds m.mu (from refresh), and only stores
// actions at or after the roster start. Re-recording is idempotent.
func (m *Monitor) recordPaper(s *Snapshot) {
	m.paperMu.Lock()
	roster := m.paperRoster
	if roster == nil || !m.paperMembers[s.Follow.PortfolioID] {
		m.paperMu.Unlock()
		return
	}
	key := roster.ID + "\x00" + s.Follow.PortfolioID
	cursor, ok := m.paperCursor[key]
	m.paperMu.Unlock()
	if !ok {
		c, err := m.store.PaperCursor(roster.ID, s.Follow.PortfolioID)
		if err != nil {
			logger.Warnf("copy-trade paper: cursor %s: %v", s.Follow.PortfolioID, err)
			return
		}
		cursor = c
		m.paperMu.Lock()
		m.paperCursor[key] = cursor
		m.paperMu.Unlock()
	}

	marks := m.currentMarks()
	bench := marks[benchSymbol]
	style := s.Style.Kind
	nowMs := time.Now().UnixMilli()
	var rows []store.CopyTradePaperFill
	maxT := cursor
	// s.Events is newest first; walk oldest first so the cursor advances in order.
	for i := len(s.Events) - 1; i >= 0; i-- {
		e := s.Events[i]
		if e.Time < roster.StartedAt || e.Time < cursor {
			continue
		}
		row := store.CopyTradePaperFill{
			RosterID: roster.ID, PortfolioID: s.Follow.PortfolioID, Nickname: s.Follow.Nickname,
			Symbol: e.Symbol, Side: e.Side, Action: e.Action, TradeTime: e.Time,
			Qty: e.Qty, TraderPrice: e.Price, SeenAt: nowMs, SeenPrice: marks[e.Symbol],
			BenchPrice: bench, Style: style,
		}
		row.DelayMs = nowMs - e.Time
		if row.DelayMs < 0 {
			row.DelayMs = 0
		}
		row.Fee = paperFee * math.Abs(e.Qty) * e.Price
		rows = append(rows, row)
		if e.Time > maxT {
			maxT = e.Time
		}
	}
	if len(rows) == 0 {
		return
	}
	if _, err := m.store.AddPaperFills(rows); err != nil {
		logger.Warnf("copy-trade paper: record %s: %v", s.Follow.PortfolioID, err)
		return
	}
	m.paperMu.Lock()
	if maxT > m.paperCursor[key] {
		m.paperCursor[key] = maxT
	}
	m.paperMu.Unlock()
}

// PaperRow is one line of the simulated-following report.
type PaperRow struct {
	Table string `json:"table"`
	Label string `json:"label"`
	// Trips is finished or still-open copied positions; Traders their owners.
	Trips   int     `json:"trips"`
	Traders int     `json:"traders"`
	Win     float64 `json:"win"` // share of positions profitable after fees
	Avg     float64 `json:"avg"` // mean return per position, on the opening stake
	Med     float64 `json:"med"`
	Total   float64 `json:"total"` // sum of returns, in stakes
	Beat    float64 `json:"beat"`  // share that beat the same side of BTC
	AvgExcess float64 `json:"avg_excess"`
	MaxOpen int     `json:"max_open"`
	MaxDD   float64 `json:"max_dd"`
	ROI     float64 `json:"roi"`
	// AvgDelayMin is the mean copying delay of the positions' first fill.
	AvgDelayMin float64 `json:"avg_delay_min"`
}

type paperLeg struct {
	qty         float64 // signed: > 0 grows the position
	price       float64 // seen mark (fallback: trader price)
	traderPrice float64
	bench       float64
}

type paperTrip struct {
	trader, symbol, side, style string
	openAt, closedAt            int64
	delayMs                     int64
	legs                        []paperLeg
	agree                       int
}

type paperResult struct {
	ret     float64
	excess  float64
	benched bool
	openAt  int64
	exit    int64
	delay   float64
}

// PaperReport groups the recorded fills into the dimensions the experiment asks
// about: style, side, agreement, copying delay, and price basis.
func PaperReport(fills []store.CopyTradePaperFill) []PaperRow {
	trips := paperTrips(fills)
	accs := map[string]*paperAcc{}
	var order []string
	add := func(table, label string, t paperTrip, ret float64, ok bool) {
		k := table + "\x00" + label
		a := accs[k]
		if a == nil {
			a = &paperAcc{traders: map[string]bool{}}
			accs[k] = a
			order = append(order, k)
		}
		if !ok {
			return
		}
		res := paperResult{ret: ret, openAt: t.openAt, exit: t.exitTime(), delay: float64(t.delayMs) / 60000}
		if b, ok := benchReturn(t); ok {
			res.benched, res.excess = true, ret-b
		}
		a.res = append(a.res, res)
		a.traders[t.trader] = true
	}
	for _, t := range trips {
		main, ok := mirrorPaper(t, false)
		add("all", "every action", t, main, ok)
		add("style", t.style, t, main, ok)
		add("side", t.side, t, main, ok)
		agree := "alone"
		if t.agree >= 2 {
			agree = "2+ traders within 4h"
		}
		add("agree", agree, t, main, ok)
		add("delay", delayBucket(t.delayMs), t, main, ok)
		own, ook := mirrorPaper(t, true)
		add("price", "trader own prices (not reachable)", t, own, ook)
		add("price", "seen prices (copy)", t, main, ok)
	}
	sort.SliceStable(order, func(i, j int) bool {
		ti, _, _ := strings.Cut(order[i], "\x00")
		tj, _, _ := strings.Cut(order[j], "\x00")
		if ti != tj {
			return ti < tj
		}
		return order[i] < order[j]
	})
	out := make([]PaperRow, 0, len(order))
	for _, k := range order {
		table, label, _ := strings.Cut(k, "\x00")
		out = append(out, accs[k].row(table, label))
	}
	return out
}

func (t paperTrip) exitTime() int64 {
	if t.closedAt > 0 {
		return t.closedAt
	}
	return t.openAt
}

func seenPrice(f store.CopyTradePaperFill) float64 {
	if f.SeenPrice > 0 {
		return f.SeenPrice
	}
	return f.TraderPrice
}

func paperTrips(fills []store.CopyTradePaperFill) []paperTrip {
	open := map[string]*paperTrip{}
	var trips []paperTrip
	key := func(f store.CopyTradePaperFill) string { return f.PortfolioID + "|" + f.Symbol + "|" + f.Side }
	leg := func(f store.CopyTradePaperFill, signed float64) paperLeg {
		return paperLeg{qty: signed, price: seenPrice(f), traderPrice: f.TraderPrice, bench: f.BenchPrice}
	}
	for _, f := range fills {
		k := key(f)
		switch f.Action {
		case "open":
			if t := open[k]; t != nil {
				trips = append(trips, *t)
				delete(open, k)
			}
			t := &paperTrip{trader: f.PortfolioID, symbol: f.Symbol, side: f.Side, style: f.Style,
				openAt: f.TradeTime, delayMs: f.DelayMs}
			t.legs = append(t.legs, leg(f, f.Qty))
			open[k] = t
		case "add":
			if t := open[k]; t != nil {
				t.legs = append(t.legs, leg(f, f.Qty))
			}
		case "reduce":
			if t := open[k]; t != nil {
				t.legs = append(t.legs, leg(f, -f.Qty))
			}
		case "close":
			if t := open[k]; t != nil {
				t.legs = append(t.legs, leg(f, -f.Qty))
				t.closedAt = f.TradeTime
				trips = append(trips, *t)
				delete(open, k)
			}
		}
	}
	keys := make([]string, 0, len(open))
	for k := range open {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		trips = append(trips, *open[k])
	}
	// How many traders opened the same coin and side within agreeWindow.
	sort.SliceStable(trips, func(a, b int) bool { return trips[a].openAt < trips[b].openAt })
	recent := map[string]map[string]int64{}
	for i := range trips {
		t := &trips[i]
		k := t.symbol + "|" + t.side
		if recent[k] == nil {
			recent[k] = map[string]int64{}
		}
		recent[k][t.trader] = t.openAt
		n := 0
		for _, at := range recent[k] {
			if t.openAt-at <= agreeWindow.Milliseconds() {
				n++
			}
		}
		t.agree = n
	}
	return trips
}

// mirrorPaper values a trip proportionally, sized so the position at its
// largest is one stake. useTrader uses the trader's own prices (what they got,
// not a follower) to measure the cost of the delay.
func mirrorPaper(t paperTrip, useTrader bool) (float64, bool) {
	if len(t.legs) == 0 {
		return 0, false
	}
	px := func(l paperLeg) float64 {
		if useTrader {
			return l.traderPrice
		}
		return l.price
	}
	entry := px(t.legs[0])
	if entry <= 0 || t.legs[0].qty <= 0 {
		return 0, false
	}
	sign := 1.0
	if t.side == "short" {
		sign = -1
	}
	var pos, cash, traded, peak float64
	for _, l := range t.legs {
		p := px(l)
		if p <= 0 {
			return 0, false
		}
		q := l.qty
		if q < 0 && -q > pos {
			q = -pos
		}
		pos += q
		cash -= sign * q * p
		traded += math.Abs(q) * p
		if v := pos * p; v > peak {
			peak = v
		}
	}
	if peak <= 0 {
		return 0, false
	}
	if pos > 1e-12 {
		// Still open at the end: valued at the last seen price.
		p := px(t.legs[len(t.legs)-1])
		if p <= 0 {
			return 0, false
		}
		cash += sign * pos * p
		traded += pos * p
	}
	return (cash - paperFee*traded) / peak, true
}

func benchReturn(t paperTrip) (float64, bool) {
	if len(t.legs) == 0 {
		return 0, false
	}
	b0, b1 := t.legs[0].bench, t.legs[len(t.legs)-1].bench
	if b0 <= 0 || b1 <= 0 {
		return 0, false
	}
	r := b1/b0 - 1
	if t.side == "short" {
		r = -r
	}
	return r, true
}

func delayBucket(ms int64) string {
	switch min := ms / 60000; {
	case min < 5:
		return "under 5 min"
	case min < 15:
		return "5-15 min"
	case min < 60:
		return "15-60 min"
	default:
		return "over 60 min"
	}
}

type paperAcc struct {
	res     []paperResult
	traders map[string]bool
}

func (a *paperAcc) row(table, label string) PaperRow {
	r := PaperRow{Table: table, Label: label, Trips: len(a.res), Traders: len(a.traders)}
	if len(a.res) == 0 {
		return r
	}
	rets := make([]float64, len(a.res))
	wins := 0
	var delay float64
	for i, x := range a.res {
		rets[i] = x.ret
		r.Total += x.ret
		delay += x.delay
		if x.ret > 0 {
			wins++
		}
	}
	n := float64(len(a.res))
	r.Win, r.Avg, r.AvgDelayMin = float64(wins)/n, r.Total/n, delay/n
	benched, beat := 0, 0
	for _, x := range a.res {
		if x.benched {
			benched++
			r.AvgExcess += x.excess
			if x.ret > x.excess {
				beat++
			}
		}
	}
	if benched > 0 {
		r.Beat, r.AvgExcess = float64(beat)/float64(benched), r.AvgExcess/float64(benched)
	}
	sort.Float64s(rets)
	r.Med = rets[len(rets)/2]
	if len(rets)%2 == 0 {
		r.Med = (rets[len(rets)/2-1] + r.Med) / 2
	}
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
	byExit := append([]paperResult(nil), a.res...)
	sort.SliceStable(byExit, func(i, j int) bool { return byExit[i].exit < byExit[j].exit })
	var run, top float64
	for _, x := range byExit {
		run += x.ret
		top = math.Max(top, run)
		r.MaxDD = math.Max(r.MaxDD, top-run)
	}
	return r
}

package copytrade

import (
	"sort"
	"time"

	"nofx/store"
)

// BacktestInput is the stored history a backtest replays.
type BacktestInput struct {
	// Fills per portfolio, in any order: followed traders and dropped ones
	// whose fills were kept.
	Fills map[string][]Fill
	// Follows carries what is known of a portfolio (nickname, public twin).
	// Results on Binance (ROI, drawdown) and the margin balance are today's and
	// are not used: every trader scores the same and no balance is assumed, so
	// nothing read after the fact leaks in.
	Follows map[string]store.CopyTradeFollow
	// Bars are closed hourly candles per symbol, oldest first, benchSymbol
	// included.
	Bars     map[string][]Bar
	From, To time.Time
}

// BacktestResult is the record the replayed signals would have had.
type BacktestResult struct {
	Hours, Signals, Traders int
	Rates                   []HitRate
	// Styles is how each style's traders did on their own calls, as of To.
	Styles []StyleStat
	// Follow is what following each open would have returned (callfollow.go).
	Follow []CallRow
}

// backtestScore stands in for the trader score in a backtest.
const backtestScore = 50

// Backtest replays the fills hour by hour through the same code that reads
// the live market (Replay, StyleOf, SymbolsAt, hitRates) and scores each
// hour's signals 4h, 24h and 72h later, against BTC.
//
// Its limits: traders dropped before their fills were kept are missing, so
// the traders are those who lasted (results lean optimistic); positions are
// all inferred from fills, public ones included; and a trader's history
// starts with the stored fills, so early hours know little of style or
// accuracy. Use it to compare signal definitions, not to predict returns.
func Backtest(in BacktestInput) BacktestResult {
	type trader struct {
		id     string
		fills  []Fill
		lo, hi int
		snap   Snapshot
		calls  []call
		raw    []Position
		built  bool
	}
	var traders []*trader
	for id, fills := range in.Fills {
		sorted := append([]Fill(nil), fills...)
		sort.SliceStable(sorted, func(a, b int) bool {
			if sorted[a].Time != sorted[b].Time {
				return sorted[a].Time < sorted[b].Time
			}
			return sorted[a].Seq > sorted[b].Seq
		})
		traders = append(traders, &trader{id: id, fills: sorted})
	}
	sort.Slice(traders, func(a, b int) bool { return traders[a].id < traders[b].id })
	closes := make(map[string]map[int64]float64, len(in.Bars))
	for symbol, bars := range in.Bars {
		m := make(map[int64]float64, len(bars))
		for _, b := range bars {
			m[b.Time] = b.Close
		}
		closes[symbol] = m
	}
	res := BacktestResult{Traders: len(traders)}
	follow := newCallCollector()
	var sigs []store.CopyTradeSignal
	end := in.To.Unix()
	for at := in.From.Truncate(time.Hour); !at.After(in.To); at = at.Add(time.Hour) {
		hour := at.Unix()
		// What was known at the hour: the close of the candle just ended.
		marks := map[string]float64{}
		for symbol, m := range closes {
			if c := m[hour-3600]; c > 0 {
				marks[symbol] = c
			}
		}
		snaps := make([]Snapshot, 0, len(traders))
		var opens []rawOpen
		for _, t := range traders {
			ms, since := at.UnixMilli(), at.Add(-retention).UnixMilli()
			hi := sort.Search(len(t.fills), func(i int) bool { return t.fills[i].Time > ms })
			lo := sort.Search(len(t.fills), func(i int) bool { return t.fills[i].Time >= since })
			if hi <= lo {
				continue
			}
			if !t.built || hi != t.hi || lo != t.lo {
				t.built, t.hi, t.lo = true, hi, lo
				window := t.fills[lo:hi]
				positions, events := Replay(window)
				t.calls = callsOf(events)
				for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
					events[i], events[j] = events[j], events[i]
				}
				f := in.Follows[t.id]
				// MarginBalance is deliberately left at 0. The stored follow
				// holds TODAY's balance; using it for every replayed hour was
				// look-ahead (and a level mismatch for anyone whose balance
				// changed), feeding both the stake weight and the inferred
				// liquidation test. With no balance, stakeFactor returns its
				// neutral 0.5 and the liquidation test is skipped, the same way
				// the trader score is neutralised to backtestScore.
				t.snap = Snapshot{
					Follow: store.CopyTradeFollow{PortfolioID: t.id, Nickname: f.Nickname, PublicID: f.PublicID},
					Events: events, Style: StyleOf(window), HistoryComplete: true, PositionsSource: "inferred",
					Score: TraderScore{Score: backtestScore},
				}
				t.raw = positions
			}
			s := t.snap
			s.OrdersPerDay = ordersSince(s.Events, at.Add(-24*time.Hour).UnixMilli())
			s.HighFreq = s.OrdersPerDay >= highFreqOrders
			s.Positions = assessHealth(t.raw, marks, s.Follow.MarginBalance, at)
			s.Accuracy = scoreCalls(t.calls, in.Bars, at)
			s.StyleAccuracy = scoreCallsOver(t.calls, in.Bars, at, styleHorizon(horizonKind(s)))
			s.AccuracyBy = map[string]Accuracy{}
			by := map[string][]call{}
			for _, c := range t.calls {
				by[ClassOf(c.symbol)] = append(by[ClassOf(c.symbol)], c)
			}
			for class, list := range by {
				s.AccuracyBy[class] = scoreCalls(list, in.Bars, at)
			}
			snaps = append(snaps, s)
			// This hour's opens, with what is known of the trader now.
			for _, e := range s.Events {
				if e.Time <= at.Add(-time.Hour).UnixMilli() {
					break
				}
				if e.Action != "open" || e.Price <= 0 {
					continue
				}
				opens = append(opens, rawOpen{trader: t.id, votes: styleVotes(s), callEvent: callEvent{
					kind: styleKindOf(s), symbol: e.Symbol, side: e.Side, at: e.Time, hour: hour, price: e.Price,
					own: int(styleHorizon(horizonKind(s)).Hours()), prior: priorBand(s.StyleAccuracy)}})
			}
		}
		follow.add(opens)
		coins := SymbolsAt(snaps, at)
		sort.SliceStable(coins, func(a, b int) bool { return coins[a].Confidence.Score > coins[b].Confidence.Score })
		// The per-style reading keeps its own hourly top, as Confidence does,
		// so the two records are of the same size.
		styles := StyleReadings(snaps, at)
		styleTop := map[string]bool{}
		{
			order := make([]string, 0, len(styles))
			for symbol, r := range styles {
				if r.Direction != "neutral" && r.Score > 0 && marks[symbol] > 0 {
					order = append(order, symbol)
				}
			}
			sort.Slice(order, func(a, b int) bool {
				if styles[order[a]].Score != styles[order[b]].Score {
					return styles[order[a]].Score > styles[order[b]].Score
				}
				return order[a] < order[b]
			})
			for _, symbol := range order[:min(len(order), maxSignalsPerHour)] {
				styleTop[symbol] = true
			}
		}
		ranked := 0
		for _, c := range coins {
			called := c.Confidence.Direction != "neutral" && c.Confidence.Score > 0
			shadowed := c.Shadow.Direction != "neutral" && c.Shadow.Score > 0
			price := marks[c.Symbol]
			if (!(called && ranked < maxSignalsPerHour) && !shadowed && !styleTop[c.Symbol]) || price <= 0 {
				continue
			}
			row := SignalRow(c, hour, price)
			if called && ranked < maxSignalsPerHour {
				ranked++
				row.Ranked = true
			}
			if styleTop[c.Symbol] {
				r := styles[c.Symbol]
				row.StyleDirection, row.StyleScore, row.StyleTraders = r.Direction, r.Score, r.Traders
			}
			for _, h := range []struct {
				hours      int64
				ret, bench **float64
			}{{4, &row.Ret4h, &row.Bench4h}, {24, &row.Ret24h, &row.Bench24h}, {72, &row.Ret72h, &row.Bench72h}} {
				if hour+h.hours*3600 > end {
					continue
				}
				if c := closes[row.Symbol][hour+(h.hours-1)*3600]; c > 0 {
					r := c/price - 1
					*h.ret = &r
				}
				if from, to := closes[benchSymbol][hour-3600], closes[benchSymbol][hour+(h.hours-1)*3600]; from > 0 && to > 0 {
					r := to/from - 1
					*h.bench = &r
				}
			}
			sigs = append(sigs, row)
		}
		res.Hours++
	}
	res.Signals = len(sigs)
	res.Follow = followStats(follow.events, closes, in.Bars, in.From.Truncate(time.Hour).Unix(), end)
	res.Rates = hitRates(sigs, 0)
	// Each style's own record, from everything known at the end.
	stats := map[string]*StyleStat{}
	holds := map[string][]float64{}
	for _, t := range traders {
		if !t.built {
			continue
		}
		s := t.snap
		s.OrdersPerDay = ordersSince(s.Events, in.To.Add(-24*time.Hour).UnixMilli())
		s.HighFreq = s.OrdersPerDay >= highFreqOrders
		kind := styleKindOf(s)
		st := stats[kind]
		if st == nil {
			st = &StyleStat{Kind: kind, OwnHours: int(styleHorizon(horizonKind(s)).Hours())}
			stats[kind] = st
		}
		st.Traders++
		st.Own = mergeAccuracy(st.Own, scoreCallsOver(t.calls, in.Bars, in.To, styleHorizon(horizonKind(s))))
		st.Day = mergeAccuracy(st.Day, scoreCalls(t.calls, in.Bars, in.To))
		if s.Style.MedianHoldHours > 0 {
			holds[kind] = append(holds[kind], s.Style.MedianHoldHours)
		}
	}
	for kind, st := range stats {
		if h := holds[kind]; len(h) > 0 {
			sort.Float64s(h)
			st.MedianHold = h[len(h)/2]
		}
		res.Styles = append(res.Styles, *st)
	}
	sort.Slice(res.Styles, func(a, b int) bool { return res.Styles[a].Kind < res.Styles[b].Kind })
	return res
}

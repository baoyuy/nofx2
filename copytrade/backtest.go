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
	// Follows carries what is known of a portfolio (balance, public twin).
	// Results on Binance (ROI, drawdown) are today's and are not used: every
	// trader scores the same, so nothing read after the fact leaks in.
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
				t.snap = Snapshot{
					Follow: store.CopyTradeFollow{PortfolioID: t.id, Nickname: f.Nickname, PublicID: f.PublicID,
						MarginBalance: f.MarginBalance},
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
			s.AccuracyBy = map[string]Accuracy{}
			by := map[string][]call{}
			for _, c := range t.calls {
				by[ClassOf(c.symbol)] = append(by[ClassOf(c.symbol)], c)
			}
			for class, list := range by {
				s.AccuracyBy[class] = scoreCalls(list, in.Bars, at)
			}
			snaps = append(snaps, s)
		}
		coins := SymbolsAt(snaps, at)
		sort.SliceStable(coins, func(a, b int) bool { return coins[a].Confidence.Score > coins[b].Confidence.Score })
		ranked := 0
		for _, c := range coins {
			called := c.Confidence.Direction != "neutral" && c.Confidence.Score > 0
			shadowed := c.Shadow.Direction != "neutral" && c.Shadow.Score > 0
			price := marks[c.Symbol]
			if (!(called && ranked < maxSignalsPerHour) && !shadowed) || price <= 0 {
				continue
			}
			row := SignalRow(c, hour, price)
			if called && ranked < maxSignalsPerHour {
				ranked++
				row.Ranked = true
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
	res.Rates = hitRates(sigs, 0)
	return res
}

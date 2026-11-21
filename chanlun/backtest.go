package chanlun

import "math"

// Funding is one perpetual funding settlement; longs pay Rate on notional.
type Funding struct {
	Time int64
	Rate float64
}

// Config controls the simulated execution. Signals are known at a bar's close
// and filled at the next bar's open.
type Config struct {
	FeeRate  float64 // per side, on notional
	Slippage float64 // per fill, against the trade
	LongOnly bool
	Trail    bool            // tighten the stop to newer structure: same-side signals and confirmed segment ends
	Align    bool            // take 3rd points only along the running higher-level move
	Kinds    map[string]bool // enabled signal kinds; nil enables all
	Funding  []Funding       // sorted by time; empty ignores funding
}

type Trade struct {
	Kind                string
	Long                bool
	EntryIdx, ExitIdx   int
	EntryTime, ExitTime int64
	Entry, Exit         float64
	Return              float64 // net change in equity over the trade
	Reason              string  // stop | signal | end
}

type Result struct {
	Trades   []Trade
	Equity   []float64 // marked to each bar's close, starts near 1
	Signals  map[string]int
	Points   int
	Segments int
	Big      int
	Pivots   int
}

// Run replays bars through the engine with one full-equity position at a time.
func Run(bars []Bar, cfg Config) Result {
	type order struct {
		closeFirst bool
		side       int
		stop       float64
		kind       string
	}
	eng := New()
	res := Result{Signals: map[string]int{}, Equity: make([]float64, 0, len(bars))}
	equity := 1.0
	var (
		side     int
		entry    float64
		stop     float64
		kind     string
		entryIdx int
		entryEq  float64
		pend     *order
		fi       int
	)
	closeAt := func(i int, px float64, reason string) {
		fill := px * (1 - float64(side)*cfg.Slippage)
		equity *= 1 + float64(side)*(fill/entry-1)
		equity *= 1 - cfg.FeeRate
		res.Trades = append(res.Trades, Trade{
			Kind: kind, Long: side > 0, EntryIdx: entryIdx, ExitIdx: i,
			EntryTime: bars[entryIdx].Time, ExitTime: bars[i].Time,
			Entry: entry, Exit: fill, Return: equity/entryEq - 1, Reason: reason,
		})
		side = 0
	}

	for i, b := range bars {
		if pend != nil {
			if side != 0 && pend.closeFirst {
				closeAt(i, b.Open, "signal")
			}
			// An open already through the stop means the signal is void.
			if pend.side != 0 && side == 0 && float64(pend.side)*(b.Open-pend.stop) > 0 {
				side, stop, kind, entryIdx, entryEq = pend.side, pend.stop, pend.kind, i, equity
				entry = b.Open * (1 + float64(side)*cfg.Slippage)
				equity *= 1 - cfg.FeeRate
			}
			pend = nil
		}
		// Funding settled inside this bar is charged on the open position.
		for ; fi < len(cfg.Funding) && (i+1 == len(bars) || cfg.Funding[fi].Time < bars[i+1].Time); fi++ {
			if f := cfg.Funding[fi]; side != 0 && f.Time >= b.Time {
				equity *= 1 - float64(side)*f.Rate
			}
		}
		// A gap through the stop fills at the open, not at the stop.
		if side > 0 && b.Low <= stop {
			closeAt(i, math.Min(stop, b.Open), "stop")
		} else if side < 0 && b.High >= stop {
			closeAt(i, math.Max(stop, b.Open), "stop")
		}

		ev := eng.Push(b)
		// tighten moves the stop toward price, never away and never past it.
		tighten := func(level float64) {
			if d := float64(side); d*(level-stop) > 0 && d*(b.Close-level) > 0 {
				stop = level
			}
		}
		if p := ev.Seg; p != nil && cfg.Trail && side != 0 && p.Top == (side < 0) {
			tighten(p.Price)
		}
		if s := ev.Signal; s != nil {
			res.Signals[s.Kind]++
			dir := 1
			if !s.Long {
				dir = -1
			}
			ok := cfg.Kinds == nil || cfg.Kinds[s.Kind]
			if cfg.Align && s.Kind[1] == '3' && s.Big != dir {
				ok = false
			}
			switch {
			case !ok:
			case side == dir:
				if cfg.Trail {
					tighten(s.Stop)
				}
			default:
				pend = &order{closeFirst: side != 0, stop: s.Stop, kind: s.Kind}
				if dir > 0 || !cfg.LongOnly {
					pend.side = dir
				}
			}
		}

		mark := equity
		if side != 0 {
			mark *= 1 + float64(side)*(b.Close/entry-1)
		}
		res.Equity = append(res.Equity, mark)
	}
	if side != 0 {
		last := len(bars) - 1
		closeAt(last, bars[last].Close, "end")
		res.Equity[last] = equity
	}
	res.Points = len(eng.Points())
	res.Segments = len(eng.Segments())
	res.Big = eng.BigSegments()
	res.Pivots = len(eng.Pivots())
	return res
}

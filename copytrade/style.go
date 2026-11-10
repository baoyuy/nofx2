package copytrade

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Style kinds of a lead trader.
const (
	// StyleScalp holds positions for less than scalpMaxHold.
	StyleScalp = "scalp"
	// StyleSwing holds them for scalpMaxHold to longMinHold.
	StyleSwing = "swing"
	// StyleLong holds them for longer than longMinHold.
	StyleLong = "long"
	// StyleMartingale keeps adding to losing positions: its size says little
	// about conviction, so it votes martingaleWeight.
	StyleMartingale = "martingale"
	// StyleFewSamples has too few completed trades to tell.
	StyleFewSamples = "few_samples"
	// StyleUnknown is a portfolio whose trades Binance does not show.
	StyleUnknown = "unknown"
)

const (
	scalpMaxHold   = 4 * time.Hour
	longMinHold    = 72 * time.Hour
	minStyleTrades = 5

	// martingaleAddsPerTrade and martingaleAdverseShare are the bar for
	// StyleMartingale: on average at least one add per trade, and over half of
	// those adds made while underwater. How the trades end does not matter: a
	// trader who averages down and waits to break even mostly exits green.
	martingaleAddsPerTrade = 1.0
	martingaleAdverseShare = 0.5

	// martingaleWeight scales a martingale's vote. Adding to a losing position
	// makes the size look like conviction when it is the opposite.
	martingaleWeight = 0.3
)

// Style is how a lead trader trades, measured from the fills kept for them.
type Style struct {
	Kind string `json:"kind"`
	// Trades is how many completed round trips the measures come from.
	Trades int `json:"trades"`
	// MedianHoldHours is the median time from open to close; the median since
	// one position held for months would otherwise decide alone.
	MedianHoldHours float64 `json:"median_hold_hours"`
	// AddsPerTrade counts adds per round trip.
	AddsPerTrade float64 `json:"adds_per_trade"`
	// AdverseAddShare is the share of adds made while the position was losing.
	AdverseAddShare float64 `json:"adverse_add_share"`
	// LossExitShare is the share of round trips closed at a loss.
	LossExitShare float64 `json:"loss_exit_share"`
}

// Martingale tells whether the trader keeps adding to losing positions.
func (s Style) Martingale() bool { return s.Kind == StyleMartingale }

type styleBook struct {
	q        float64 // signed: long > 0, short < 0
	cost     float64
	openedAt int64
	adds     int
	adverse  int
	realized float64
	closes   int
}

func share(part, whole int) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

// holdHours turns the gap between two fill times (ms) into hours.
func holdHours(from, to int64) float64 { return float64(to-from) / float64(time.Hour/time.Millisecond) }

// StyleOf classifies a trader from fills. The first round trip of every symbol
// is left out: it may have been opened before the stored fills begin, which
// would make its holding time read too short.
func StyleOf(fills []Fill) Style {
	sorted := append([]Fill(nil), fills...)
	sort.SliceStable(sorted, func(a, b int) bool {
		if sorted[a].Time != sorted[b].Time {
			return sorted[a].Time < sorted[b].Time
		}
		return sorted[a].Seq > sorted[b].Seq
	})
	books := map[string]*styleBook{}
	var holds []float64
	trips, lossTrips, adds, adverseAdds := 0, 0, 0, 0
	for _, f := range sorted {
		if f.Qty <= 0 || f.Price <= 0 {
			continue
		}
		mode := strings.ToUpper(f.PositionSide)
		if mode != "LONG" && mode != "SHORT" {
			mode = "BOTH"
		}
		key := f.Symbol + "|" + mode
		b := books[key]
		if b == nil {
			b = &styleBook{}
			books[key] = b
		}
		delta := f.Qty
		if strings.ToUpper(f.Side) == "SELL" {
			delta = -f.Qty
		}
		b.realized += f.RealizedPnL
		opensSide := map[string]float64{"LONG": 1, "SHORT": -1}[mode]
		switch {
		case b.q == 0 && (opensSide*delta < 0 || (mode == "BOTH" && f.RealizedPnL != 0)):
			// Closes a position opened before the stored fills: no trade of ours.
		case b.q == 0 || (b.q > 0) == (delta > 0):
			if b.q == 0 {
				b.openedAt, b.adds, b.adverse, b.realized = f.Time, 0, 0, f.RealizedPnL
			} else {
				b.adds++
				avg := b.cost / math.Abs(b.q)
				if (delta > 0 && f.Price < avg) || (delta < 0 && f.Price > avg) {
					b.adverse++
				}
			}
			b.q += delta
			b.cost += f.Qty * f.Price
		default:
			held := math.Abs(b.q)
			if f.Qty < held && !near0(held-f.Qty, held) {
				b.cost *= (held - f.Qty) / held
				b.q += delta
				continue
			}
			first := b.closes == 0
			b.closes++
			if !first && b.openedAt > 0 {
				trips++
				if b.realized < 0 {
					lossTrips++
				}
				adds += b.adds
				adverseAdds += b.adverse
				holds = append(holds, holdHours(b.openedAt, f.Time))
			}
			excess := f.Qty - held
			b.q, b.cost, b.adds, b.adverse, b.realized = 0, 0, 0, 0, 0
			if mode == "BOTH" && excess > 0 && !near0(excess, held) {
				// The rest of the fill opened the other side.
				b.q = excess
				if delta < 0 {
					b.q = -excess
				}
				b.cost, b.openedAt = excess*f.Price, f.Time
			}
		}
	}
	style := Style{Kind: StyleFewSamples, Trades: trips}
	if trips == 0 {
		return style
	}
	sort.Float64s(holds)
	median := holds[len(holds)/2]
	if len(holds)%2 == 0 {
		median = (holds[len(holds)/2-1] + holds[len(holds)/2]) / 2
	}
	style.MedianHoldHours = median
	style.AddsPerTrade = share(adds, trips)
	style.AdverseAddShare = share(adverseAdds, adds)
	style.LossExitShare = share(lossTrips, trips)
	switch {
	case trips < minStyleTrades:
		style.Kind = StyleFewSamples
	case style.AddsPerTrade >= martingaleAddsPerTrade && style.AdverseAddShare >= martingaleAdverseShare:
		style.Kind = StyleMartingale
	case median < scalpMaxHold.Hours():
		style.Kind = StyleScalp
	case median < longMinHold.Hours():
		style.Kind = StyleSwing
	default:
		style.Kind = StyleLong
	}
	return style
}

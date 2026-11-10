package copytrade

import (
	"math"
	"sort"
	"strings"
)

// Fill is one stored trade. Seq is its index in Binance's newest-first list
// at fetch time; fills with the same Time are replayed by descending Seq.
type Fill struct {
	Time         int64
	Seq          int
	Symbol       string
	Side         string // BUY | SELL
	PositionSide string // LONG | SHORT | BOTH
	Qty          float64
	Price        float64
	Fee          float64
	RealizedPnL  float64
}

// Position is a held position, read from Binance (Source "public") or
// rebuilt from fills (Source "inferred").
type Position struct {
	Symbol     string  `json:"symbol"`
	Side       string  `json:"side"` // long | short
	Qty        float64 `json:"qty"`
	EntryPrice float64 `json:"entry_price"`
	// EntryValue is qty × entry price: the money put to work at entry.
	EntryValue float64 `json:"entry_value"`
	// FirstEntry is the price of the first fill seen for the position: the
	// real entry when OpenedAt is known, else the earliest evidence of it.
	FirstEntry float64 `json:"first_entry,omitempty"`
	// FirstValue is FirstEntry × the quantity of that first fill: the size the
	// trader committed when the position was opened, before any adds. Adding
	// must not make a position look like a bigger conviction.
	FirstValue float64 `json:"first_value,omitempty"`
	// PreExisting marks a position opened before the stored fills begin: its
	// quantity, average and holding time are only partly known then.
	PreExisting bool `json:"pre_existing,omitempty"`
	// OpenedAt is the first fill of the position in ms; 0 if it opened
	// before monitoring started.
	OpenedAt int64 `json:"opened_at"`
	// LastAddAt is the latest open or add in ms; 0 if not seen.
	LastAddAt int64  `json:"last_add_at"`
	Source    string `json:"source"`
	// Leverage and Margin are known for public positions only; MarkPrice and
	// UnrealizedPnL are Binance's for public ones, estimated from the latest
	// mark price for inferred ones.
	MarkPrice     float64 `json:"mark_price,omitempty"`
	Leverage      float64 `json:"leverage,omitempty"`
	Margin        float64 `json:"margin,omitempty"`
	UnrealizedPnL float64 `json:"unrealized_pnl,omitempty"`
	Health
}

// Event is one trading action: the fills of one order merged.
type Event struct {
	Time        int64   `json:"time"`
	Symbol      string  `json:"symbol"`
	Side        string  `json:"side"`   // long | short
	Action      string  `json:"action"` // open | add | reduce | close
	Qty         float64 `json:"qty"`
	Price       float64 `json:"price"`
	Value       float64 `json:"value"`
	RealizedPnL float64 `json:"realized_pnl"`
	Fee         float64 `json:"fee"`
	// PositionQty is the position size after the action.
	PositionQty float64 `json:"position_qty"`
	// Untracked marks reductions of a position opened before monitoring.
	Untracked bool `json:"untracked,omitempty"`

	lastTime int64
}

// mergeWindowMs merges fills of one order that Binance reports a few
// seconds apart.
const mergeWindowMs = 3000

type bucket struct {
	q        float64 // signed: long > 0, short < 0
	cost     float64 // |q| × average entry
	openedAt int64
	addedAt  int64
	// first is the price of the first fill of the book and firstQty its size,
	// pre whether the book was already open before the stored fills.
	first    float64
	firstQty float64
	pre      bool
}

func sideOf(q float64) string {
	if q < 0 {
		return "short"
	}
	return "long"
}

func near0(q, ref float64) bool { return math.Abs(q) <= 1e-9+1e-7*math.Abs(ref) }

// Replay rebuilds held positions and actions from fills. Fills may come in
// any order. Positions opened before the first fill are not visible; their
// reductions are reported as Untracked.
func Replay(fills []Fill) ([]Position, []Event) { return ReplayKnown(fills, nil) }

// PreKey names the side a fill or position belongs to, as ReplayKnown wants.
func PreKey(symbol, side string) string { return strings.ToUpper(symbol) + "|" + side }

// ReplayKnown is Replay for books held before the first stored fill: pre
// carries their symbol|side keys (see PreKey), so their first same-direction
// fill is an add to a position of unknown size, not a new open.
func ReplayKnown(fills []Fill, pre map[string]bool) ([]Position, []Event) {
	sorted := append([]Fill(nil), fills...)
	sort.SliceStable(sorted, func(a, b int) bool {
		if sorted[a].Time != sorted[b].Time {
			return sorted[a].Time < sorted[b].Time
		}
		return sorted[a].Seq > sorted[b].Seq
	})
	books := map[string]*bucket{}
	var events []Event
	lastIdx := map[string]int{}
	emit := func(key string, e Event) {
		e.lastTime = e.Time
		if i, ok := lastIdx[key]; ok {
			prev := &events[i]
			grow := (prev.Action == "open" || prev.Action == "add") && e.Action == "add"
			shrink := prev.Action == "reduce" && (e.Action == "reduce" || e.Action == "close")
			if (grow || shrink) && prev.Side == e.Side && prev.Untracked == e.Untracked && e.Time-prev.lastTime <= mergeWindowMs {
				prev.Qty += e.Qty
				prev.Value += e.Value
				prev.Price = prev.Value / prev.Qty
				prev.RealizedPnL += e.RealizedPnL
				prev.Fee += e.Fee
				prev.PositionQty = e.PositionQty
				prev.lastTime = e.Time
				if shrink {
					prev.Action = e.Action
				}
				return
			}
		}
		lastIdx[key] = len(events)
		events = append(events, e)
	}

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
			b = &bucket{}
			books[key] = b
			b.first, b.firstQty = f.Price, f.Qty
		}
		delta := f.Qty
		if strings.ToUpper(f.Side) == "SELL" {
			delta = -f.Qty
		}
		base := Event{Time: f.Time, Symbol: f.Symbol, Qty: f.Qty, Price: f.Price, Value: f.Qty * f.Price,
			RealizedPnL: f.RealizedPnL, Fee: f.Fee}
		// In hedge mode the bucket fixes the side; a fill against it reduces.
		opensSide := map[string]float64{"LONG": 1, "SHORT": -1}[mode]

		switch {
		case b.q == 0 && (opensSide*delta < 0 || (mode == "BOTH" && f.RealizedPnL != 0)):
			// Reduces a position opened before monitoring: report, keep flat.
			base.Side = sideOf(-delta)
			base.Action = "reduce"
			base.Untracked = true
			emit(key, base)
		case b.q == 0 || (b.q > 0) == (delta > 0):
			action := "add"
			if b.q != 0 && !b.pre && f.Time-b.openedAt <= mergeWindowMs {
				// Another fill of the opening order.
				b.firstQty += f.Qty
			}
			if b.q == 0 {
				// Every new round trip starts its own opening size, not only
				// the first one of the book.
				action, b.openedAt, b.first, b.firstQty = "open", f.Time, f.Price, f.Qty
				// Already held before the stored fills: this fill adds to a
				// position whose open time and size we never saw.
				if pre[PreKey(f.Symbol, sideOf(delta))] {
					action, b.openedAt, b.pre = "add", 0, true
				}
			}
			b.q += delta
			b.cost += f.Qty * f.Price
			b.addedAt = f.Time
			base.Side, base.Action, base.PositionQty = sideOf(b.q), action, math.Abs(b.q)
			emit(key, base)
		default:
			held := math.Abs(b.q)
			side := sideOf(b.q)
			if f.Qty < held && !near0(held-f.Qty, held) {
				b.cost *= (held - f.Qty) / held
				b.q += delta
				base.Side, base.Action, base.PositionQty = side, "reduce", math.Abs(b.q)
				emit(key, base)
				continue
			}
			excess := f.Qty - held
			entry := b.cost / held
			*b = bucket{}
			closeEv := base
			closeEv.Side, closeEv.Action = side, "close"
			if mode != "BOTH" || near0(excess, held) || !isFlip(side, entry, f.Price, held, f.RealizedPnL) {
				// Hedge buckets cannot flip; a larger close means the position
				// was bigger than what monitoring saw.
				emit(key, closeEv)
				continue
			}
			closeEv.Qty, closeEv.Value = held, held*f.Price
			closeEv.Fee = f.Fee * held / f.Qty
			emit(key, closeEv)
			b.q = excess
			if delta < 0 {
				b.q = -excess
			}
			b.cost, b.openedAt, b.addedAt = excess*f.Price, f.Time, f.Time
			emit(key, Event{Time: f.Time, Symbol: f.Symbol, Side: sideOf(b.q), Action: "open", Qty: excess,
				Price: f.Price, Value: excess * f.Price, Fee: f.Fee * excess / f.Qty, PositionQty: excess})
			b.first, b.firstQty = f.Price, excess
		}
	}

	var positions []Position
	for key, b := range books {
		if near0(b.q, b.q) || b.q == 0 {
			continue
		}
		qty := math.Abs(b.q)
		positions = append(positions, Position{Symbol: strings.SplitN(key, "|", 2)[0], Side: sideOf(b.q), Qty: qty,
			EntryPrice: b.cost / qty, EntryValue: b.cost, OpenedAt: b.openedAt, LastAddAt: b.addedAt, Source: "inferred",
			FirstEntry: b.first, FirstValue: b.first * b.firstQty, PreExisting: b.pre})
	}
	sort.Slice(positions, func(a, b int) bool { return positions[a].EntryValue > positions[b].EntryValue })
	return positions, events
}

// isFlip tells whether realized PnL matches closing exactly the held size,
// so the rest of the fill opened the other side. Otherwise the position was
// larger than monitoring saw.
func isFlip(side string, entry, price, held, realized float64) bool {
	expected := (price - entry) * held
	if side == "short" {
		expected = -expected
	}
	tolerance := math.Max(0.5, math.Max(0.05*math.Abs(expected), 0.0005*held*price))
	return math.Abs(realized-expected) <= tolerance
}

// LivePositions converts Binance's public positions, taking each position's
// opening time from the rebuilt one when they match.
func LivePositions(live []LivePosition, inferred []Position) []Position {
	seen := map[string]Position{}
	for _, p := range inferred {
		seen[p.Symbol+"|"+p.Side] = p
	}
	out := make([]Position, 0, len(live))
	for _, l := range live {
		amt := float64(l.PositionAmount)
		side := "long"
		switch strings.ToUpper(l.PositionSide) {
		case "SHORT":
			side = "short"
		case "BOTH":
			side = sideOf(amt)
		}
		qty, entry, lev := math.Abs(amt), float64(l.EntryPrice), float64(l.Leverage)
		r := seen[l.Symbol+"|"+side]
		p := Position{Symbol: l.Symbol, Side: side, Qty: qty, EntryPrice: entry, EntryValue: qty * entry,
			OpenedAt: r.OpenedAt, LastAddAt: r.LastAddAt, Source: "public", MarkPrice: float64(l.MarkPrice), Leverage: lev,
			UnrealizedPnL: float64(l.UnrealizedProfit)}
		p.FirstEntry, p.FirstValue, p.PreExisting = r.FirstEntry, r.FirstValue, r.PreExisting
		if lev > 0 {
			p.Margin = p.EntryValue / lev
		}
		out = append(out, p)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].EntryValue > out[b].EntryValue })
	return out
}

// preOpenedBooks lists the symbol|side keys Binance shows bigger than the
// fills explain: those positions were opened before the stored fills begin,
// so the first fill we have for them is an add, not an open. Live positions
// are the only evidence of that.
func preOpenedBooks(live []LivePosition, inferred []Position) map[string]bool {
	if len(live) == 0 {
		return nil
	}
	seen := map[string]float64{}
	for _, p := range inferred {
		seen[PreKey(p.Symbol, p.Side)] += p.Qty
	}
	out := map[string]bool{}
	for _, l := range live {
		amt := float64(l.PositionAmount)
		if amt == 0 {
			continue
		}
		side := sideOf(amt)
		if strings.EqualFold(l.PositionSide, "SHORT") {
			side = "short"
		} else if strings.EqualFold(l.PositionSide, "LONG") {
			side = "long"
		}
		key := PreKey(l.Symbol, side)
		if math.Abs(amt) > seen[key]*(1+1e-6)+1e-9 {
			out[key] = true
		}
	}
	return out
}

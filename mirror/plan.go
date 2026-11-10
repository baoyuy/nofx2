// Package mirror copies an AI trader's positions to another Binance account
// (real or paper) of the same user: opens, adds, reduces and closes, sized by
// a fixed margin or by the AI's share of its equity, and optionally its stop
// loss and take profit.
//
// It works on positions, not orders: each tick compares the AI's positions
// with what the link last saw, so every change is copied the same way,
// whether the AI decided it, its stop fired on the exchange, or it was
// liquidated. The AI trader wakes the link right after its own orders; a
// timer catches the rest.
package mirror

import (
	"fmt"
	"math"
	"sort"

	"nofx/store"
)

// Position is one side of a coin on an account.
type Position struct {
	Symbol   string
	Side     string // long | short
	Qty      float64
	Entry    float64
	Mark     float64
	Leverage int
	PnL      float64
}

func key(symbol, side string) string { return symbol + "|" + side }

// parsePositions reads Trader.GetPositions (Binance hedge-mode shape).
func parsePositions(raw []map[string]interface{}) map[string]Position {
	out := make(map[string]Position, len(raw))
	for _, r := range raw {
		p := Position{}
		p.Symbol, _ = r["symbol"].(string)
		p.Side, _ = r["side"].(string)
		p.Qty, _ = r["positionAmt"].(float64)
		p.Qty = math.Abs(p.Qty)
		p.Entry, _ = r["entryPrice"].(float64)
		p.Mark, _ = r["markPrice"].(float64)
		p.PnL, _ = r["unRealizedProfit"].(float64)
		if lev, ok := r["leverage"].(float64); ok {
			p.Leverage = int(lev)
		}
		if p.Symbol == "" || (p.Side != "long" && p.Side != "short") || p.Qty <= 0 {
			continue
		}
		if p.Mark <= 0 {
			p.Mark = p.Entry
		}
		out[key(p.Symbol, p.Side)] = p
	}
	return out
}

// Skip reasons of a MirrorPosition.
const (
	SkipBeforeStart    = "before_start"    // open before the link (re)started
	SkipOpenFailed     = "open_failed"     // the copy could not be opened
	SkipFollowerClosed = "follower_closed" // the follower's copy is gone
)

// Kinds of planned actions.
const (
	ActOpen   = "open"
	ActAdd    = "add"
	ActReduce = "reduce"
	ActClose  = "close"
	// ActTrack saves a state without an order: a new AI position not to copy,
	// or a new AI quantity to compare against.
	ActTrack = "track"
	// ActForget drops a state whose AI position is gone with nothing to close.
	ActForget = "forget"
	// ActGone marks the follower's copy closed outside the link.
	ActGone = "gone"
)

// Action is one step of a tick.
type Action struct {
	Kind   string
	Symbol string
	Side   string
	// Leader is the AI position now (zero when closed).
	Leader Position
	// Qty is the follower quantity to add, reduce or close; 0 for a close
	// means everything.
	Qty float64
	// Scale is the AI's add or reduce as a share of what it had.
	Scale float64
	State store.MirrorPosition
}

// Tolerances: the AI's quantity must move over qtyMove to count as an add or
// reduce; a reduce leaving under dustShare of the copy closes it.
const (
	qtyMove   = 0.01
	dustShare = 0.02
	// goneAfterMs: a copy missing this long after the link's last order was
	// closed outside the link (stop, liquidation, by hand).
	goneAfterMs = 30_000
)

// plan compares the AI's positions with the link's states and the follower's
// positions. On the first tick after a (re)start, AI positions and adds the
// link has not seen are tracked, never copied, so a stale entry is not
// chased; closes and reduces are still copied.
func plan(leader, follower map[string]Position, states []store.MirrorPosition, first bool, nowMs int64) []Action {
	known := make(map[string]store.MirrorPosition, len(states))
	for _, s := range states {
		known[key(s.Symbol, s.Side)] = s
	}
	var out []Action
	for k, l := range leader {
		if _, ok := known[k]; ok {
			continue
		}
		st := store.MirrorPosition{Symbol: l.Symbol, Side: l.Side, LeaderQty: l.Qty, OpenedAt: nowMs}
		if first {
			st.Skip = SkipBeforeStart
			out = append(out, Action{Kind: ActTrack, Symbol: l.Symbol, Side: l.Side, Leader: l, State: st})
			continue
		}
		out = append(out, Action{Kind: ActOpen, Symbol: l.Symbol, Side: l.Side, Leader: l, State: st})
	}
	for k, st := range known {
		l, open := leader[k]
		f, held := follower[k]
		copied := st.Skip == "" && st.FollowerQty > 0
		a := Action{Symbol: st.Symbol, Side: st.Side, Leader: l, State: st}
		switch {
		case !open && copied && held:
			a.Kind, a.Qty = ActClose, 0
		case !open:
			a.Kind = ActForget
		case !copied:
			if math.Abs(l.Qty-st.LeaderQty) > qtyMove*st.LeaderQty {
				a.Kind = ActTrack
				a.State.LeaderQty = l.Qty
			} else {
				continue
			}
		case !held:
			if nowMs-st.OrderAt < goneAfterMs {
				continue // the order may not show yet
			}
			a.Kind = ActGone
			a.State.Skip, a.State.FollowerQty = SkipFollowerClosed, 0
			a.State.LeaderQty = l.Qty
		default:
			// The follower's copy shrank on its own (partial stop or
			// liquidation): hold what is there.
			if f.Qty < st.FollowerQty*(1-qtyMove) && nowMs-st.OrderAt >= goneAfterMs {
				a.State.FollowerQty = f.Qty
			}
			change := (l.Qty - st.LeaderQty) / st.LeaderQty
			switch {
			case change > qtyMove && first:
				// Added while the link was stopped: compare from here on.
				a.Kind, a.State.LeaderQty = ActTrack, l.Qty
			case change > qtyMove:
				a.Kind, a.Scale = ActAdd, change
				a.Qty = a.State.FollowerQty * change
			case change < -qtyMove:
				a.Kind, a.Scale = ActReduce, -change
				a.Qty = math.Min(a.State.FollowerQty*-change, f.Qty)
				if f.Qty-a.Qty < dustShare*a.State.FollowerQty {
					a.Kind, a.Qty = ActClose, 0
				}
			case a.State.FollowerQty != st.FollowerQty:
				a.Kind = ActTrack
			default:
				continue
			}
		}
		out = append(out, a)
	}
	// Closes and reduces first: they free margin for the opens.
	rank := map[string]int{ActClose: 0, ActReduce: 1, ActGone: 2, ActForget: 3, ActTrack: 4, ActAdd: 5, ActOpen: 6}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Kind] != rank[out[j].Kind] {
			return rank[out[i].Kind] < rank[out[j].Kind]
		}
		return key(out[i].Symbol, out[i].Side) < key(out[j].Symbol, out[j].Side)
	})
	return out
}

// leverageFor is the follower's leverage for a copy of l: the fixed one, or
// the AI's capped at MaxLeverage. The cap only applies when following.
func leverageFor(link store.MirrorLink, l Position) int {
	if link.LeverageMode == "fixed" {
		return max(link.Leverage, 1)
	}
	lev := l.Leverage
	if lev <= 0 {
		lev = link.Leverage
	}
	if link.MaxLeverage > 0 && lev > link.MaxLeverage {
		lev = link.MaxLeverage
	}
	return max(lev, 1)
}

// marginFor is the follower's margin for a new copy of l, before the balance
// check. leaderEquity and followerEquity are only used in ratio mode.
func marginFor(link store.MirrorLink, l Position, leaderEquity, followerEquity float64) (float64, error) {
	var m float64
	switch link.SizeMode {
	case "ratio":
		if leaderEquity <= 0 || followerEquity <= 0 {
			return 0, fmt.Errorf("equity unknown (AI %.2f, follower %.2f)", leaderEquity, followerEquity)
		}
		lev := max(l.Leverage, 1)
		share := l.Qty * l.Mark / float64(lev) / leaderEquity
		m = share * link.Ratio * followerEquity
	default:
		m = link.FixedMargin
	}
	if link.MaxMargin > 0 && m > link.MaxMargin {
		m = link.MaxMargin
	}
	return m, nil
}

package copytrade

import (
	"math"
	"sort"
	"time"
)

// Shadow is a second reading of a coin, recorded next to Confidence and
// scored the same way so the two can be compared before either is trusted.
// Confidence asks who holds the coin; Shadow asks who called it lately: lead
// traders who opened or added within 24h and are not trapped, against those
// who closed or reduced. Scalpers, high-frequency traders and traders who
// average into losses do not vote. Its weights are a first guess, to be set
// from the measured record (see HitRates, group "shadow").
type Shadow struct {
	Direction string  `json:"direction"` // long | short | neutral
	Score     float64 `json:"score"`     // 0-100
	// Calls are fresh calls per side, traders opening within callCluster of
	// each other counted once (one source, several accounts).
	LongCalls  int `json:"long_calls"`
	ShortCalls int `json:"short_calls"`
	// Exits are traders whose latest action on the side within 24h was a
	// close or a reduce.
	LongExits  int `json:"long_exits"`
	ShortExits int `json:"short_exits"`
	// Burst is the most calls of the leading side inside burstWindow: several
	// traders arriving together are an event, the same count spread over a
	// day is not.
	Burst int `json:"burst"`
	// EntryGapPct is how far the price has moved past the leading callers'
	// average entry, in their direction (%). High means the move they called
	// already happened.
	EntryGapPct float64 `json:"entry_gap_pct"`
	// Flow is the balance of calls and exits, −1 (all short) to 1 (all long).
	Flow float64 `json:"flow"`
	// Effective is the number of independent calls behind the leading side:
	// (Σw)²/Σw² of the clustered calls. Accounts that move together count as
	// one, so this is smaller than the call count.
	Effective float64 `json:"effective"`
	// OpenShare is the share of the leading side's calls that opened a new
	// position rather than adding to one, 0-1. Adds are weaker evidence and
	// score half.
	OpenShare float64 `json:"open_share"`
}

const (
	callCluster = time.Minute
	burstWindow = 6 * time.Hour
	// exitWeight: leaving a side says less than opening the other one.
	exitWeight = 0.5
	// Past chaseFreePct beyond the callers' entry the score fades, down to
	// chaseFloor at chaseFullPct.
	chaseFreePct = 1.0
	chaseFullPct = 6.0
	chaseFloor   = 0.4
)

// ClassOf names the kind of market a symbol is, for accuracy kept per kind: a
// trader right on BTC is not thereby right on small coins or stocks. The
// server replaces it with one that knows Binance's stock and commodity
// contracts.
var ClassOf = func(symbol string) string {
	switch symbol {
	case "BTCUSDT", "ETHUSDT":
		return ClassMajor
	}
	return ClassAlt
}

// Market classes.
const (
	ClassMajor  = "major"
	ClassAlt    = "alt"
	ClassTradFi = "tradfi"
)

// shadowCall is one trader's fresh call on one side of a coin.
type shadowCall struct {
	side         string
	weight       float64
	add          bool  // added to an existing position, not a new open
	at           int64 // ms, latest open or add
	entry, value float64
	mark         float64
}

type shadowExit struct {
	side   string
	weight float64
}

// shadowVoter tells whether a trader's calls count, and how much.
func shadowVoter(s Snapshot, symbol string, now time.Time) (float64, bool) {
	if s.HighFreq || s.Style.Martingale() || s.Style.Kind == StyleScalp {
		return 0, false
	}
	acc := s.Accuracy
	if by, ok := s.AccuracyBy[ClassOf(symbol)]; ok && by.Calls >= minAccuracyCalls {
		acc = by
	}
	w := math.Max(0.2, s.Score.Score/100) * shadowAccuracy(acc)
	if onProbation(s.Follow, now) {
		w *= probationWeight
	}
	return w, true
}

// openFactor scales a call by whether it opened a position: an add to an
// existing one is half the evidence of a new open.
func openFactor(openShare float64) float64 {
	return 0.5 + 0.5*math.Max(0, math.Min(1, openShare))
}

// shadowAccuracy weighs a trader's own record harder than the live score
// does: 0.25 at a shrunk rate of 35% or worse, 2 at 70% or better.
func shadowAccuracy(a Accuracy) float64 {
	if a.Calls < minAccuracyCalls {
		return 1
	}
	return math.Max(0.25, math.Min(2, 1+(a.shrunk()-0.5)*5))
}

// latestActions is each symbol|side's newest action at or after sinceMs, from
// newest-first events.
func latestActions(events []Event, sinceMs int64) map[string]string {
	out := map[string]string{}
	for _, e := range events {
		if e.Time < sinceMs {
			break
		}
		if k := e.Symbol + "|" + e.Side; out[k] == "" {
			out[k] = e.Action
		}
	}
	return out
}

func leaving(action string) bool { return action == "close" || action == "reduce" }

// clusters merges calls made within callCluster of the previous one into one
// vote of the heaviest weight, and returns the votes oldest first.
func clusters(calls []shadowCall) []shadowCall {
	sort.SliceStable(calls, func(a, b int) bool { return calls[a].at < calls[b].at })
	var out []shadowCall
	for i, c := range calls {
		if i > 0 && c.at-calls[i-1].at <= callCluster.Milliseconds() {
			last := &out[len(out)-1]
			last.weight = math.Max(last.weight, c.weight)
			last.at = c.at
			// One open in a cluster makes it an open: the cluster is one source.
			last.add = last.add && c.add
			continue
		}
		out = append(out, c)
	}
	return out
}

func shadowOf(calls []shadowCall, exits []shadowExit) Shadow {
	sh := Shadow{Direction: "neutral"}
	var long, short []shadowCall
	for _, c := range calls {
		if c.side == "short" {
			short = append(short, c)
		} else {
			long = append(long, c)
		}
	}
	longVotes, shortVotes := clusters(long), clusters(short)
	sh.LongCalls, sh.ShortCalls = len(longVotes), len(shortVotes)
	var bull, bear float64
	for _, c := range longVotes {
		bull += c.weight
	}
	for _, c := range shortVotes {
		bear += c.weight
	}
	for _, e := range exits {
		if e.side == "short" {
			sh.ShortExits++
			bull += exitWeight * e.weight
		} else {
			sh.LongExits++
			bear += exitWeight * e.weight
		}
	}
	total := bull + bear
	if total <= 0 {
		return sh
	}
	sh.Flow = (bull - bear) / total
	lead, raw, sign := longVotes, long, 1.0
	switch {
	case bull > bear:
		sh.Direction = "long"
	case bear > bull:
		sh.Direction, lead, raw, sign = "short", shortVotes, short, -1
	default:
		return sh
	}
	if len(lead) == 0 {
		// Only exits: people left, nobody called the other way.
		sh.Direction = "neutral"
		return sh
	}
	for i := range lead {
		n := 1
		for j := i + 1; j < len(lead) && lead[j].at-lead[i].at <= burstWindow.Milliseconds(); j++ {
			n++
		}
		sh.Burst = max(sh.Burst, n)
	}
	weights := make([]float64, 0, len(lead))
	adds := 0
	for _, c := range lead {
		weights = append(weights, c.weight)
		if c.add {
			adds++
		}
	}
	sh.Effective = effectiveCount(weights)
	sh.OpenShare = 1
	if n := len(lead); n > 0 {
		sh.OpenShare = float64(n-adds) / float64(n)
	}
	var value, cost, mark float64
	for _, c := range raw {
		if c.entry > 0 && c.value > 0 {
			value += c.value
			cost += c.value / c.entry
		}
		if c.mark > 0 {
			mark = c.mark
		}
	}
	chase := 1.0
	if cost > 0 && mark > 0 {
		avg := value / cost
		sh.EntryGapPct = sign * (mark - avg) / avg * 100
		if sh.EntryGapPct > chaseFreePct {
			chase = math.Max(chaseFloor, 1-(1-chaseFloor)*(sh.EntryGapPct-chaseFreePct)/(chaseFullPct-chaseFreePct))
		}
	}
	burst := 1.0
	switch {
	case sh.Burst >= 3:
		burst = 1.3
	case sh.Burst == 2:
		burst = 1.15
	}
	sh.Score = math.Min(100, 100*math.Abs(sh.Flow)*countFactor(len(lead))*chase*burst*openFactor(sh.OpenShare))
	return sh
}

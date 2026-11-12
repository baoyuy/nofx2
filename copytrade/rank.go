package copytrade

import "math"

// Rank is the cross-coin score: which coin is worth trading now, given what
// the followed traders do. Confidence answers "how one-sided are the holders",
// which says little about whether the trade is still there; Rank answers "is
// there a trade here":
//
//   - fresh calls weigh more than stale holdings: by the time everybody holds
//     a coin the move they called has usually happened;
//   - independent voices weigh more than raw account counts: several accounts
//     of one source are one call;
//   - a coin already held by a crowd, thin on volume, or chased past the
//     callers' entry ranks lower.
//
// All parts are 0-1 except Score and Strength, which are 0-100. The weights
// are a first guess, to be replaced by the measured record (HitRates, group
// "rank") once there is enough of it. Rank does not move trading: the strategy
// still reads Confidence or Shadow through lean().
type Rank struct {
	Direction    string  `json:"direction"`    // long | short | neutral
	Score        float64 `json:"score"`        // 0-100, comparable across coins
	Strength     float64 `json:"strength"`     // 0-100 before the discounts
	Independence float64 `json:"independence"` // effective independent callers
	Crowding     float64 `json:"crowding"`     // 1 = room, floor crowdFloor
	Liquidity    float64 `json:"liquidity"`    // 1 = deep, 0.4 thin, 0.7 unknown
	Entry        float64 `json:"entry"`        // 1 = at the callers' average
	Cost         float64 `json:"cost_pct"`     // estimated round-trip cost, %
	// Basis is what the direction rests on: "fresh" (calls within 24h) or
	// "holding" (no fresh call, only holders).
	Basis   string `json:"basis"`
	Callers int    `json:"callers"` // fresh callers on the leading side
	Holders int    `json:"holders"` // accounts holding the leading side
}

const (
	// rankHoldWeight discounts a reading that has no fresh calls: existing
	// holdings are the weaker evidence.
	rankHoldWeight = 0.7
	// crowdFree holders on one side are fine; beyond it the coin is crowded.
	crowdFree  = 6
	crowdStep  = 0.05
	crowdFloor = 0.7
	// unknownLiquidity is used when the coin's 24h value is not known.
	unknownLiquidity = 0.7
	// Round-trip cost: taker fee twice, plus slippage scaled by liquidity.
	takerFeePct = 0.04
	slippagePct = 0.05
	// A better-than-average entry is a small bonus, a chased one a larger cut.
	entryBonusCap = 1.15
)

// effectiveCount is the number of independent voices behind a set of weights:
// (Σw)² / Σw². Equal weights count once each; one dominant weight still counts
// about one. It is the count the score should use instead of accounts.
func effectiveCount(ws []float64) float64 {
	var sum, sq float64
	for _, w := range ws {
		if w <= 0 {
			continue
		}
		sum += w
		sq += w * w
	}
	if sq <= 0 {
		return 0
	}
	return sum * sum / sq
}

// satCalls turns independent callers into 0-1: 1 → 0.39, 2 → 0.63, 3 → 0.78,
// 5 → 0.92. Two independent calls say much more than one; the tenth adds
// little.
func satCalls(neff float64) float64 {
	if neff <= 0 {
		return 0
	}
	return 1 - math.Exp(-neff/2.0)
}

// crowdingFactor discounts a coin most followed traders already hold. The
// measured record has the 8+ holder band doing worst, so crowding is a cost,
// not a confirmation.
func crowdingFactor(holders int) float64 {
	if holders <= crowdFree {
		return 1
	}
	return math.Max(crowdFloor, 1-crowdStep*float64(holders-crowdFree))
}

// liquidityFactor scores 24h traded value: 100M USDT or more counts fully,
// 1M about a third, unknown is neutral. Below the floor a coin is treated as
// thin whatever it traded.
func liquidityFactor(volume24h float64) float64 {
	if volume24h <= 0 {
		return unknownLiquidity
	}
	return math.Max(0.4, math.Min(1, math.Log10(volume24h/1e6)/2))
}

// entryFactor scores the price against the callers' average entry, in their
// direction (negative = better than their average, positive = chased).
func entryFactor(gapPct float64) float64 {
	if gapPct < 0 {
		return math.Min(entryBonusCap, 1-gapPct*0.05)
	}
	if gapPct <= chaseFreePct {
		return 1
	}
	if gapPct >= chaseFullPct {
		return chaseFloor
	}
	return 1 - (1-chaseFloor)*(gapPct-chaseFreePct)/(chaseFullPct-chaseFreePct)
}

// burstFactor rewards calls that arrived together: several traders within a
// few hours are an event, the same count spread over a day is not.
func burstFactor(burst int) float64 {
	switch {
	case burst >= 3:
		return 1.3
	case burst == 2:
		return 1.15
	}
	return 1
}

func leadingHolders(c SymbolSummary, direction string) int {
	if direction == "short" {
		return c.Short.Traders
	}
	return c.Long.Traders
}

// RankOf scores one coin. Volume24h (optional) makes the liquidity part real;
// without it the coin is treated as of unknown depth.
func RankOf(c SymbolSummary) Rank {
	r := Rank{Direction: "neutral", Crowding: 1, Liquidity: liquidityFactor(c.Volume24h), Entry: 1}
	sh, conf := c.Shadow, c.Confidence
	switch {
	case sh.Direction == "long" || sh.Direction == "short":
		// Fresh calls first: they are the part that is not already priced in.
		r.Direction, r.Basis = sh.Direction, "fresh"
		r.Callers = sh.LongCalls
		if sh.Direction == "short" {
			r.Callers = sh.ShortCalls
		}
		r.Independence = sh.Effective
		if r.Independence <= 0 {
			r.Independence = float64(r.Callers)
		}
		r.Entry = entryFactor(sh.EntryGapPct)
		r.Strength = 100 * math.Abs(sh.Flow) * satCalls(r.Independence) * burstFactor(sh.Burst) * openFactor(sh.OpenShare)
	case conf.Direction == "long" || conf.Direction == "short":
		r.Direction, r.Basis = conf.Direction, "holding"
		r.Independence = conf.Effective
		if r.Independence <= 0 {
			r.Independence = float64(conf.Traders)
		}
		r.Strength = conf.Score * rankHoldWeight
	default:
		return r
	}
	r.Holders = leadingHolders(c, r.Direction)
	r.Crowding = crowdingFactor(r.Holders)
	r.Cost = takerFeePct*2 + slippagePct/math.Max(0.4, r.Liquidity)
	r.Score = math.Min(100, r.Strength*r.Crowding*r.Liquidity*r.Entry)
	return r
}

// AttachRanks sets each coin's 24h traded value, which the copy-trade
// snapshots do not carry, and recomputes Rank with it. Coins missing from
// volumes keep whatever Rank they had.
func AttachRanks(sums []SymbolSummary, volume24h map[string]float64) {
	for i := range sums {
		v, ok := volume24h[sums[i].Symbol]
		if ok {
			sums[i].Volume24h = v
		}
		sums[i].Rank = RankOf(sums[i])
	}
}

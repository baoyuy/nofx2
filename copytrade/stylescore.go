package copytrade

import (
	"math"
	"sort"
	"time"
)

// The per-style reading: every trader is rated inside their own style, over
// the horizon that style trades on, and that rating is their vote on the
// coins they hold. A scalp's position says something for hours, a long-term
// holder's for days, so a vote also fades on the trader's own clock.
//
// It is measured in backtests before anything trades on it (see Backtest).

// styleHorizon is how long after a call a trader of the style is judged.
func styleHorizon(kind string) time.Duration {
	switch kind {
	case StyleScalp:
		return 4 * time.Hour
	case "high_freq":
		// High frequency books are useful for the next few hours only; using
		// the generic 24h horizon made their record look artificially stale.
		return 4 * time.Hour
	case StyleMartingale:
		// A martingale's first call can carry a short-term signal, while its
		// later adds are risk information. Keep the rating on the short clock.
		return 4 * time.Hour
	case StyleLong:
		return 72 * time.Hour
	}
	return accuracyHorizon // swing, and styles not told yet
}

// styleVotes tells whether a trader of the style votes at all. Averaging
// down makes size a symptom, and high-frequency books turn over faster than
// hourly data can follow (they are to be rated on minutes, separately).
func styleVotes(s Snapshot) bool {
	// Every style remains observable. High-frequency and martingale traders
	// are down-weighted below rather than discarded: their activity can still
	// identify short-term pressure, crowded risk and failed moves.
	return s.Style.Kind != StyleUnknown && s.Style.Kind != StyleFewSamples
}

// styleWeight turns the trader's record at their own horizon into a vote
// weight: 1 until minAccuracyCalls, then 0.25 at a shrunk rate of 35% or
// worse up to 2 at 70% or better.
func styleWeight(a Accuracy) float64 { return shadowAccuracy(a) }

// styleFactor keeps different behaviours comparable without treating a
// style label as a quality judgement. The factor is a prior on how much of a
// position is a directional vote for a four-hour horizon; the trader's own
// accuracy still moves it up or down.
func styleFactor(s Snapshot) float64 {
	f := 1.0
	if s.HighFreq || s.Style.Kind == "high_freq" {
		f = 0.20
	}
	switch s.Style.Kind {
	case StyleMartingale:
		f = 0.10
	case "grid":
		f = 0.35
	case StyleScalp:
		f = 0.75
	case StyleLong:
		f = 0.45
	case StyleFewSamples, StyleUnknown:
		f = 0.20
	}
	return f
}

// styleDecay fades a position on the trader's own clock: full weight up to
// their median holding time, down to 0.2 at three times it. A trader with no
// measured holding time, or a position of unknown age, uses holdFactor.
func styleDecay(p Position, s Style, now time.Time) float64 {
	if s.MedianHoldHours <= 0 || s.Kind == StyleFewSamples || p.PreExisting || p.OpenedAt <= 0 {
		return holdFactor(p, now)
	}
	// A floor of an hour: hourly replays cannot see a faster clock.
	median := math.Max(1, s.MedianHoldHours)
	age := now.Sub(time.UnixMilli(p.OpenedAt)).Hours()
	if age <= median {
		return 1
	}
	return math.Max(0.2, 1-0.8*(age-median)/(2*median))
}

// StyleReadings is every coin's per-style reading. Snapshots carry their
// position health and StyleAccuracy.
func StyleReadings(snaps []Snapshot, now time.Time) map[string]Confidence {
	votes := map[string]map[string]*vote{}
	balance := map[string]float64{}
	weight := map[string]float64{}
	decay := map[string]float64{}
	factors := map[string]float64{}
	for _, s := range distinctPortfolios(snaps) {
		if !styleVotes(s) {
			continue
		}
		id := s.Follow.PortfolioID
		balance[id] = s.Follow.MarginBalance
		weight[id] = styleWeight(s.StyleAccuracy)
		factors[id] = styleFactor(s)
		for _, p := range s.Positions {
			if p.Liquidated {
				continue
			}
			if votes[p.Symbol] == nil {
				votes[p.Symbol] = map[string]*vote{}
			}
			k := id + p.Side
			v := votes[p.Symbol][k]
			if v == nil {
				v = &vote{trader: id, side: p.Side}
				votes[p.Symbol][k] = v
			}
			v.stake += p.EntryValue
			v.health += healthFactor(p) * p.EntryValue
			v.opened += openedValue(p)
			decay[p.Symbol+k] += styleDecay(p, s.Style, now) * p.EntryValue
		}
	}
	out := make(map[string]Confidence, len(votes))
	for symbol, byTrader := range votes {
		keys := make([]string, 0, len(byTrader))
		for k := range byTrader {
			keys = append(keys, k)
		}
		sort.Strings(keys) // float sums must not depend on map order
		list := make([]vote, 0, len(keys))
		for _, k := range keys {
			v := byTrader[k]
			health, fade := 1.0, 1.0
			if v.stake > 0 {
				health = v.health / v.stake
				fade = decay[symbol+k] / v.stake
			}
			size := v.opened
			if size <= 0 {
				size = v.stake
			}
			w := weight[v.trader] * factors[v.trader]
			base := w * stakeFactor(size, balance[v.trader]) * fade
			// The rating stands in for the trader score: 1 is an unrated 50.
			list = append(list, vote{trader: v.trader, side: v.side, score: math.Min(100, 50*w),
				base: base, weight: base * health})
		}
		out[symbol] = confidence(list)
	}
	return out
}

// StyleStat is how the traders of one style did on their own calls.
type StyleStat struct {
	Kind    string
	Traders int
	// Calls scored at the style's own horizon, and at 24h for comparison.
	Own, Day Accuracy
	// OwnHours is the style's horizon in hours.
	OwnHours int
	// MedianHold is the median of the traders' median holding hours.
	MedianHold float64
}

// styleKindOf names the group a snapshot is counted in.
func styleKindOf(s Snapshot) string {
	if s.HighFreq {
		return "high_freq"
	}
	return s.Style.Kind
}

func mergeAccuracy(a, b Accuracy) Accuracy {
	out := Accuracy{Calls: a.Calls + b.Calls, Hits: a.Hits + b.Hits}
	if out.Calls > 0 {
		out.Rate = float64(out.Hits) / float64(out.Calls)
		out.AvgReturn = (a.AvgReturn*float64(a.Calls) + b.AvgReturn*float64(b.Calls)) / float64(out.Calls)
	}
	return out
}

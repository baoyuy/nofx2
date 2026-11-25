package copytrade

import (
	"math"
	"sort"
)

// Ranking by measured history instead of the model's self-reported strength.
//
// The per-coin AI reading returns bull/bear scores that the prompt itself calls
// an evidence ranking, not a calibrated win probability. Ranking the coin pool
// on that number ranks on an unvalidated quantity. Instead, readings are
// grouped by (side, conviction band) and each group is ranked by the
// COST-ADJUSTED forward return its own past readings actually produced, with a
// one-sided lower confidence bound so a few lucky samples cannot dominate.
//
// Nothing here is a probability of the next trade. A group's bound is a
// conservative summary of what that group has done before, over whatever market
// conditions the retained history happens to cover.

// GroupCost is the assumed round-trip execution cost (fees + slippage)
// subtracted from every observed return before a group is judged. It is an
// assumption, not a measured fill cost.
const GroupCost = 0.002

// ConvBands are the conviction (|bull-bear|) cut points. A reading below the
// first cut has no band and never enters the pool.
var ConvBands = []float64{20, 35, 50}

// ConvBand labels the conviction band of one reading, or "" when the reading is
// too weak to rank at all.
func ConvBand(conviction float64) string {
	switch {
	case conviction >= ConvBands[2]:
		return "50+"
	case conviction >= ConvBands[1]:
		return "35-50"
	case conviction >= ConvBands[0]:
		return "20-35"
	}
	return ""
}

// GroupKey identifies one ranking group.
type GroupKey struct {
	Side string // long | short
	Band string
}

// GroupStat is the measured history of one group. All returns are fractions
// (0.01 = 1%) already signed in the predicted direction and already reduced by
// GroupCost.
type GroupStat struct {
	Key GroupKey `json:"key"`
	// Samples is the number of mature, de-duplicated readings behind the stat.
	Samples int `json:"samples"`
	// NetMean is the mean cost-adjusted directional return.
	NetMean float64 `json:"net_mean"`
	// NetLower is a one-sided 90% lower bound on NetMean. Negative means the
	// history does not establish a positive edge for this group.
	NetLower float64 `json:"net_lower"`
	// ExcessMean/ExcessLower are the same, measured against the mean return of
	// the coins scored in the same 30-minute cohort (the market benchmark).
	ExcessMean  float64 `json:"excess_mean"`
	ExcessLower float64 `json:"excess_lower"`
	// HasExcess reports that at least one sample carried a cohort benchmark,
	// so the excess figures mean something. Without it they are both zero and
	// must not be read as "no excess".
	HasExcess bool `json:"has_excess"`
	// WinRate is the share of samples whose cost-adjusted return was positive.
	WinRate float64 `json:"win_rate"`
	// Clusters is how many independent time cohorts the samples came from.
	Clusters int `json:"clusters"`
	// Thin reports that Samples is below GroupMinSamples or Clusters below
	// GroupMinClusters, so the bounds are not informative and the caller must
	// fall back rather than trust them.
	Thin bool `json:"thin"`
}

// GroupMinSamples is the point below which a group's measured bound is treated
// as uninformative. Chosen so a bound is not read off a handful of samples; it
// is not a power calculation.
const GroupMinSamples = 25

// GroupObservation is one mature reading fed into the statistics.
type GroupObservation struct {
	Symbol     string
	AnalyzedAt int64
	Side       string  // long | short
	Conviction float64 // |bull - bear|
	// Return is the forward return already signed in the predicted direction
	// (positive = the call was right), before cost.
	Return float64
	// MarketReturn is the mean LONG-side return of the coins scored in the same
	// cohort, i.e. side-agnostic. It is flipped internally for shorts.
	MarketReturn float64
	HasMarket    bool
}

// GroupStats measures every group present in obs.
func GroupStats(obs []GroupObservation) map[GroupKey]GroupStat {
	type acc struct {
		wins, n   int
		exSamples int
		// Per time cohort: the cohort is the independent unit (see below).
		net, ex map[int64][]float64
	}
	buckets := map[GroupKey]*acc{}
	for _, o := range obs {
		if o.Side != "long" && o.Side != "short" {
			continue
		}
		if math.IsNaN(o.Return) || math.IsInf(o.Return, 0) {
			continue
		}
		band := ConvBand(o.Conviction)
		if band == "" {
			continue
		}
		k := GroupKey{Side: o.Side, Band: band}
		a := buckets[k]
		if a == nil {
			a = &acc{net: map[int64][]float64{}, ex: map[int64][]float64{}}
			buckets[k] = a
		}
		c := cohortOf(o.AnalyzedAt)
		net := o.Return - GroupCost
		a.net[c] = append(a.net[c], net)
		a.n++
		if net > 0 {
			a.wins++
		}
		if o.HasMarket && !math.IsNaN(o.MarketReturn) && !math.IsInf(o.MarketReturn, 0) {
			bm := o.MarketReturn
			if o.Side == "short" {
				bm = -bm
			}
			a.ex[c] = append(a.ex[c], net-bm)
			a.exSamples++
		}
	}
	out := make(map[GroupKey]GroupStat, len(buckets))
	for k, a := range buckets {
		st := GroupStat{Key: k, Samples: a.n, Clusters: len(a.net)}
		// Thin is judged on independent clusters, not raw rows: 200 rows from
		// three cohorts are three observations of the market, not 200.
		st.Thin = a.n < GroupMinSamples || st.Clusters < GroupMinClusters
		st.NetMean, st.NetLower = clusterMeanLower(a.net)
		st.ExcessMean, st.ExcessLower = clusterMeanLower(a.ex)
		st.HasExcess = a.exSamples > 0
		if a.n > 0 {
			st.WinRate = float64(a.wins) / float64(a.n)
		}
		out[k] = st
	}
	return out
}

// GroupCohortMs is the time cohort used as the independent unit. Coins scored
// in the same window move together in a one-way market, so treating each as
// an independent draw divided the standard error by sqrt(coins) too many and
// made every short group's bound look positive in a falling market: "short
// everything" again, now with a confidence label.
const GroupCohortMs = int64(4 * 60 * 60 * 1000)

// GroupMinClusters is the fewest independent cohorts a group needs before its
// bound is trusted.
const GroupMinClusters = 6

func cohortOf(atMs int64) int64 { return atMs / GroupCohortMs }

// clusterMeanLower is meanLower over cohort means: each cohort contributes one
// value (the mean of its samples), so correlated samples inside a cohort do
// not shrink the bound. The mean is weighted by cohort size, so a large cohort
// still counts for more in the point estimate.
func clusterMeanLower(byCohort map[int64][]float64) (float64, float64) {
	if len(byCohort) == 0 {
		return 0, 0
	}
	var total, n float64
	means := make([]float64, 0, len(byCohort))
	for _, xs := range byCohort {
		var sum float64
		for _, x := range xs {
			sum += x
		}
		means = append(means, sum/float64(len(xs)))
		total += sum
		n += float64(len(xs))
	}
	_, lower := meanLower(means)
	mean := total / n
	// The bound is centred on the cohort-mean spread but must not sit above
	// the pooled mean.
	gap := meanOf(means) - lower
	return mean, mean - gap
}

func meanOf(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// MeanLower90 is meanLower, exported for the statistics endpoints so the pool
// ranking and the page report the same bound.
func MeanLower90(xs []float64) (float64, float64) { return meanLower(xs) }

// meanLower returns the mean and a one-sided 90% lower bound (normal
// approximation on the standard error). A single sample has no bound, so the
// bound equals the mean; callers must check Samples/Thin.
func meanLower(xs []float64) (float64, float64) {
	n := len(xs)
	if n == 0 {
		return 0, 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(n)
	if n < 2 {
		return mean, mean
	}
	var ss float64
	for _, x := range xs {
		ss += (x - mean) * (x - mean)
	}
	se := math.Sqrt(ss/float64(n-1)) / math.Sqrt(float64(n))
	return mean, mean - 1.2816*se // one-sided 90%
}

// GroupRankInput is one current candidate waiting to be ranked.
type GroupRankInput struct {
	Symbol     string
	Side       string
	Conviction float64
}

// GroupRanked is a candidate with the rank key its group earned.
type GroupRanked struct {
	GroupRankInput
	Band string
	Stat GroupStat
	// Rank is the sort key: higher is better. It is derived from the group's
	// measured lower bound, mapped onto 0-100 for display and for the existing
	// pool-entry threshold.
	Rank float64
	// Thin marks a candidate ranked on a provisional default because its group
	// has too little history. Such a candidate is deliberately kept (a side
	// with no samples yet would otherwise be shut out for good) but ranked
	// below every group with a measured positive bound.
	Thin bool
	// Blocked is set when the group's measured history is informative AND
	// non-positive: the candidate must not enter the pool.
	Blocked bool
	Reason  string
}

// Rank scale: a measured bound is mapped onto 0-100 so one point of rank is
// 0.05% of cost-adjusted return per 4h, centred so a zero bound scores
// thinRank. A thin group sits just under a measured-break-even group.
const (
	rankCenter   = 40.0
	rankPerPct   = 20.0 // rank points per 1% of lower bound
	thinRank     = 30.0 // provisional rank for a group with too little history
	thinMaxBound = 0.0
)

// RankCandidates orders candidates by their group's measured history.
//
// Two different bounds, two different jobs:
//   - BLOCK on the absolute cost-adjusted bound (NetLower). A group whose
//     calls, after cost, did not make money is dropped. Blocking on excess
//     instead was tried against the user's history (11-19..11-22) and blocked
//     every well-sampled group, because in a one-way market no group beat the
//     average of the coins scored with it; the pool then stays empty and the
//     strategy stops trading, which is a decision for the market-breadth veto,
//     not for selection.
//   - RANK on the excess-over-market bound when the group has benchmarks,
//     because inside groups that make money, the one that also beat the coins
//     scored alongside it is showing selection rather than riding the market.
//
// Thin groups are kept at a provisional rank and flagged, so a direction with
// no history yet is not permanently excluded.
func RankCandidates(cands []GroupRankInput, stats map[GroupKey]GroupStat) []GroupRanked {
	out := make([]GroupRanked, 0, len(cands))
	for _, c := range cands {
		band := ConvBand(c.Conviction)
		if band == "" {
			continue
		}
		g := GroupRanked{GroupRankInput: c, Band: band}
		st, ok := stats[GroupKey{Side: c.Side, Band: band}]
		g.Stat = st
		switch {
		case !ok || st.Thin:
			g.Thin = true
			g.Rank = thinRank
			g.Reason = "thin-history"
		default:
			if st.NetLower <= thinMaxBound {
				g.Blocked = true
				g.Reason = "net-bound-not-positive"
			}
			bound := st.NetLower
			if st.HasExcess {
				bound = st.ExcessLower
			}
			g.Rank = rankCenter + bound*100*rankPerPct
			if g.Rank < 0 {
				g.Rank = 0
			}
			if g.Rank > 100 {
				g.Rank = 100
			}
			if g.Reason == "" {
				if st.HasExcess && st.ExcessLower > 0 {
					g.Reason = "net-and-excess-positive"
				} else {
					g.Reason = "net-positive-excess-unproven"
				}
			}
		}
		out = append(out, g)
	}
	// Measured-positive groups first, then thin ones; inside a tier, higher
	// rank, then higher conviction, then symbol for a stable order.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Thin != b.Thin {
			return !a.Thin
		}
		if a.Rank != b.Rank {
			return a.Rank > b.Rank
		}
		if a.Conviction != b.Conviction {
			return a.Conviction > b.Conviction
		}
		return a.Symbol < b.Symbol
	})
	return out
}

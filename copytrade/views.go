package copytrade

import (
	"math"
	"sort"
	"strings"
	"time"
)

// FeedItem is one action of one followed trader.
type FeedItem struct {
	Event
	PortfolioID string `json:"portfolio_id"`
	Nickname    string `json:"nickname"`
	AvatarURL   string `json:"avatar_url"`
}

// FeedFilter narrows the action feed. Zero values match everything.
type FeedFilter struct {
	PortfolioID string
	Symbol      string
	Action      string // open | add | reduce | close
	MinValue    float64
	// HideHighFreq drops high-frequency traders unless PortfolioID picks one.
	HideHighFreq bool
	Limit        int
	// Offset skips that many newest matches (for paging).
	Offset int
}

// Feed merges every followed trader's actions, newest first.
func Feed(snaps []Snapshot, f FeedFilter) []FeedItem {
	items, _ := FeedPage(snaps, f)
	return items
}

// FeedPage returns one page of the merged actions and how many match in all.
func FeedPage(snaps []Snapshot, f FeedFilter) ([]FeedItem, int) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 200
	}
	sym := strings.ToUpper(f.Symbol)
	var out []FeedItem
	for _, s := range snaps {
		if f.PortfolioID != "" && s.Follow.PortfolioID != f.PortfolioID {
			continue
		}
		if f.HideHighFreq && f.PortfolioID == "" && s.HighFreq {
			continue
		}
		for _, e := range s.Events {
			if (sym != "" && e.Symbol != sym) || (f.Action != "" && e.Action != f.Action) || e.Value < f.MinValue {
				continue
			}
			out = append(out, FeedItem{Event: e, PortfolioID: s.Follow.PortfolioID, Nickname: s.Follow.Nickname,
				AvatarURL: s.Follow.AvatarURL})
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Time > out[b].Time })
	total := len(out)
	start := min(max(0, f.Offset), total)
	return out[start:min(start+f.Limit, total)], total
}

// SideSummary totals one side of a coin across followed traders.
type SideSummary struct {
	Traders int     `json:"traders"`
	Value   float64 `json:"value"`
	// AvgEntry is the size-weighted average entry price.
	AvgEntry float64 `json:"avg_entry"`
	// Trapped holders are deep in loss; Fresh ones opened or added within
	// 24 hours. Both are among Traders.
	Trapped int `json:"trapped"`
	Fresh   int `json:"fresh"`
	// Openers are fresh holders whose call counts: not trapped, not high
	// frequency, not averaging down. Holding alone is not a call.
	Openers int `json:"openers"`
}

// SymbolSummary is how followed traders hold one coin.
type SymbolSummary struct {
	Symbol     string      `json:"symbol"`
	Long       SideSummary `json:"long"`
	Short      SideSummary `json:"short"`
	Confidence Confidence  `json:"confidence"`
	// Liquidated counts traders whose position here was likely liquidated;
	// they are left out of Long, Short and Confidence.
	Liquidated int `json:"liquidated"`
	// HighFreq counts high-frequency holders, whose votes are weighted down.
	HighFreq int `json:"high_freq"`
}

// Holder is one followed trader's position in a coin, with the trader's
// 30-day results.
type Holder struct {
	Position
	PortfolioID string  `json:"portfolio_id"`
	Nickname    string  `json:"nickname"`
	AvatarURL   string  `json:"avatar_url"`
	ROI30       float64 `json:"roi_30d"`
	PnL30       float64 `json:"pnl_30d"`
	WinRate30   float64 `json:"win_rate_30d"`
	Score       float64 `json:"score"`
	HighFreq    bool    `json:"high_freq"`
	// Style is how the holder trades: scalp, swing, long, martingale, ...
	Style string `json:"style,omitempty"`
	// Accuracy of the holder's own calls (see Snapshot.Accuracy).
	Accuracy           Accuracy  `json:"accuracy"`
	PositionsUpdatedAt time.Time `json:"positions_updated_at"`
	FillsUpdatedAt     time.Time `json:"fills_updated_at"`
	ProfileUpdatedAt   time.Time `json:"profile_updated_at"`
}

// openedValue is the size a position was opened with: adding to it later must
// not make the trader look more convinced. Falls back to the current value when
// the opening fill is unknown.
func openedValue(p Position) float64 {
	if p.FirstValue > 0 {
		return p.FirstValue
	}
	return p.EntryValue
}

// holdFactor scales a vote by how long the position has been held. A
// position opened minutes ago may be noise; one opened in the last few days
// is the trader's current view; one held for a week or more is mostly a loss
// being sat out (most old positions are under water) and fades. An unknown
// age means it was opened before the 30 days of stored fills: oldest of all.
func holdFactor(p Position, now time.Time) float64 {
	if p.PreExisting || p.OpenedAt <= 0 {
		return 0.3
	}
	switch age := now.Sub(time.UnixMilli(p.OpenedAt)); {
	case age < time.Hour:
		return 0.6
	case age < 4*time.Hour:
		return 0.8
	case age < 3*24*time.Hour:
		return 1
	case age < 7*24*time.Hour:
		return 0.6
	default:
		return 0.3
	}
}

// Symbols sums positions per coin with each coin's direction confidence,
// most traders first. Snapshots must carry their Score and position health.
// Votes weigh each trader's stake by its health, so traders sitting on deep
// losses count less than ones who just opened or added.
func Symbols(snaps []Snapshot) []SymbolSummary {
	now := time.Now()
	by := map[string]*SymbolSummary{}
	qty := map[string]float64{}
	balance := map[string]float64{}
	// Traders who average into losses: their size says little, see style.go.
	martingale := map[string]bool{}
	// New auto-follow picks, still being watched: their votes count half.
	probation := map[string]bool{}
	// Traders whose own calls proved right or wrong weigh more or less.
	accuracy := map[string]float64{}
	// One vote per trader and side.
	votes := map[string]map[string]*vote{}
	for _, s := range snaps {
		balance[s.Follow.PortfolioID] = s.Follow.MarginBalance
		martingale[s.Follow.PortfolioID] = s.Style.Martingale()
		probation[s.Follow.PortfolioID] = onProbation(s.Follow, now)
		accuracy[s.Follow.PortfolioID] = accuracyFactor(s.Accuracy)
		counted := map[string]bool{}
		gone := map[string]bool{}
		hf := map[string]bool{}
		for _, p := range s.Positions {
			sum := by[p.Symbol]
			if sum == nil {
				sum = &SymbolSummary{Symbol: p.Symbol}
				by[p.Symbol] = sum
			}
			k := p.Symbol + p.Side
			if p.Liquidated {
				if !gone[p.Symbol] {
					gone[p.Symbol] = true
					sum.Liquidated++
				}
				continue
			}
			if votes[p.Symbol] == nil {
				votes[p.Symbol] = map[string]*vote{}
			}
			v := votes[p.Symbol][s.Follow.PortfolioID+p.Side]
			if v == nil {
				v = &vote{trader: s.Follow.PortfolioID, side: p.Side, score: s.Score.Score, highFreq: s.HighFreq}
				votes[p.Symbol][s.Follow.PortfolioID+p.Side] = v
			}
			if s.HighFreq && !hf[p.Symbol] {
				hf[p.Symbol] = true
				sum.HighFreq++
			}
			v.stake += p.EntryValue
			v.health += healthFactor(p) * p.EntryValue
			v.opened += openedValue(p)
			v.hold += holdFactor(p, now) * p.EntryValue
			side := &sum.Long
			if p.Side == "short" {
				side = &sum.Short
			}
			// A trader counts once per side even with several positions.
			if !counted[k] {
				counted[k] = true
				side.Traders++
				if p.Trapped {
					side.Trapped++
				}
				if p.Fresh {
					side.Fresh++
					if !p.Trapped && !s.HighFreq && !s.Style.Martingale() {
						side.Openers++
					}
				}
			}
			side.Value += p.EntryValue
			qty[k] += p.Qty
		}
	}
	out := make([]SymbolSummary, 0, len(by))
	for _, s := range by {
		var list []vote
		for _, v := range votes[s.Symbol] {
			health, hold := 1.0, 1.0
			if v.stake > 0 {
				health = v.health / v.stake
				hold = v.hold / v.stake
			}
			// Stake is what the position was opened with: adding to it must
			// not make the vote weigh more.
			size := v.opened
			if size <= 0 {
				size = v.stake
			}
			base := math.Max(1, v.score) * stakeFactor(size, balance[v.trader])
			if v.highFreq {
				base *= highFreqWeight
			}
			// A trader who keeps adding to losses would otherwise look
			// convinced: their position size is a symptom, not a signal.
			if martingale[v.trader] {
				base *= martingaleWeight
			}
			if probation[v.trader] {
				base *= probationWeight
			}
			base *= accuracy[v.trader]
			// Age shapes how much the vote counts, not how healthy it is:
			// Health must stay about losses only.
			list = append(list, vote{trader: v.trader, side: v.side, score: v.score, base: base * hold,
				weight: base * hold * health})
		}
		s.Confidence = confidence(list)
		if q := qty[s.Symbol+"long"]; q > 0 {
			s.Long.AvgEntry = s.Long.Value / q
		}
		if q := qty[s.Symbol+"short"]; q > 0 {
			s.Short.AvgEntry = s.Short.Value / q
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(a, b int) bool {
		na, nb := out[a].Long.Traders+out[a].Short.Traders, out[b].Long.Traders+out[b].Short.Traders
		if na != nb {
			return na > nb
		}
		return out[a].Long.Value+out[a].Short.Value > out[b].Long.Value+out[b].Short.Value
	})
	return out
}

// Holders lists followed traders' positions in one coin, largest first.
func Holders(snaps []Snapshot, symbol string) []Holder {
	symbol = strings.ToUpper(symbol)
	var out []Holder
	for _, s := range snaps {
		for _, p := range s.Positions {
			if p.Symbol != symbol {
				continue
			}
			f := s.Follow
			out = append(out, Holder{Position: p, PortfolioID: f.PortfolioID, Nickname: f.Nickname, AvatarURL: f.AvatarURL,
				ROI30: f.ROI, PnL30: f.PnL, WinRate30: f.WinRate, Score: s.Score.Score, HighFreq: s.HighFreq,
				Style: s.Style.Kind, Accuracy: s.Accuracy, PositionsUpdatedAt: s.PositionsUpdatedAt,
				FillsUpdatedAt: s.FillsUpdatedAt, ProfileUpdatedAt: s.ProfileUpdatedAt})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].EntryValue > out[b].EntryValue })
	return out
}

// Fresh keeps complete, independently refreshed AI snapshots. The UI may still
// show partial snapshots, but their absence must not become an AI exit signal.
func Fresh(snaps []Snapshot, maxAge time.Duration, now time.Time) []Snapshot {
	out := make([]Snapshot, 0, len(snaps))
	for _, s := range snaps {
		if !s.Follow.Private && s.Error == "" && s.Quality(maxAge, now).Complete() {
			out = append(out, s)
		}
	}
	return out
}

// RecentBySymbol returns per coin the newest actions at or after sinceMs,
// at most perSymbol each, newest first, high-frequency traders left out.
func RecentBySymbol(snaps []Snapshot, sinceMs int64, perSymbol int) map[string][]FeedItem {
	out := map[string][]FeedItem{}
	for _, it := range Feed(snaps, FeedFilter{HideHighFreq: true, Limit: 500}) {
		if it.Time < sinceMs {
			break
		}
		if len(out[it.Symbol]) < perSymbol {
			out[it.Symbol] = append(out[it.Symbol], it)
		}
	}
	return out
}

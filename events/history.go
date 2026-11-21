package events

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"nofx/store"
)

type HistoryOptions struct {
	Window       time.Duration
	ThresholdPP  float64
	EndingWithin time.Duration
}
type PriceChange struct {
	Outcome       string    `json:"outcome"`
	PreviousPrice float64   `json:"previous_price"`
	CurrentPrice  float64   `json:"current_price"`
	DeltaPP       float64   `json:"delta_pp"`
	FromAt        time.Time `json:"from_at"`
	ToAt          time.Time `json:"to_at"`
	Significant   bool      `json:"significant"`
}

func marketFingerprint(e Event, m Market) string {
	names := make([]string, 0, len(m.Outcomes))
	for _, o := range m.Outcomes {
		names = append(names, o.Name)
	}
	sort.Strings(names)
	// Length-delimited JSON prevents ambiguous joins. Exact names (not indices),
	// question, rules, deadlines and price basis define comparable contracts.
	data, _ := json.Marshal(struct {
		Title, Question, Rules, Basis string
		End, EventEnd                 *time.Time
		Names                         []string
	}{e.Title, m.Question, m.Rules, m.PriceBasis, m.EndAt, e.EndAt, names})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
func validHistoryMarket(m Market) bool {
	if m.ID == "" || strings.TrimSpace(m.PriceBasis) == "" {
		return false
	}
	names := map[string]bool{}
	for _, o := range m.Outcomes {
		if strings.TrimSpace(o.Name) == "" || names[o.Name] || math.IsNaN(o.Price) || math.IsInf(o.Price, 0) || o.Price < 0 || o.Price > 1 {
			return false
		}
		names[o.Name] = true
	}
	return true
}

// MarkHistoryUnavailable resets local annotations without modifying platform data.
// Market slices are copied because the feed's cached samples are shared read-only.
func MarkHistoryUnavailable(board *Board) {
	if board == nil {
		return
	}
	for i := range board.Events {
		e := &board.Events[i]
		e.FirstSeenAt = nil
		e.IsNew = false
		e.EndingSoon = false
		e.SignificantChange = false
		e.Markets = append([]Market{}, e.Markets...)
		for j := range e.Markets {
			e.Markets[j].Changes = []PriceChange{}
			e.Markets[j].HistoryStatus = "unavailable"
		}
	}
}

// AnnotateHistory records this user's observation of fetched public samples and
// computes deterministic local changes. It performs no network/model/trade work.
// On any persistence failure annotations remain unavailable and the error is
// returned so the caller can append a warning. Options must be explicitly set.
func AnnotateHistory(history *store.EventHistoryStore, userID string, board *Board, opts HistoryOptions) error {
	if board == nil {
		return fmt.Errorf("nil event board")
	}
	MarkHistoryUnavailable(board)
	if opts.Window <= 0 || opts.Window > 15*24*time.Hour || opts.EndingWithin < 0 || math.IsNaN(opts.ThresholdPP) || math.IsInf(opts.ThresholdPP, 0) || opts.ThresholdPP < 0 || opts.ThresholdPP > 100 {
		return fmt.Errorf("invalid event history options")
	}
	samples := make([]store.EventHistorySample, len(board.Events))
	for i, e := range board.Events {
		sample := store.EventHistorySample{Source: e.Source, EventID: e.ID, Title: e.Title, URL: e.URL, Coins: e.Coins, ReceivedAt: e.ReceivedAt}
		sample.Kind, sample.Publisher, sample.Summary, sample.DiscussionURL, sample.ArticleType = e.Kind, e.Publisher, e.Summary, e.DiscussionURL, e.ArticleType
		for _, m := range e.Markets {
			if !validHistoryMarket(m) {
				continue
			}
			ms := store.EventHistoryMarketSample{MarketID: m.ID, Question: m.Question, PriceBasis: m.PriceBasis, Fingerprint: marketFingerprint(e, m)}
			for _, o := range m.Outcomes {
				ms.Prices = append(ms.Prices, store.EventHistoryPrice{Name: o.Name, Price: o.Price})
			}
			sample.Markets = append(sample.Markets, ms)
		}
		samples[i] = sample
	}
	results, err := history.Record(userID, board.GeneratedAt, samples, opts.Window)
	if err != nil {
		return err
	}
	for i := range board.Events {
		e := &board.Events[i]
		result := results[i]
		first := result.FirstSeenAt
		e.FirstSeenAt = &first
		age := board.GeneratedAt.Sub(first)
		e.IsNew = age >= 0 && age <= 24*time.Hour
		if e.EndAt != nil {
			remaining := e.EndAt.Sub(board.GeneratedAt)
			e.EndingSoon = remaining > 0 && remaining <= opts.EndingWithin
		}
		for j := range e.Markets {
			m := &e.Markets[j]
			if !validHistoryMarket(*m) {
				continue
			}
			m.HistoryStatus = "insufficient"
			if board.GeneratedAt.Sub(e.ReceivedAt) > 15*time.Minute || (e.EndAt != nil && !e.EndAt.After(board.GeneratedAt)) || (m.EndAt != nil && !m.EndAt.After(board.GeneratedAt)) {
				m.HistoryStatus = "stale"
				continue
			}
			// Do not compare samples timestamped after the observation clock.
			if e.ReceivedAt.After(board.GeneratedAt) {
				continue
			}
			baseline, ok := result.Baselines[m.ID]
			if !ok || len(m.Outcomes) == 0 {
				continue
			}
			var previous []store.EventHistoryPrice
			if json.Unmarshal([]byte(baseline.PricesJSON), &previous) != nil || len(previous) != len(m.Outcomes) {
				continue
			}
			byName := map[string]float64{}
			for _, p := range previous {
				byName[p.Name] = p.Price
			}
			changes := make([]PriceChange, 0, len(m.Outcomes))
			comparable := true
			for _, o := range m.Outcomes {
				old, ok := byName[o.Name]
				if !ok {
					comparable = false
					break
				}
				delta := (o.Price - old) * 100
				// A tiny arithmetic tolerance makes decimal 5pp boundary inclusive.
				significant := math.Abs(delta)+1e-9 >= opts.ThresholdPP
				changes = append(changes, PriceChange{Outcome: o.Name, PreviousPrice: old, CurrentPrice: o.Price, DeltaPP: delta, FromAt: baseline.ReceivedAt, ToAt: e.ReceivedAt, Significant: significant})
			}
			if !comparable {
				continue
			}
			sort.Slice(changes, func(a, b int) bool { return changes[a].Outcome < changes[b].Outcome })
			m.Changes = changes
			m.HistoryStatus = "available"
			for _, c := range changes {
				if c.Significant {
					e.SignificantChange = true
				}
			}
		}
	}
	return nil
}

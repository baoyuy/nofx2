package copytrade

import (
	"math"
	"time"

	"nofx/store"
)

// TraderScore rates how far a lead trader's record can be trusted, 0-100.
// Weights are fixed: quality 30, drawdown 20, win rate 15, tenure 15,
// scale 10, data reliability 10. Each part is 0-1.
type TraderScore struct {
	Score       float64 `json:"score"`
	Quality     float64 `json:"quality"`     // 30-day ROI relative to max drawdown
	Drawdown    float64 `json:"drawdown"`    // 1 = no drawdown
	WinRate     float64 `json:"win_rate"`    // 30-day win rate
	Tenure      float64 `json:"tenure"`      // days leading, saturates at 90
	Scale       float64 `json:"scale"`       // AUM and copiers, log scale
	Reliability float64 `json:"reliability"` // public positions 1, inferred 0.8
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// ScoreTrader rates a followed trader from its stored profile.
func ScoreTrader(f store.CopyTradeFollow, now time.Time) TraderScore {
	mdd := math.Max(5, f.MDD)
	ratio := f.ROI / mdd
	s := TraderScore{
		// ROI/MDD of 0 → 0, 1 → 0.5, 3 → 0.75, 10 → ~0.9.
		Quality:  clamp01(ratio / (ratio + 1)),
		Drawdown: clamp01(1 - math.Max(0, f.MDD-10)/50),
		WinRate:  clamp01(f.WinRate / 100),
		// Unknown start time counts as half.
		Tenure:      0.5,
		Scale:       clamp01((math.Log10(1+f.AUM)/6 + math.Log10(1+float64(f.CopyCount))/3) / 2),
		Reliability: 0.8,
	}
	if ratio <= 0 {
		s.Quality = 0
	}
	if f.StartTime > 0 {
		days := now.Sub(time.UnixMilli(f.StartTime)).Hours() / 24
		s.Tenure = clamp01(days / 90)
	}
	if f.PositionShow {
		s.Reliability = 1
	}
	s.Score = 100 * (0.30*s.Quality + 0.20*s.Drawdown + 0.15*s.WinRate + 0.15*s.Tenure + 0.10*s.Scale + 0.10*s.Reliability)
	return s
}

// stakeFactor turns a position's share of the trader's balance into a vote
// weight: 1% → ~0.1, 10% → ~0.37, 100% → ~0.66, 300%+ → 1.
func stakeFactor(entryValue, balance float64) float64 {
	if balance <= 0 {
		return 0.5
	}
	return math.Max(0.1, clamp01(math.Log10(1+100*entryValue/balance)/math.Log10(301)))
}

// countFactor keeps one or two traders from making a confident call:
// 1 → 0.25, 3 → 0.58, 5 → 0.76, 8 → 0.90.
func countFactor(traders int) float64 { return 1 - math.Exp(-float64(traders)/3.5) }

// Confidence is a coin's direction among followed traders.
type Confidence struct {
	Direction string  `json:"direction"` // long | short | neutral
	Score     float64 `json:"score"`     // 0-100
	// Parts of the score, for the explanation shown to the user.
	Agreement   float64 `json:"agreement"`    // |long-short| / total weight
	LongShare   float64 `json:"long_share"`   // long weight / total weight
	CountFactor float64 `json:"count_factor"` // penalty for few traders
	Quality     float64 `json:"quality"`      // mean score of the holders, 0-1
	// Health of the leading side: its health-weighted votes over the same
	// votes without health, at most 1. Below 1 when the side sits on losses.
	Health  float64 `json:"health"`
	Traders int     `json:"traders"`
}

// vote is one trader's position on a coin.
type vote struct {
	trader string
	side   string
	weight float64
	// base is weight without the health factor (stake, score, style and age
	// are in it).
	base  float64
	score float64
	// Summed while collecting: entry value, entry value × health, the value
	// the position was opened with, and entry value × hold factor.
	stake, health, opened, hold float64
	highFreq                    bool
}

func confidence(votes []vote) Confidence {
	var long, short, longBase, shortBase, scoreSum float64
	traders := map[string]float64{}
	for _, v := range votes {
		base := v.base
		if base == 0 {
			base = v.weight
		}
		if v.side == "short" {
			short += v.weight
			shortBase += base
		} else {
			long += v.weight
			longBase += base
		}
		traders[v.trader] = v.score
	}
	for _, s := range traders {
		scoreSum += s
	}
	// A hedged trader (long and short) counts once.
	c := Confidence{Direction: "neutral", Traders: len(traders)}
	total := long + short
	if total <= 0 || len(traders) == 0 {
		return c
	}
	c.LongShare = long / total
	c.Agreement = math.Abs(long-short) / total
	c.CountFactor = countFactor(len(traders))
	c.Quality = scoreSum / float64(len(traders)) / 100
	switch {
	case long > short:
		c.Direction = "long"
	case short > long:
		c.Direction = "short"
	}
	// Agreement alone stays high when the whole leading side is trapped;
	// their losses lower the score too.
	lead, leadBase := long, longBase
	if short > long {
		lead, leadBase = short, shortBase
	}
	c.Health = 1
	if leadBase > 0 {
		c.Health = math.Min(1, lead/leadBase)
	}
	// Weak holders lower confidence by up to half.
	c.Score = 100 * c.Agreement * c.CountFactor * (0.5 + 0.5*c.Quality) * c.Health
	return c
}

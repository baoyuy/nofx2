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

// stakeFactor turns the MARGIN a position commits, as a share of the trader's
// balance, into a vote weight: 1% → ~0.1, 10% → ~0.37, 100% → ~0.66, 300%+ → 1.
//
// It used to take notional. At 10x a position committing 30% of the balance is
// 300% of it in notional, so almost every leveraged position saturated at 1 and
// the "conviction" term carried no information. Margin is the risk the trader
// actually put behind the call.
func stakeFactor(margin, balance float64) float64 {
	if balance <= 0 {
		return 0.5
	}
	return math.Max(0.1, clamp01(math.Log10(1+100*margin/balance)/math.Log10(301)))
}

// positionMargin is the margin behind a position: Binance's own figure when
// the portfolio is public, else the notional divided by the position's
// leverage, else by assumedLeverage. notional is the size the vote is based on
// (what the position was opened with), so the estimate matches that size
// rather than the current, possibly added-to, one.
func positionMargin(p Position, notional float64) float64 {
	if p.Margin > 0 && p.EntryValue > 0 && notional > 0 {
		// Scale Binance's margin to the notional the vote uses.
		return p.Margin * notional / p.EntryValue
	}
	lev := p.Leverage
	if lev <= 0 {
		lev = assumedLeverage
	}
	return notional / lev
}

// countFactor saturates with the number of independent voices: about 0.25 for
// one, 0.68 for four, 0.90 for eight.
func countFactor(voices float64) float64 {
	if voices <= 0 {
		return 0
	}
	return 1 - math.Exp(-voices/3.5)
}

// Confidence is a coin's direction among followed traders.
type Confidence struct {
	Direction string  `json:"direction"` // long | short | neutral
	Score     float64 `json:"score"`     // 0-100
	// Parts of the score, for the explanation shown to the user.
	Agreement   float64 `json:"agreement"`    // |long-short| / total weight
	LongShare   float64 `json:"long_share"`   // long weight / total weight
	CountFactor float64 `json:"count_factor"` // penalty for few independent voices
	// Crowding discounts a coin most followed traders already hold (1 = room).
	Crowding float64 `json:"crowding"`
	Quality  float64 `json:"quality"` // mean score of the holders, 0-1
	// Health of the leading side: its health-weighted votes over the same
	// votes without health, at most 1. Below 1 when the side sits on losses.
	Health  float64 `json:"health"`
	Traders int     `json:"traders"`
	// Effective is the number of independent voices behind the leading side:
	// (Σw)²/Σw². Several accounts of one source count as one, so a coin held
	// by "8" traders who move together is not eight times the evidence.
	Effective float64 `json:"effective"`
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
	// the position was opened with, entry value × hold factor, and the margin
	// committed behind the opened value.
	stake, health, opened, hold, margin float64
	highFreq                            bool
}

// marginFor is the margin behind the vote's size, falling back to an assumed
// leverage when no position reported one.
func (v vote) marginFor(size float64) float64 {
	if v.margin > 0 {
		return v.margin
	}
	return size / assumedLeverage
}

func confidence(votes []vote) Confidence {
	var long, short, longBase, shortBase, scoreSum, longSq, shortSq float64
	traders := map[string]float64{}
	for _, v := range votes {
		base := v.base
		if base == 0 {
			base = v.weight
		}
		if v.side == "short" {
			short += v.weight
			shortBase += base
			shortSq += v.weight * v.weight
		} else {
			long += v.weight
			longBase += base
			longSq += v.weight * v.weight
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
	leadSq := longSq
	if short > long {
		lead, leadBase, leadSq = short, shortBase, shortSq
	}
	if leadSq > 0 {
		c.Effective = lead * lead / leadSq
	}
	// The count factor rests on INDEPENDENT voices behind the leading side,
	// not on raw accounts. Counting accounts rewarded crowding: eight
	// correlated holders of the same BTC-beta coin, all in profit, scored like
	// eight separate opinions, so the top band was "the move already happened"
	// and did worse than average. Effective (Σw)²/Σw² was computed for exactly
	// this and was shown on the page, but never entered the score.
	c.CountFactor = countFactor(c.Effective)
	c.Health = 1
	if leadBase > 0 {
		c.Health = math.Min(1, lead/leadBase)
	}
	// A coin most followed traders already hold is crowded: the measured
	// record has the 8+ holder band doing worst. Rank already discounts this;
	// Confidence, which drives the signal ranking and the strategy, did not.
	c.Crowding = crowdingFactor(c.Traders)
	// Weak holders lower confidence by up to half.
	c.Score = 100 * c.Agreement * c.CountFactor * (0.5 + 0.5*c.Quality) * c.Health * c.Crowding
	return c
}

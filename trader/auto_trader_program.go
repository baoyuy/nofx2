package trader

import (
	"fmt"
	"time"

	"nofx/kernel"
	"nofx/store"
)

// programStableConfig returns the coin-source config when the strategy is in
// "program-stable" (rule-based, no AI) mode, else nil.
func (at *AutoTrader) programStableConfig() *store.CoinSourceConfig {
	if at.strategyEngine == nil {
		return nil
	}
	c := at.strategyEngine.GetConfig()
	if c == nil {
		return nil
	}
	if c.CoinSource.ProgramStable {
		cs := c.CoinSource
		return &cs
	}
	return nil
}

func itoaInt(n int) string { return fmt.Sprintf("%d", n) }

// programDecision builds deterministic open/close decisions from the 4h stable
// ranking. It NEVER calls a model. Sizing/stop/take come from the strategy.
func (at *AutoTrader) programDecision(ctx *kernel.Context) (*kernel.FullDecision, error) {
	cs := at.programStableConfig()
	if cs == nil {
		return nil, fmt.Errorf("not a program-stable strategy")
	}
	if kernel.StableRankingResolver == nil {
		return nil, fmt.Errorf("stable ranking unavailable")
	}
	ranks := kernel.StableRankingResolver(at.userID)

	allowLong := cs.ProgramSides == "" || cs.ProgramSides == "both" || cs.ProgramSides == "long"
	allowShort := cs.ProgramSides == "" || cs.ProgramSides == "both" || cs.ProgramSides == "short"
	topN := cs.ProgramTopN
	if topN <= 0 {
		topN = 5
	}
	maxPos := cs.ProgramMaxPositions
	if maxPos <= 0 {
		maxPos = topN
	}
	turnover := cs.ProgramMaxTurnover
	if turnover <= 0 {
		turnover = topN
	}
	posPct := cs.ProgramPositionPct
	if posPct <= 0 {
		posPct = 10
	}
	lev := cs.ProgramLeverage
	if lev <= 0 {
		lev = 3
	}
	stop := cs.ProgramStopLossPct
	if stop <= 0 {
		stop = 5
	}
	take := cs.ProgramTakeProfitPct
	if take <= 0 {
		take = 10
	}
	equity := ctx.Account.TotalEquity

	// Pick top-N tradable targets.
	targets := make([]kernel.StableRank, 0, topN)
	for _, r := range ranks {
		if (r.Direction != "long" && r.Direction != "short") || r.Price <= 0 {
			continue
		}
		if r.Stable < cs.ProgramMinStable {
			continue
		}
		if r.Direction == "long" && !allowLong {
			continue
		}
		if r.Direction == "short" && !allowShort {
			continue
		}
		targets = append(targets, r)
		if len(targets) >= topN {
			break
		}
	}
	targetDir := map[string]string{}
	for _, t := range targets {
		targetDir[t.Symbol] = t.Direction
	}

	inRanks := map[string]bool{}
	for _, r := range ranks {
		inRanks[r.Symbol] = true
	}
	var decisions []kernel.Decision
	stillHeld := map[string]string{}
	closed := 0
	// 1) Close positions that dropped out, flipped, or (expired => not in ranking).
	for _, p := range ctx.Positions {
		want, inTarget := targetDir[p.Symbol]
		closeIt := false
		reason := ""
		if inTarget && want != p.Side {
			if cs.ProgramCloseOnFlip || cs.ProgramExitOnDrop {
				closeIt, reason = true, "方向翻转"
			}
		} else if !inTarget {
			if inRanks[p.Symbol] {
				// still scored/ranked but fell out of the top-N
				if cs.ProgramExitOnDrop {
					closeIt, reason = true, "掉出Top"+itoaInt(topN)
				}
			} else if cs.ProgramCloseOnExpire {
				// no longer ranked => score expired / out of scope
				closeIt, reason = true, "评分过期"
			}
		}
		if closeIt {
			act := "close_long"
			if p.Side == "short" {
				act = "close_short"
			}
			decisions = append(decisions, kernel.Decision{Symbol: p.Symbol, Action: act, Reasoning: "程序平仓: " + reason})
			closed++
		} else {
			stillHeld[p.Symbol] = p.Side
		}
	}

	// 2) Open target coins not already held (respect max positions + turnover).
	opened := 0
	for _, t := range targets {
		if opened >= turnover {
			break
		}
		if len(stillHeld) >= maxPos {
			break
		}
		if _, ok := stillHeld[t.Symbol]; ok {
			continue // already holding this coin
		}
		price := t.Price
		action := "open_long"
		var sl, tp float64
		if t.Direction == "long" {
			sl = price * (1 - stop/100)
			tp = price * (1 + take/100)
		} else {
			action = "open_short"
			sl = price * (1 + stop/100)
			tp = price * (1 - take/100)
		}
		size := equity * posPct / 100
		if lev < 1 {
			lev = 1
		}
		if lev > 125 {
			lev = 125
		}
		if size <= 0 || size > equity {
			size = equity
		}
		if size < 5 || stop <= 0 || take <= 0 || sl <= 0 || tp <= 0 {
			at.logWarnf("🧩 skip open %s: invalid size/sl/tp (size=%.2f sl=%.4f tp=%.4f)", t.Symbol, size, sl, tp)
			continue
		}
		decisions = append(decisions, kernel.Decision{
			Symbol: t.Symbol, Action: action, Leverage: lev, PositionSizeUSD: size,
			StopLoss: sl, TakeProfit: tp, Confidence: int(clampFloat(t.Stable, 0, 100)),
			Reasoning: fmt.Sprintf("程序开仓(稳定分%.0f, %s)", t.Stable, t.Direction),
		})
		stillHeld[t.Symbol] = t.Direction
		opened++
	}

	at.logInfof("🧩 程序(4h稳定)决策: 目标=%d 开=%d 平=%d 权益=%.2f", len(targets), opened, closed, equity)
	return &kernel.FullDecision{Decisions: decisions, Timestamp: time.Now()}, nil
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}


// applyProgramScanInterval lets the strategy's program_scan_minutes override the
// trader scan interval for program-stable strategies.
func (at *AutoTrader) applyProgramScanInterval() {
	cs := at.programStableConfig()
	if cs == nil {
		return
	}
	if cs.ProgramScanMinutes > 0 {
		at.config.ScanInterval = time.Duration(cs.ProgramScanMinutes) * time.Minute
		at.logInfof("🧩 program scan interval applied: %d min", cs.ProgramScanMinutes)
	}
}

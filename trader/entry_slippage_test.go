package trader

import (
	"math"
	"strings"
	"testing"

	"nofx/kernel"
	"nofx/store"
)

// postFillRatio is the check verifyOpeningFill applies to the confirmed entry.
func postFillRatio(entry, stop, take, cost float64) float64 {
	risk := math.Abs(entry-stop)/entry + cost
	return (math.Abs(take-entry)/entry - cost) / risk
}

// Replays every opening the server actually placed under these rules. All of
// them were refused after the fill (or would have been), so all of them must be
// refused at the quote instead. Values are the recorded quote, stop, target and
// the paper venue's confirmed entry.
func TestQuoteTimeRatioRejectsPlansThatCannotSurviveTheFill(t *testing.T) {
	r := store.RiskControlConfig{MaxMarginUsage: 0.5, MinRiskRewardRatio: 2,
		MaxTradeRiskPct: 1, MaxPortfolioRiskPct: 4, TradingCostPct: 0.2}
	cost := 0.002

	cases := []struct {
		name        string
		action      string
		quote, fill float64
		stop, take  float64
	}{
		// Decision 2925: quote ratio 2.051 passed, fill 771.83 gave 1.995 and
		// the position was opened and emergency-closed.
		{"ZEC short, emergency-closed after the fill", "open_short", 772.40139533, 771.825604, 801.5, 708},
		// Decision 2809: quote ratio 2.036, fill 5.0500098 gave 1.998, same.
		{"UNI long, emergency-closed after the fill", "open_long", 5.048, 5.0500098, 4.9, 5.38},
		// Decision 2769 was recorded as a success, but it ran before the
		// post-fill re-check was deployed. Its fill ratio was 1.958, so under
		// these rules it would have been emergency-closed as well.
		{"HYPE long, predates the re-check and would have failed it", "open_long", 64.011, 64.0538082, 63.1, 66.3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The fixture must reproduce the real post-fill failure.
			if fillRatio := postFillRatio(c.fill, c.stop, c.take, cost); fillRatio >= r.MinRiskRewardRatio {
				t.Fatalf("fixture does not reproduce the case: fill ratio %.4f already clears %.1f",
					fillRatio, r.MinRiskRewardRatio)
			}
			// ... and the quote must have looked acceptable, which is why the
			// order was placed at all.
			if quoteRatio := postFillRatio(c.quote, c.stop, c.take, cost); quoteRatio < r.MinRiskRewardRatio {
				t.Fatalf("fixture quote ratio %.4f was already below the bar; nothing to prevent", quoteRatio)
			}
			d := kernel.Decision{Symbol: "XUSDT", Action: c.action, Leverage: 3,
				PositionSizeUSD: 300, StopLoss: c.stop, TakeProfit: c.take}
			err := sizeOpening(&d, r, 1000, 1000, 0, 0, c.quote, false)
			if err == nil {
				t.Fatal("a plan that cannot hold the ratio after the fill must be refused before ordering")
			}
			if !strings.Contains(err.Error(), "entry slippage") {
				t.Fatalf("error should name the slippage headroom, got: %v", err)
			}
		})
	}
}

// The allowance must cover the adverse quote-to-fill moves actually observed on
// the server, otherwise it would not have prevented these emergency closes.
func TestEntrySlipAllowanceCoversObservedFills(t *testing.T) {
	observed := []struct {
		name        string
		quote, fill float64
		short       bool
	}{
		{"ZEC short", 772.40139533, 771.825604, true},
		{"UNI long", 5.048, 5.0500098, false},
		{"HYPE long", 64.011, 64.0538082, false},
	}
	for _, o := range observed {
		adverse := o.fill/o.quote - 1 // long: positive is adverse
		if o.short {
			adverse = 1 - o.fill/o.quote // short: filling lower is adverse
		}
		if adverse <= 0 {
			t.Fatalf("%s: fixture is not an adverse fill (%.5f)", o.name, adverse)
		}
		if adverse > entrySlipAllowance {
			t.Fatalf("%s: observed adverse entry %.4f%% exceeds the %.4f%% allowance, so the check would not have prevented it",
				o.name, adverse*100, entrySlipAllowance*100)
		}
	}
}

// The quote-time check evaluates the post-fill formula at the worst entry the
// allowance covers, so it agrees with verifyOpeningFill there by construction.
func TestWorstEntryRatioMatchesPostFillCheckAtTheWorstEntry(t *testing.T) {
	cost := 0.002
	for _, tc := range []struct {
		name       string
		price      float64
		stop, take float64
		short      bool
	}{
		{"long", 100, 97, 107, false},
		{"short", 100, 103, 93, true},
		{"wide stop long", 500, 450, 640, false},
		{"zec short", 772.40139533, 801.5, 708, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worst := tc.price * (1 + entrySlipAllowance)
			if tc.short {
				worst = tc.price * (1 - entrySlipAllowance)
			}
			got := worstEntryRatio(tc.price, tc.stop, tc.take, cost, tc.short)
			want := postFillRatio(worst, tc.stop, tc.take, cost)
			if math.Abs(got-want) > 1e-12 {
				t.Fatalf("worst-entry ratio %.9f != post-fill ratio at the worst entry %.9f", got, want)
			}
			// An adverse entry can only make the plan worse.
			if got > postFillRatio(tc.price, tc.stop, tc.take, cost) {
				t.Fatalf("worst entry must not improve the ratio")
			}
		})
	}
}

// The headroom does not change the configured ratio: a plan with comfortable
// margin is accepted, and the quote ratio alone is still enforced.
func TestQuoteTimeCheckKeepsConfiguredRatioAsTheBar(t *testing.T) {
	r := store.RiskControlConfig{MaxMarginUsage: 0.5, MinRiskRewardRatio: 2,
		MaxTradeRiskPct: 1, MaxPortfolioRiskPct: 4, TradingCostPct: 0.2}
	ok := kernel.Decision{Symbol: "XUSDT", Action: "open_long", Leverage: 3,
		PositionSizeUSD: 300, StopLoss: 97, TakeProfit: 110}
	if err := sizeOpening(&ok, r, 1000, 1000, 0, 0, 100, false); err != nil {
		t.Fatalf("a plan well above the bar must pass: %v", err)
	}
	poor := kernel.Decision{Symbol: "XUSDT", Action: "open_long", Leverage: 3,
		PositionSizeUSD: 300, StopLoss: 97, TakeProfit: 104}
	err := sizeOpening(&poor, r, 1000, 1000, 0, 0, 100, false)
	if err == nil || !strings.Contains(err.Error(), "below configured") || strings.Contains(err.Error(), "entry slippage") {
		t.Fatalf("a plan below the bar at the quote keeps the original message, got: %v", err)
	}
}

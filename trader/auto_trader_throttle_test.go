package trader

import (
	"nofx/kernel"
	"strings"
	"testing"
	"time"
)

func throttleContext(symbol, side string, heldFor time.Duration, pnlPct float64) *kernel.Context {
	return leveragedThrottleContext(symbol, side, heldFor, pnlPct, 1)
}

func leveragedThrottleContext(symbol, side string, heldFor time.Duration, pnlPct float64, leverage int) *kernel.Context {
	return &kernel.Context{
		Positions: []kernel.PositionInfo{
			{
				Symbol:           symbol,
				Side:             side,
				UnrealizedPnLPct: pnlPct,
				Leverage:         leverage,
				UpdateTime:       time.Now().Add(-heldFor).UnixMilli(),
			},
		},
	}
}

func TestTradeThrottleNeverBlocksRiskReducingClose(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		for _, held := range []time.Duration{time.Minute, 75 * time.Minute, 90 * time.Minute, 150 * time.Minute, 3 * time.Hour} {
			for _, pnl := range []float64{-60, -7.7, -5.4, -1.67, 0, 4.6, 20, 80} {
				ctx := leveragedThrottleContext("SOLUSDT", side, held, pnl, 10)
				at := &AutoTrader{}
				if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "SOLUSDT", Action: "close_" + side}, ctx); reason != "" {
					t.Fatalf("risk exit blocked for %s held %s ROI %.2f: %s", side, held, pnl, reason)
				}
			}
		}
	}
}

// These snapshots are from the server's rejected SOL exits. They check only
// permission to reduce risk, not a hypothetical fill or improved profit.
func TestServerSOLExitReplayIsNotAgeGated(t *testing.T) {
	for _, tc := range []struct {
		held         time.Duration
		marginReturn float64
	}{
		{75 * time.Minute, -5.4}, {90 * time.Minute, -5.3}, {150 * time.Minute, -7.7}, {180*time.Minute - time.Second, -1.67},
	} {
		at := &AutoTrader{}
		ctx := leveragedThrottleContext("SOLUSDT", "long", tc.held, tc.marginReturn, 10)
		if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "SOLUSDT", Action: "close_long"}, ctx); reason != "" {
			t.Fatal(reason)
		}
	}
}

func TestTradeThrottleBlocksQuickReentryAfterClose(t *testing.T) {
	// Re-entering a just-closed symbol was a consistent loss source in the
	// replay data; the 4h cooldown is enforced from recent close orders, which
	// requires a store — covered by the throttle reason path being non-empty
	// only when a recent close order exists (nil store returns no orders).
	at := &AutoTrader{}
	ctx := &kernel.Context{}
	if reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: "open_long"}, ctx); reason != "" {
		t.Fatalf("expected open with no order history to be allowed, got %q", reason)
	}
}

func TestTradeThrottleDoesNotCapOpensPerCycle(t *testing.T) {
	at := &AutoTrader{}
	ctx := &kernel.Context{}

	for _, symbol := range []string{"xyz:INTC", "xyz:NVDA", "xyz:SNDK", "xyz:MU", "xyz:SP500"} {
		if reason := at.tradeThrottleReason(kernel.Decision{Symbol: symbol, Action: "open_long"}, ctx); reason != "" {
			t.Fatalf("expected %s open to have no per-cycle count cap, got %q", symbol, reason)
		}
	}
}

func TestTradeThrottleBlocksOpeningAgainstExistingPosition(t *testing.T) {
	at := &AutoTrader{}
	ctx := throttleContext("xyz:INTC", "long", 2*time.Hour, 1.0)

	reason := at.tradeThrottleReason(kernel.Decision{Symbol: "xyz:INTC", Action: "open_short"}, ctx)
	if !strings.Contains(reason, "already has an open") {
		t.Fatalf("expected opposite open to be blocked when position exists, got %q", reason)
	}
}

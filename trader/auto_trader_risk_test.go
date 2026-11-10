package trader

import "testing"

func TestDrawdownCloseArmsOnPriceBasisOnly(t *testing.T) {
	cases := []struct {
		name                         string
		peakPricePnLPct, drawdownPct float64
		shouldClose                  bool
	}{
		{"tiny price move big drawdown", 0.5, 60, false},
		{"real gain small drawdown", 6, 20, false},
		{"real gain big drawdown", 6, 45, true},
		{"at threshold not armed", 5, 45, false},
		{"fell back below threshold but historical peak armed", 6, 45, true},
		{"loss never triggers", -3, 80, false},
	}
	for _, c := range cases {
		if got := shouldDrawdownClose(c.peakPricePnLPct, c.drawdownPct); got != c.shouldClose {
			t.Fatalf("%s: shouldDrawdownClose(%.1f, %.1f) = %v, want %v",
				c.name, c.peakPricePnLPct, c.drawdownPct, got, c.shouldClose)
		}
	}
}

package store

import (
	"fmt"
	"math"
)

// ProfitProtectionConfig uses underlying price percentages, never leveraged ROI.
// Zero activation/giveback values mean defaults, NOT immediate activation.
// A zero lock means fee/slippage-adjusted breakeven; a positive lock adds a
// gross price buffer, not a promise of net realized returns through gaps.
type ProfitProtectionConfig struct {
	Enabled                bool    `json:"enabled"`
	BreakevenActivationPct float64 `json:"breakeven_activation_pct"`
	BreakevenLockPct       float64 `json:"breakeven_lock_pct"`
	TrailingActivationPct  float64 `json:"trailing_activation_pct"`
	TrailingGivebackPct    float64 `json:"trailing_giveback_pct"`
}

func NormalizeProfitProtection(c ProfitProtectionConfig) ProfitProtectionConfig {
	positive := func(v, fallback, min, max float64) float64 {
		if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return fallback
		}
		return math.Max(min, math.Min(max, v))
	}
	c.BreakevenActivationPct = positive(c.BreakevenActivationPct, 2, 0.1, 100)
	if math.IsNaN(c.BreakevenLockPct) || math.IsInf(c.BreakevenLockPct, 0) {
		c.BreakevenLockPct = 0
	}
	c.BreakevenLockPct = math.Max(0, math.Min(c.BreakevenActivationPct, c.BreakevenLockPct))
	c.TrailingActivationPct = positive(c.TrailingActivationPct, 5, 0.1, 100)
	c.TrailingGivebackPct = positive(c.TrailingGivebackPct, 40, 1, 99)
	return c
}

// ValidateProfitProtectionExchange must also run before constructing an exchange
// client and when replacing a running strategy. The monitor independently gates it.
func ValidateProfitProtectionExchange(exchange string, c ProfitProtectionConfig) error {
	if c.Enabled && exchange != "binance_paper" {
		return fmt.Errorf("profit protection is supported only for binance_paper simulated accounts")
	}
	return nil
}

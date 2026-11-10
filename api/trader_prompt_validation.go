package api

import (
	"fmt"

	"nofx/trader"
)

// Validate against the effective saved strategy before any trader write. An
// explicitly detached trader has no automatic runtime until rebound.
func (s *Server) validateTraderPromptForStrategy(userID, strategyID, prompt string, override bool) error {
	if strategyID == "" {
		return nil
	}
	strategy, err := s.store.Strategy().Get(userID, strategyID)
	if err != nil {
		return fmt.Errorf("trader strategy is unavailable; no changes were saved")
	}
	cfg, err := strategy.ParseConfig()
	if err != nil {
		return fmt.Errorf("invalid trader strategy configuration; no changes were saved")
	}
	return trader.ValidateTraderPrompt(cfg.StrategyType, prompt, override)
}

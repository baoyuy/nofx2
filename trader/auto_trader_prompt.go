package trader

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"nofx/kernel"
	"nofx/store"
)

var strategyLifecycleMu sync.Mutex

// LockStrategyLifecycle serializes API strategy/prompt saves with Run's initial
// strategy refresh and running-flag publication in this process. The returned
// release is idempotent so startup can release early and still defer cleanup.
// It never spans model calls, exchange requests, or the trading loop.
func LockStrategyLifecycle() func() {
	strategyLifecycleMu.Lock()
	var once sync.Once
	return func() { once.Do(strategyLifecycleMu.Unlock) }
}

// ValidateTraderPrompt rejects settings the grid decision path cannot consume.
// Empty + false is always allowed so legacy settings can be cleared.
func ValidateTraderPrompt(strategyType, prompt string, override bool) error {
	if strategyType == "grid_trading" && (strings.TrimSpace(prompt) != "" || override) {
		return fmt.Errorf("trader custom_prompt/override_base_prompt are not supported for grid strategies; clear both settings or use an AI strategy")
	}
	return nil
}

func validatePromptRulesForStrategy(config *store.StrategyConfig, rules kernel.TraderPromptRules) error {
	if config == nil {
		return nil
	}
	return ValidateTraderPrompt(config.StrategyType, rules.CustomPrompt, rules.OverrideBasePrompt)
}

// SetPromptSettings updates both fields atomically, including clearing them.
func (at *AutoTrader) SetPromptSettings(prompt string, override bool) error {
	at.strategyMu.RLock()
	defer at.strategyMu.RUnlock()
	if err := validatePromptRulesForStrategy(at.config.StrategyConfig, kernel.TraderPromptRules{CustomPrompt: prompt, OverrideBasePrompt: override}); err != nil {
		return err
	}
	at.promptMu.Lock()
	defer at.promptMu.Unlock()
	at.customPrompt, at.overrideBasePrompt = prompt, override
	return nil
}

func (at *AutoTrader) promptRules() kernel.TraderPromptRules {
	at.promptMu.RLock()
	defer at.promptMu.RUnlock()
	return kernel.TraderPromptRules{CustomPrompt: at.customPrompt, OverrideBasePrompt: at.overrideBasePrompt}
}

// requestAIDecision is shared by the live AI loop and final-message tests.
func (at *AutoTrader) requestAIDecision(ctx *kernel.Context) (*kernel.FullDecision, error) {
	at.strategyMu.RLock()
	engine := at.strategyEngine
	at.strategyMu.RUnlock()
	return kernel.GetFullDecisionWithStrategy(ctx, at.mcpClient, engine, "balanced", at.promptRules())
}

func cloneTraderStrategyConfig(config *store.StrategyConfig) *store.StrategyConfig {
	raw, err := json.Marshal(config)
	if err != nil {
		return nil
	}
	var copy store.StrategyConfig
	if err := json.Unmarshal(raw, &copy); err != nil {
		return nil
	}
	return &copy
}

// IsRunning reads only the runtime flag (unlike GetStatus, no unrelated state).
func (at *AutoTrader) IsRunning() bool {
	at.isRunningMutex.RLock()
	defer at.isRunningMutex.RUnlock()
	return at.isRunning
}

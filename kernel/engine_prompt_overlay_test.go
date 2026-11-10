package kernel

import (
	"nofx/store"
	"strings"
	"testing"
)

func TestSingleXYZPreservesExplicitCustomRules(t *testing.T) {
	for _, symbol := range []string{"xyz:NVDA", "xyz:GOLD", "xyz:EUR"} {
		for _, custom := range []string{"Only enter after the daily close.", "用户规则：只在美股开盘后一小时交易。", "Use Claw402 data only as supporting context; never remove my rule."} {
			cfg := store.GetDefaultStrategyConfig("en")
			cfg.CoinSource = store.CoinSourceConfig{SourceType: "static", StaticCoins: []string{symbol}}
			cfg.CustomPrompt = custom
			prompt := NewStrategyEngine(&cfg).BuildSystemPrompt(1000, "balanced")
			if !strings.Contains(prompt, custom) {
				t.Fatalf("%s custom rules erased: %q", symbol, custom)
			}
			if strings.Contains(prompt, "DIRECTIONAL, SIGNAL-DRIVEN") {
				t.Fatalf("%s explicit rules replaced/augmented by directional default", symbol)
			}
			if !strings.Contains(prompt, "Hard Constraints (Risk Control)") || !strings.Contains(prompt, "<decision>") {
				t.Fatal("asset rules removed safety/output protocol")
			}
		}
	}
}
func TestSingleXYZUsesTemplateOnlyForEmptyCustomRules(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource = store.CoinSourceConfig{SourceType: "static", StaticCoins: []string{"xyz:NVDA"}}
	cfg.CustomPrompt = "  "
	prompt := NewStrategyEngine(&cfg).BuildSystemPrompt(1000, "balanced")
	if !strings.Contains(prompt, "DIRECTIONAL, SIGNAL-DRIVEN") {
		t.Fatal("empty XYZ config lost built-in default")
	}
	prompt = NewStrategyEngine(&cfg).BuildSystemPrompt(1000, "balanced", TraderPromptRules{CustomPrompt: "TRADER_REPLACEMENT", OverrideBasePrompt: true})
	if strings.Contains(prompt, "DIRECTIONAL, SIGNAL-DRIVEN") || !strings.Contains(prompt, "TRADER_REPLACEMENT") {
		t.Fatal("trader override must suppress editable default")
	}
}
func TestTraderRulesCannotRemoveSignalPolicyContract(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "vergex_signal"
	cfg.CustomPrompt = "STRATEGY_USER_PREFERENCE"
	prompt := NewStrategyEngine(&cfg).BuildSystemPrompt(1000, "balanced", TraderPromptRules{CustomPrompt: "TRADER_RULE: prefer Claw402 explanations in Chinese.", OverrideBasePrompt: true})
	for _, required := range []string{"TRADER_RULE", "Existing long + bullish ranking: always `hold`", "Direction is determined only by the current Claw402 ranking", "<decision>", "stop_loss", "cannot be overridden"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("signal prompt lost %q", required)
		}
	}
	if strings.Contains(prompt, "STRATEGY_USER_PREFERENCE") {
		t.Fatal("override retained strategy custom preference")
	}
}

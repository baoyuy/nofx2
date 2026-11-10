package kernel

import (
	"strings"
	"testing"

	"nofx/store"
)

func TestBuildSystemPromptUsesVergexClaw402Prompt(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("zh")
	cfg.CoinSource.SourceType = "vergex_signal"
	cfg.CoinSource.VergexLimit = 5
	cfg.PromptSections.RoleDefinition = "# You are a professional Hyperliquid USDC multi-asset trading AI"
	cfg.CustomPrompt = "Long only, no shorts."

	engine := NewStrategyEngine(&cfg)
	prompt := engine.BuildSystemPrompt(30, "balanced")

	if !strings.Contains(prompt, "NOFX Claw402 auto-trader") {
		t.Fatalf("prompt did not use the Claw402/Vergex TradeFi role:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Claw402.ai Direction Board") || !strings.Contains(prompt, "Current Direction and Direction History") || !strings.Contains(prompt, "Cost/Liquidation Heatmap") {
		t.Fatalf("prompt is missing Claw402/Vergex detail data guidance:\n%s", prompt)
	}
	if !strings.Contains(prompt, "open_short") {
		t.Fatalf("prompt should explicitly allow short entries:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Direction is determined only by the current Claw402 ranking") {
		t.Fatalf("prompt should make the current Claw402 direction authoritative:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Existing long + bullish ranking: always `hold`") ||
		!strings.Contains(prompt, "Existing short + bearish ranking: always `hold`") ||
		!strings.Contains(prompt, "Close an existing position only when its ranking direction changes") {
		t.Fatalf("prompt is missing the strict signal hold/exit state machine:\n%s", prompt)
	}
	if !strings.Contains(prompt, "every open position must use exactly 10x") {
		t.Fatalf("prompt should force 10x leverage for Claw402 opens:\n%s", prompt)
	}
	if !strings.Contains(prompt, "use the full max notional per position") {
		t.Fatalf("prompt should force full-size Claw402 opens:\n%s", prompt)
	}
	if !strings.Contains(prompt, "`stop_loss` must be a positive protective price") ||
		!strings.Contains(prompt, "`take_profit` must be exactly 0") {
		t.Fatalf("prompt must explain signal-managed exit fields:\n%s", prompt)
	}
	if containsCJK(prompt) {
		t.Fatalf("system prompt must be English-only, got CJK text:\n%s", prompt)
	}
	legacyPhrases := []string{
		"Hyperliquid USDC multi-asset trading AI",
		"Long only",
		"Altcoin",
		"BTC/ETH",
		"LONG-ONLY",
		"Do not short",
		"MUST open a long",
		"Ranking alone is not an entry reason",
		"Open only when Signal Lab",
		"Claw402.ai Signal Lab",
	}
	for _, phrase := range legacyPhrases {
		if strings.Contains(prompt, phrase) {
			t.Fatalf("prompt still contains legacy phrase %q:\n%s", phrase, prompt)
		}
	}
}

func TestBuildSystemPromptUsesUserRulesInAnyLanguage(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("zh")
	cfg.CoinSource.SourceType = "static"
	cfg.CoinSource.StaticCoins = []string{"BTCUSDT", "ETHUSDT"}
	cfg.CoinSource.VergexLimit = 0
	cfg.CoinSource.VergexMarketType = ""
	cfg.CoinSource.VergexChain = ""
	cfg.PromptSections.RoleDefinition = "# 角色\n你是一名只做 BTC 的趋势交易员"
	cfg.PromptSections.TradingFrequency = "# 交易频率\n每天最多两笔"
	cfg.PromptSections.EntryStandards = "# Entry\nFollow the Claw402 board."
	cfg.PromptSections.DecisionProcess = ""
	cfg.CustomPrompt = "只在 4 小时趋势向上时做多"

	prompt := NewStrategyEngine(&cfg).BuildSystemPrompt(30, "balanced")

	for _, phrase := range []string{
		"Data Dictionary & Trading Rules",
		"你是一名只做 BTC 的趋势交易员",
		"每天最多两笔",
		"只在 4 小时趋势向上时做多",
		"Entry Standards (Strict)", // legacy Claw402 text replaced by the built-in section
		"Decision Process",         // empty section falls back to the built-in one
	} {
		if !strings.Contains(prompt, phrase) {
			t.Fatalf("prompt missing %q:\n%s", phrase, prompt)
		}
	}
	if strings.Contains(prompt, "Claw402 board") {
		t.Fatalf("legacy paid-board rules must not reach the model:\n%s", prompt)
	}
}

// Claude refuses prompts that ask for its "chain of thought" (empty reply,
// stop_reason "refusal"), so the built-in prompt must not use that wording.
func TestBuildSystemPromptAvoidsChainOfThoughtWording(t *testing.T) {
	for _, lang := range []string{"zh", "en"} {
		cfg := store.GetDefaultStrategyConfig(lang)
		cfg.CoinSource = store.CoinSourceConfig{SourceType: "static", StaticCoins: []string{"BTCUSDT", "ETHUSDT"}}
		cfg.PromptSections.DecisionProcess = ""
		prompt := strings.ToLower(NewStrategyEngine(&cfg).BuildSystemPrompt(1000, "balanced"))
		for _, phrase := range []string{"chain of thought", "thinking process"} {
			if strings.Contains(prompt, phrase) {
				t.Fatalf("%s prompt still asks for %q", lang, phrase)
			}
		}
		if !strings.Contains(prompt, "<reasoning>") || !strings.Contains(prompt, "<decision>") {
			t.Fatalf("%s prompt lost the <reasoning>/<decision> output format", lang)
		}
	}
}

func TestBuildSystemPromptDoesNotForceLongOnlyForSingleXYZ(t *testing.T) {
	prompt := buildXYZStockCustomPrompt("XYZ:INTC")

	required := []string{
		"DIRECTIONAL, SIGNAL-DRIVEN",
		"You may open long or short",
		"open_short",
	}
	for _, phrase := range required {
		if !strings.Contains(prompt, phrase) {
			t.Fatalf("single XYZ prompt missing %q:\n%s", phrase, prompt)
		}
	}

	forbidden := []string{
		"LONG-ONLY",
		"Do not short",
		"MUST open a long",
		"Probing > waiting",
	}
	for _, phrase := range forbidden {
		if strings.Contains(prompt, phrase) {
			t.Fatalf("single XYZ prompt still contains forced-long phrase %q:\n%s", phrase, prompt)
		}
	}
}

func containsCJK(text string) bool {
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

func TestLegacyVergexFieldsDoNotSelectSignalManagedPrompt(t *testing.T) {
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.CoinSource.SourceType = "claw402"
	cfg.CoinSource.VergexLimit = 5
	cfg.CoinSource.VergexMarketType = "all"
	if NewStrategyEngine(&cfg).usesVergexSignalPrompt() {
		t.Fatal("legacy Vergex fields must not select the signal-managed prompt")
	}
}

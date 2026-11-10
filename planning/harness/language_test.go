package harness

import (
	"context"
	"strings"
	"testing"

	"nofx/planning/core"
	"nofx/planning/model"
)

type languageModel struct{ system []string }

func (m *languageModel) Chat(_ context.Context, messages []model.Message, _ []model.Tool) (model.Message, error) {
	for _, message := range messages {
		if message.Role == "system" {
			m.system = append(m.system, message.Content)
		}
	}
	return model.Message{Role: "assistant", Content: "ok"}, nil
}

func TestReplyLanguageFollowsTheInterface(t *testing.T) {
	for _, tc := range []struct{ language, want, forbid string }{
		{"zh", "Simplified Chinese", "Reply language: English"},
		{"en", "Reply language: English", "Simplified Chinese"},
		{"", "Reply language: English", "Simplified Chinese"},
	} {
		planner := &languageModel{}
		source := textMarketFixture{}
		engine := Engine{Store: &textDraftFixture{}, Model: planner, Market: source, Charts: source, Limits: core.Constraints{Equity: "10000", MaxRisk: "100", MaxNotional: "1000", MinRewardRisk: "0", FeeBPS: "10", SlippageBPS: "10"}}
		err := engine.Run(context.Background(), Input{Message: "plan", Venue: "binance", Symbol: "BTCUSDT", Interval: "1h", Language: tc.language}, func(Event) error { return nil })
		if err != nil {
			t.Fatal(tc.language, err)
		}
		joined := strings.Join(planner.system, "\n")
		if !strings.Contains(joined, tc.want) || strings.Contains(joined, tc.forbid) {
			t.Fatalf("language %q: unexpected system instructions", tc.language)
		}
		if strings.Contains(joined, "Use English for all user-facing text") {
			t.Fatal("the base prompt must not force English")
		}
	}
	if err := (Input{Message: "plan", Venue: "binance", Symbol: "BTCUSDT", Language: "fr"}).Validate(); err == nil {
		t.Fatal("unsupported reply language must be rejected")
	}
}

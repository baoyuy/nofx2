package copytrade

import "testing"

func TestClassifyEventsSeparatesDirectionalAndRiskActions(t *testing.T) {
	events := []Event{{Symbol: "X", Side: "long", Action: "open"}, {Symbol: "X", Side: "long", Action: "add"}, {Symbol: "X", Side: "short", Action: "add"}, {Symbol: "X", Side: "long", Action: "reduce"}, {Symbol: "X", Side: "short", Action: "close"}}
	pos := []Position{{Symbol: "X", Side: "long", Health: Health{Priced: true, ReturnPct: -.1}}, {Symbol: "X", Side: "short", Health: Health{Priced: true, ReturnPct: .1}}}
	got := classifyEvents(events, pos)
	want := []string{"directional_open", "adverse_add", "trend_add", "reduce", "exit"}
	for i, w := range want {
		if got[i].SignalType != w {
			t.Fatalf("event %d: got %s want %s", i, got[i].SignalType, w)
		}
	}
}

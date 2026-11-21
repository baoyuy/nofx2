package trader

import (
	"testing"
	"time"

	"nofx/kernel"
)

func TestParseEventCards(t *testing.T) {
	raw := `{"cards":[{"type":"财报","title":"Cerebras 财报超预期","summary":"营收大超预期，盘后大涨","direction":"bullish","surprise":"超预期","importance":85,"is_node":true,"node_at":"2026-11-15T21:00:00Z","valid_hours":24,"evidence_ids":["ev1","ev2","bogus"],"reason":"重要催化剂，顺势做多"}]}`
	known := map[string]bool{"ev1": true, "ev2": true}
	cards := parseEventCards(raw, "CRBS", known, time.Now().UTC())
	if len(cards) != 1 {
		t.Fatalf("expected 1 card, got %d", len(cards))
	}
	c := cards[0]
	if c.Coin != "CRBS" || c.Type != "earnings" || c.Direction != "bullish" || c.Surprise != "above" {
		t.Fatalf("unexpected card: %+v", c)
	}
	if c.Importance != 85 || !c.IsNode || c.NodeAt == nil {
		t.Fatalf("node/importance wrong: %+v", c)
	}
	if len(c.EvidenceIDs) != 2 || c.EvidenceIDs[0] != "ev1" || c.EvidenceIDs[1] != "ev2" {
		t.Fatalf("evidence not filtered: %+v", c.EvidenceIDs)
	}
	if c.ValidUntil == nil {
		t.Fatalf("valid until should be set")
	}
}

func TestParseEventCardsEmptyAndInvalid(t *testing.T) {
	if cards := parseEventCards(`{"cards":[]}`, "BTC", nil, time.Now()); len(cards) != 0 {
		t.Fatalf("expected empty, got %+v", cards)
	}
	if cards := parseEventCards(`not json`, "BTC", nil, time.Now()); len(cards) != 0 {
		t.Fatalf("expected nil on invalid, got %+v", cards)
	}
}

func TestNormalizeEventTypeSurprise(t *testing.T) {
	if got := normalizeEventType("黑天鹅"); got != "blackswan" {
		t.Fatalf("blackswan: %s", got)
	}
	if got := normalizeEventType("上市"); got != "listing" {
		t.Fatalf("listing: %s", got)
	}
	if got := normalizeEventType("weird"); got != "other" {
		t.Fatalf("other: %s", got)
	}
	if got := normalizeSurprise("超预期"); got != "above" {
		t.Fatalf("above: %s", got)
	}
	if got := normalizeSurprise("???"); got != "unknown" {
		t.Fatalf("unknown: %s", got)
	}
}

func TestEventCardIDDeterministic(t *testing.T) {
	c := kernelCard("BTC", "earnings", []string{"ev2", "ev1"})
	c2 := kernelCard("BTC", "earnings", []string{"ev1", "ev2"})
	if eventCardID(c) != eventCardID(c2) {
		t.Fatalf("card id should be order-independent")
	}
}

func kernelCard(coin, typ string, ids []string) kernel.EventCard {
	return kernel.EventCard{Coin: coin, Type: typ, EvidenceIDs: ids, Direction: "bullish"}
}

func TestClassifyEventTypeFromText(t *testing.T) {
	cases := map[string]string{
		"BTC跌破6.8万美元，市场清算约16亿美元": "blackswan",
		"加密市场抛售加深，比特币跌至四个月低点": "blackswan",
		"某交易所被盗 2 亿美元":       "security",
		"SEC 起诉某项目方":          "regulation",
		"某代币将上线币安":           "listing",
		"英伟达财报超预期":           "earnings",
		"某协议完成主网升级":          "protocol",
	}
	for in, want := range cases {
		if got := classifyEventTypeFromText(in); got != want {
			t.Fatalf("classify(%q)=%q want %q", in, got, want)
		}
	}
}

func TestMergeEventType(t *testing.T) {
	cases := []struct{ model, text, want string }{
		{"macro", "比特币跌至四个月低点，市场抛售加剧", "blackswan"},
		{"other", "某交易所被盗 2 亿美元", "security"},
		{"macro", "FOMC Meeting Minutes", "macro"},
		{"earnings", "英伟达财报超预期", "earnings"},
		{"other", "SOL创60日新低后反弹", "other"},
	}
	for _, c := range cases {
		if got := mergeEventType(c.model, c.text); got != c.want {
			t.Fatalf("mergeEventType(%q,%q)=%q want %q", c.model, c.text, got, c.want)
		}
	}
}

func TestInferHorizon(t *testing.T) {
	now := time.Now().UTC()
	far := now.AddDate(2, 0, 0)
	if got := inferHorizon("x", &far, now); got != "far" {
		t.Fatalf("far node => %s", got)
	}
	near := now.AddDate(0, 0, 3)
	if got := inferHorizon("x", &near, now); got != "near" {
		t.Fatalf("near node => %s", got)
	}
	if got := inferHorizon("DTCC 计划 2027 年上线", nil, now); got != "far" {
		t.Fatalf("2027 text => %s", got)
	}
}

func TestDedupeCards(t *testing.T) {
	cards := []kernel.EventCard{
		{Coin: "BTC", Type: "blackswan", Direction: "bearish", Horizon: "near", Importance: 70},
		{Coin: "BTC", Type: "blackswan", Direction: "bearish", Horizon: "near", Importance: 90},
		{Coin: "BTC", Type: "etf", Direction: "bearish", Horizon: "near", Importance: 60},
	}
	got := dedupeCards(cards, 8)
	if len(got) != 2 { // one blackswan (highest) + etf
		t.Fatalf("dedupe kept %d: %+v", len(got), got)
	}
	if got[0].Importance != 90 {
		t.Fatalf("should keep highest blackswan, got %+v", got[0])
	}
}

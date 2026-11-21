package trader

import (
	"strings"
	"testing"
	"time"

	"nofx/events"
)

func TestConvertEventContextItem(t *testing.T) {
	end := time.Now().UTC().Add(2 * time.Hour)
	e := events.Event{
		Title:       strings.Repeat("A", 400),
		Source:      "Kalshi",
		Kind:        "prediction",
		PublishedAt: time.Now().UTC(),
		EndAt:       &end,
		Markets: []events.Market{
			{Question: "Q1", Outcomes: []events.Outcome{{Name: "Yes", Price: 0.6}, {Name: "No", Price: 0.4}}},
			{Question: "Q2"},
			{Question: "Q3"},
		},
	}
	item := convertEventContextItem(e)
	if len([]rune(item.Title)) > maxEventSummaryRunes+1 {
		t.Fatalf("title not bounded: %d runes", len([]rune(item.Title)))
	}
	if len(item.Markets) != maxEventMarketsPerItem {
		t.Fatalf("markets not bounded to %d: got %d", maxEventMarketsPerItem, len(item.Markets))
	}
	if len(item.Markets[0].Outcomes) != 2 || item.Markets[0].Outcomes[0].Price != 0.6 {
		t.Fatalf("outcomes not copied correctly: %+v", item.Markets[0])
	}
	if item.Source != "Kalshi" || item.Kind != "prediction" {
		t.Fatalf("metadata not copied: %+v", item)
	}
}

func TestBuildEventContextSkipsWhenDisabled(t *testing.T) {
	at := &AutoTrader{config: AutoTraderConfig{}}
	if got := at.buildEventContext([]string{"BTCUSDT"}); got != nil {
		t.Fatalf("expected nil when strategy config missing, got %+v", got)
	}
}

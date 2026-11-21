package events

import (
	"testing"
	"time"
)

func TestParseTelegramChannel(t *testing.T) {
	html := `<div class="tgme_widget_message" data-post="chan/123"><div class="tgme_widget_message_text" dir="auto">Bitcoin BTC 暴涨突破新高</div><time datetime="2026-11-15T09:00:00+00:00"></time></div>` +
		`<div class="tgme_widget_message" data-post="chan/124"><div class="tgme_widget_message_text" dir="auto">ETH 跌破 1800 美元</div><time datetime="2026-11-15T09:05:00+00:00"></time></div>` +
		`<div class="tgme_widget_message" data-post="chan/125"><div class="tgme_widget_message_text" dir="auto">今日天气不错</div><time datetime="2026-11-15T09:10:00+00:00"></time></div>`
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	coins := []string{"BTC", "ETH", "SOL"}
	traders := map[string][]TraderRef{"BTC": {{TraderID: "t1"}}, "ETH": {{TraderID: "t1"}}}
	evs := parseTelegramChannel(html, "chan", coins, traders, now)
	if len(evs) != 2 {
		t.Fatalf("expected 2 matched messages, got %d: %+v", len(evs), evs)
	}
	if evs[0].Source != "Telegram" || len(evs[0].Coins) != 1 || evs[0].Coins[0] != "BTC" {
		t.Fatalf("unexpected first event: %+v", evs[0])
	}
	if len(evs[1].Coins) != 1 || evs[1].Coins[0] != "ETH" {
		t.Fatalf("unexpected second event: %+v", evs[1])
	}
}

func TestGlobalSourcesEmptyWithoutConfig(t *testing.T) {
	f := &Feed{extraCache: map[string]extraEntry{}}
	// No crypto calendar path and no telegram channels -> no events, no warnings.
	now := time.Now().UTC()
	evs, _ := f.cryptoCalendarEvents(nil, now, []string{"BTC"}, nil)
	if len(evs) != 0 {
		t.Fatalf("expected no crypto calendar events, got %+v", evs)
	}
	evs, _ = f.telegramEvents(nil, now, []string{"BTC"}, nil)
	if len(evs) != 0 {
		t.Fatalf("expected no telegram events, got %+v", evs)
	}
}

func TestFNGClassificationCN(t *testing.T) {
	cases := map[string]string{"Extreme Fear": "极度恐慌", "Fear": "恐慌", "Neutral": "中性", "Greed": "贪婪", "Extreme Greed": "极度贪婪"}
	for in, want := range cases {
		if got := fngClassificationCN(in); got != want {
			t.Fatalf("fngClassificationCN(%q)=%q want %q", in, got, want)
		}
	}
}

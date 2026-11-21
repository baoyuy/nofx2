package events

import (
	"encoding/xml"
	"testing"
	"time"
)

func TestFeedNameAndRSSParse(t *testing.T) {
	if feedName("https://www.coindesk.com/arc/outboundfeeds/rss/") != "CoinDesk" {
		t.Fatalf("feedName coindesk")
	}
	body := `<rss><channel><item><title>Bitcoin BTC surges</title><link>https://x/1</link><pubDate>Sat, 15 Nov 2026 10:00:00 GMT</pubDate><description>BTC up</description></item></channel></rss>`
	var doc rssDoc
	if err := xmlUnmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Channel.Items) != 1 || doc.Channel.Items[0].Title != "Bitcoin BTC surges" {
		t.Fatalf("rss parse: %+v", doc)
	}
	if parseRSSDate(doc.Channel.Items[0].PubDate).IsZero() {
		t.Fatalf("date parse failed")
	}
}

func TestAnnouncementEventMatches(t *testing.T) {
	now := timeNow()
	traders := map[string][]TraderRef{"AERO": {{TraderID: "t1"}}}
	evs := announcementEvent("OKX", "OKX will launch AERO/USDC for spot trading", "https://x", "announcements-new-listings", now, now, []string{"AERO", "BTC"}, traders)
	if len(evs) != 1 || evs[0].Coins[0] != "AERO" || evs[0].Kind != "listing" {
		t.Fatalf("announcement match: %+v", evs)
	}
}

func xmlUnmarshal(b string, v any) error { return xml.Unmarshal([]byte(b), v) }
func timeNow() time.Time { return time.Now().UTC() }

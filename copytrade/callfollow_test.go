package copytrade

import (
	"math"
	"testing"
	"time"
)

func TestCallCollectorCountsRepeatsOnceAndAgreement(t *testing.T) {
	base := time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC)
	open := func(trader string, after time.Duration) rawOpen {
		at := base.Add(after)
		return rawOpen{trader: trader, votes: true, callEvent: callEvent{kind: StyleScalp, symbol: "XUSDT", side: "long",
			at: at.UnixMilli(), hour: at.Truncate(time.Hour).Add(time.Hour).Unix(), price: 100, own: 4}}
	}
	c := newCallCollector()
	c.add([]rawOpen{open("a", 0)})
	c.add([]rawOpen{open("a", time.Hour), open("b", 2*time.Hour)}) // a repeats within its 4h horizon
	c.add([]rawOpen{open("a", 9*time.Hour)})                       // a new event; b's open is too old to agree
	if len(c.events) != 3 {
		t.Fatalf("%d events, want 3", len(c.events))
	}
	for i, want := range []int{1, 2, 1} {
		if c.events[i].agree != want {
			t.Errorf("event %d: %d traders agreeing, want %d", i, c.events[i].agree, want)
		}
	}
}

func TestFollowOutcomeEntersAtTheHourCloseAndSubtractsBTC(t *testing.T) {
	h0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).Unix()
	closes := map[string]map[int64]float64{
		"XUSDT":     {h0: 100, h0 + 4*3600: 110},
		benchSymbol: {h0: 50000, h0 + 4*3600: 51000},
	}
	// Opened during the bar that opens at h0; that bar closes at h0+1h.
	e := callEvent{symbol: "XUSDT", side: "short", at: (h0 + 1800) * 1000, hour: h0 + 3600, price: 90, own: 4}
	ret, excess, ok, benched := followOutcome(e, 4, false, closes, nil, h0+100*3600)
	if !ok || !benched {
		t.Fatalf("ok %v benched %v", ok, benched)
	}
	// Short from 100 to 110 loses 10%; BTC rose 2%, so a short on it lost 2%.
	if math.Abs(ret+0.10) > 1e-9 || math.Abs(excess+0.08) > 1e-9 {
		t.Errorf("ret %.4f excess %.4f, want -0.10 and -0.08", ret, excess)
	}
	// Past the end of the data: not scored.
	if _, _, ok, _ := followOutcome(e, 4, false, closes, nil, h0+2*3600); ok {
		t.Error("scored an open whose horizon is past the end")
	}
}

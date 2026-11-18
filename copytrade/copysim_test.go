package copytrade

import (
	"math"
	"testing"
	"time"
)

func TestMirrorSizesByLargestExposureAndChargesFees(t *testing.T) {
	t0 := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	ms := func(min int) int64 { return t0.Add(time.Duration(min) * time.Minute).UnixMilli() }
	var bars []Bar
	for i := 0; i < 40; i++ {
		bars = append(bars, Bar{Time: t0.Unix() + int64(i)*300, Close: 100})
	}
	bars[2].Close, bars[4].Close = 90, 99 // add at 90, close at 99
	in := CopyInput{Bars: map[string][]Bar{"XUSDT": bars}, Step: 300, To: t0.Add(3 * time.Hour), Fee: 0.001}
	// Opens 1 at 100, adds 9 at 90, closes all 10 at 99.
	trip := copyTrip{symbol: "XUSDT", side: "long", openAt: ms(1), closedAt: ms(21),
		legs: []copyLeg{{ms(1), 1, 100}, {ms(11), 9, 90}, {ms(21), -10, 99}}}
	r := in.mirror(trip, 0)
	if !r.ok {
		t.Fatal("not priced")
	}
	// Bought 100 + 810, sold 990: +80 before fees; traded 1900, fees 1.9.
	// Largest exposure, valued at the fill that made it: 10 × 90 = 900.
	if want := (80 - 1.9) / 900; math.Abs(r.ret-want) > 1e-9 {
		t.Errorf("return %.5f, want %.5f", r.ret, want)
	}
	if math.Abs(r.peak-9) > 1e-9 {
		t.Errorf("grew to %.2f times the open, want 9.00", r.peak)
	}
	// Taking only the open misses the add: 100 → 99, two fees.
	o := in.openOnly(trip, 0, 0)
	if want := -0.01 - 0.002; !o.ok || math.Abs(o.ret-want) > 1e-9 {
		t.Errorf("open-only return %.5f, want %.5f", o.ret, want)
	}
}

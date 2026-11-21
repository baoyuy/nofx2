package chanlun

import (
	"math"
	"math/rand"
	"testing"
)

// path turns a list of turning prices into bars, legLen bars per leg.
func path(turns []float64, legLen int) []Bar {
	var bars []Bar
	for t := 1; t < len(turns); t++ {
		from, to := turns[t-1], turns[t]
		for k := 0; k < legLen; k++ {
			o := from + (to-from)*float64(k)/float64(legLen)
			c := from + (to-from)*float64(k+1)/float64(legLen)
			bars = append(bars, Bar{
				Time: int64(len(bars)) * 3600_000,
				Open: o, Close: c,
				High: math.Max(o, c) + 0.1, Low: math.Min(o, c) - 0.1,
			})
		}
	}
	return bars
}

// zig expands segment-level turning prices into stroke-level ones: every move
// becomes three strokes, the last of them shorter than the first.
func zig(moves []float64) []float64 {
	out := []float64{moves[0]}
	for i := 1; i < len(moves); i++ {
		a, d := moves[i-1], moves[i]-moves[i-1]
		out = append(out, a+0.7*d, a+0.45*d, moves[i])
	}
	return out
}

func replay(bars []Bar) (*Engine, []Signal, []Point) {
	e := New()
	var sigs []Signal
	var segs []Point
	for _, b := range bars {
		ev := e.Push(b)
		if ev.Signal != nil {
			sigs = append(sigs, *ev.Signal)
		}
		if ev.Seg != nil {
			segs = append(segs, *ev.Seg)
		}
	}
	return e, sigs, segs
}

func find(sigs []Signal, kind string) *Signal {
	for i := range sigs {
		if sigs[i].Kind == kind {
			return &sigs[i]
		}
	}
	return nil
}

func kinds(sigs []Signal) []string {
	var out []string
	for _, s := range sigs {
		out = append(out, s.Kind)
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.11 }

func TestStrokesAlternate(t *testing.T) {
	e, _, _ := replay(path([]float64{100, 110, 104, 112, 105, 111, 103}, 6))
	pts := e.Points()
	if len(pts) < 5 {
		t.Fatalf("got %d points, want at least 5", len(pts))
	}
	for i := 1; i < len(pts); i++ {
		if pts[i].Top == pts[i-1].Top {
			t.Fatalf("points %d and %d are both top=%v", i-1, i, pts[i].Top)
		}
		if pts[i].Raw <= pts[i-1].Raw {
			t.Fatalf("point %d does not move forward in time", i)
		}
	}
}

func pt(top bool, price float64) Point { return Point{Top: top, Price: price} }

func alternating(firstTop bool, prices ...float64) []Point {
	var out []Point
	for i, p := range prices {
		out = append(out, pt(firstTop == (i%2 == 0), p))
	}
	return out
}

func TestSegEndNoGap(t *testing.T) {
	// Up from 100; the down strokes are [110,105] [120,114] [130,118] [124,108]:
	// the third is the top fractal and overlaps the second, so 130 ends it.
	pts := alternating(false, 100, 110, 105, 120, 114, 130, 118, 124, 108)
	if got := segEnd(pts, 0, false); got != 5 {
		t.Fatalf("segEnd = %d, want 5 (the 130 top)", got)
	}
	// One stroke earlier the fractal's third element does not exist yet.
	if got := segEnd(pts[:8], 0, false); got != -1 {
		t.Fatalf("segEnd on the prefix = %d, want -1", got)
	}
}

func TestSegEndGapNeedsSecondFractal(t *testing.T) {
	// The down stroke from 140 bottoms at 125, above the previous element's
	// high of 120: a gap, so the top stands only once the down segment shows
	// a bottom fractal in its own sequence.
	pts := alternating(false, 100, 110, 105, 120, 114, 140, 125, 132, 122)
	if got := segEnd(pts, 0, false); got != -1 {
		t.Fatalf("segEnd = %d, want -1 while the gap is unconfirmed", got)
	}
	pts = append(pts, alternating(true, 128, 118, 126, 121, 130)...)
	if got := segEnd(pts, 0, false); got != 5 {
		t.Fatalf("segEnd = %d, want 5 after the second fractal", got)
	}
}

func TestSegEndGapVoidedByNewHigh(t *testing.T) {
	pts := alternating(false, 100, 110, 105, 120, 114, 140, 125, 150, 138, 146, 130)
	if got := segEnd(pts, 0, false); got != 7 {
		t.Fatalf("segEnd = %d, want 7 (the 150 top, not the gapped 140)", got)
	}
}

func TestSegEndDownMirrorsUp(t *testing.T) {
	pts := alternating(true, 200, 190, 195, 180, 186, 170, 182, 176, 192)
	if got := segEnd(pts, 0, false); got != 5 {
		t.Fatalf("segEnd = %d, want 5 (the 170 bottom)", got)
	}
}

func TestSegmentsAlternate(t *testing.T) {
	e, _, _ := replay(randomWalk(4000, 7))
	segs := e.Segments()
	if len(segs) < 10 {
		t.Fatalf("only %d segment points", len(segs))
	}
	for i := 1; i < len(segs); i++ {
		if segs[i].Top == segs[i-1].Top {
			t.Fatalf("segment points %d and %d are both top=%v", i-1, i, segs[i].Top)
		}
		if up := segs[i].Top; up != (segs[i].Price > segs[i-1].Price) {
			t.Fatalf("segment %d ends on the wrong side: %+v -> %+v", i, segs[i-1], segs[i])
		}
	}
}

func TestThirdBuy(t *testing.T) {
	// Up into a pivot 110..118, a move out to 140, a pullback that holds 125.
	bars := path(zig([]float64{90, 100, 95, 120, 110, 118, 108, 140, 125, 150, 140, 155}), 6)
	_, sigs, _ := replay(bars)
	b3 := find(sigs, "B3")
	if b3 == nil {
		t.Fatalf("no B3 in %v", kinds(sigs))
	}
	if !near(b3.Stop, 118) {
		t.Fatalf("B3 stop = %v, want the pivot's ZG near 118", b3.Stop)
	}
}

func TestFirstAndSecondBuy(t *testing.T) {
	// Down trend with two separate pivots. The last fall makes a new low in
	// strokes that keep getting shorter, then the first pullback holds.
	turns := zig([]float64{
		215, 220, 170, // the entering fall
		182, 172, 181, // pivot A
		140,           // the fall between the pivots
		152, 142, 151, // pivot B
	})
	turns = append(turns, 140.5, 144, 137) // new low on a shorter, slower stroke
	turns = append(turns, zig([]float64{137, 150, 141, 160, 150, 165})[1:]...)
	// 12 bars a stroke, so the MACD has time to follow each one.
	_, sigs, _ := replay(path(turns, 12))
	b1 := find(sigs, "B1")
	if b1 == nil {
		t.Fatalf("no B1 in %v", kinds(sigs))
	}
	if !near(b1.Stop, 137) {
		t.Fatalf("B1 stop = %v, want the new low near 137", b1.Stop)
	}
	b2 := find(sigs, "B2")
	if b2 == nil {
		t.Fatalf("no B2 in %v", kinds(sigs))
	}
	if !near(b2.Stop, 137) {
		t.Fatalf("B2 stop = %v, want the 1st buy's low near 137", b2.Stop)
	}
}

func randomWalk(n int, seed int64) []Bar {
	r := rand.New(rand.NewSource(seed))
	px := 100.0
	bars := make([]Bar, n)
	for i := range bars {
		o := px
		px *= 1 + r.NormFloat64()*0.01
		hi := math.Max(o, px) * (1 + r.Float64()*0.004)
		lo := math.Min(o, px) * (1 - r.Float64()*0.004)
		bars[i] = Bar{Time: int64(i) * 3600_000, Open: o, High: hi, Low: lo, Close: px}
	}
	return bars
}

// Nothing reported on a prefix may change when later bars are appended.
func TestNoLookAhead(t *testing.T) {
	bars := randomWalk(6000, 42)
	_, full, fullSegs := replay(bars)
	if len(full) < 20 {
		t.Fatalf("only %d signals on the random walk, test is too weak", len(full))
	}
	for cut := 500; cut < len(bars); cut += 613 {
		_, part, partSegs := replay(bars[:cut])
		for i, s := range part {
			if i >= len(full) || full[i] != s {
				t.Fatalf("cut %d: signal %d = %+v differs from the full run", cut, i, s)
			}
		}
		for i, p := range partSegs {
			if i >= len(fullSegs) || fullSegs[i] != p {
				t.Fatalf("cut %d: segment %d = %+v differs from the full run", cut, i, p)
			}
		}
	}
}

func TestRunAccounting(t *testing.T) {
	bars := randomWalk(6000, 3)
	res := Run(bars, Config{Trail: true})
	if len(res.Trades) < 5 {
		t.Fatalf("only %d trades", len(res.Trades))
	}
	if len(res.Equity) != len(bars) {
		t.Fatalf("equity has %d marks for %d bars", len(res.Equity), len(bars))
	}
	eq := 1.0
	for i, tr := range res.Trades {
		if tr.ExitIdx < tr.EntryIdx {
			t.Fatalf("trade %d exits before it enters: %+v", i, tr)
		}
		if i > 0 && tr.EntryIdx < res.Trades[i-1].ExitIdx {
			t.Fatalf("trade %d overlaps the previous one", i)
		}
		eq *= 1 + tr.Return
	}
	if got := res.Equity[len(bars)-1]; math.Abs(got-eq) > 1e-9 {
		t.Fatalf("final equity %v does not match the compounded trades %v", got, eq)
	}
	// With no costs a long stopped out can lose no more than its stop allows
	// unless the bar gapped; costs must only ever reduce the result.
	costly := Run(bars, Config{Trail: true, FeeRate: 0.001, Slippage: 0.001})
	if costly.Equity[len(bars)-1] >= res.Equity[len(bars)-1] {
		t.Fatal("fees and slippage did not reduce the result")
	}
}

func TestFundingCharged(t *testing.T) {
	bars := randomWalk(6000, 3)
	var f []Funding
	for i := 0; i < len(bars); i += 8 {
		f = append(f, Funding{Time: bars[i].Time, Rate: 0.0001})
	}
	base := Run(bars, Config{LongOnly: true})
	paid := Run(bars, Config{LongOnly: true, Funding: f})
	if paid.Equity[len(bars)-1] >= base.Equity[len(bars)-1] {
		t.Fatal("positive funding did not cost a long-only run anything")
	}
}

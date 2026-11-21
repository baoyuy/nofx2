// Package chanlun is an incremental 缠论 structure engine:
//
//	K线 -> 包含处理 -> 分型 -> 笔 -> 线段(特征序列) -> 线段中枢 -> 走势类型 -> 三类买卖点
//
// with a second pass of the segment rule over the segments themselves for the
// higher level, and 区间套 for timing: a buy/sell point of the segment level
// is taken when the stroke that ends its last, still unconfirmed segment is
// confirmed. Every output is derived only from bars already pushed, so a
// backtest built on it cannot look ahead.
package chanlun

import "math"

// Bar is one closed candle, time in ms.
type Bar struct {
	Time                   int64
	Open, High, Low, Close float64
}

// merged is a candle after containment processing. hiIdx/loIdx are the raw
// bar indexes that set its extremes.
type merged struct {
	high, low    float64
	hiIdx, loIdx int
}

// Point is a fractal that ends one stroke and starts the next.
type Point struct {
	Top   bool
	Price float64
	Raw   int // raw bar index of the extreme
	m     int // merged bar index
}

// Pivot is a 中枢 built from segment-level moves. ZG/ZD come from its first
// three moves; GG/DD are the extremes of the moves that belong to it (the
// move that leaves it does not).
type Pivot struct {
	ZG, ZD, GG, DD float64
	Start, End     int // move indexes; End is the last move that touched it
	Enter          int // index of the point where the entering move starts
	Dir            int // +1 wholly above the previous pivot, -1 wholly below
	Count          int // consecutive pivots in Dir (a trend needs >= 2)
}

// Signal is a buy/sell point recognised at the close of bar Bar.
type Signal struct {
	Kind string // B1 B2 B3 S1 S2 S3
	Long bool
	Stop float64 // the structural level that invalidates the signal
	Big  int     // direction of the running higher-level move: +1, -1, 0 unknown
	Bar  int
}

// Event is what one pushed bar produced; both fields are usually nil.
type Event struct {
	Signal *Signal
	Seg    *Point // a segment end confirmed by this bar
}

// minGap is the merged-bar distance between the two fractals of a stroke
// (5 merged bars end to end).
const minGap = 4

type Engine struct {
	bars []Bar
	pos  []float64 // prefix sums of the positive MACD histogram
	neg  []float64 // prefix sums of the negative MACD histogram, as magnitudes
	fast float64
	slow float64
	dea  float64

	m   []merged
	pts []Point

	segs   []int   // pts indexes of confirmed segment ends; segs[0] starts the first
	segPts []Point // the same points, the input of the higher level
	big    []int   // segPts indexes of confirmed higher-level segment ends

	lastB1, lastS1 int // pts index of the latest 1st buy / sell, -1 if none
}

func New() *Engine {
	return &Engine{lastB1: -1, lastS1: -1, pos: []float64{0}, neg: []float64{0}}
}

// Points returns the stroke endpoints found so far.
func (e *Engine) Points() []Point { return e.pts }

// Segments returns the confirmed segment endpoints.
func (e *Engine) Segments() []Point { return e.segPts }

// BigSegments returns how many higher-level segment ends are confirmed.
func (e *Engine) BigSegments() int { return len(e.big) }

// Pivots returns the pivots of the confirmed segments.
func (e *Engine) Pivots() []Pivot { return e.pivots(e.segs) }

// Push adds one closed bar and reports what it confirmed.
func (e *Engine) Push(b Bar) Event {
	i := len(e.bars)
	e.bars = append(e.bars, b)
	e.macd(math.Log(b.Close), i)

	if !e.merge(b, i) {
		return Event{}
	}
	n := len(e.m)
	if n < 3 {
		return Event{}
	}
	// The bar before the one just appended can no longer change: test it.
	k := n - 2
	var f Point
	switch {
	case e.m[k].high > e.m[k-1].high && e.m[k].high > e.m[k+1].high:
		f = Point{Top: true, Price: e.m[k].high, Raw: e.m[k].hiIdx, m: k}
	case e.m[k].low < e.m[k-1].low && e.m[k].low < e.m[k+1].low:
		f = Point{Top: false, Price: e.m[k].low, Raw: e.m[k].loIdx, m: k}
	default:
		return Event{}
	}
	if !e.addFractal(f) || len(e.pts) < 2 {
		return Event{}
	}
	ev := Event{Seg: e.grow()}
	if s := e.signal(); s != nil {
		s.Bar = i
		ev.Signal = s
	}
	return ev
}

// macd runs on log prices so that areas years apart stay comparable.
func (e *Engine) macd(c float64, i int) {
	var h float64
	if i == 0 {
		e.fast, e.slow = c, c
	} else {
		e.fast += (c - e.fast) * 2 / 13
		e.slow += (c - e.slow) * 2 / 27
		dif := e.fast - e.slow
		e.dea += (dif - e.dea) * 2 / 10
		h = 2 * (dif - e.dea)
	}
	e.pos = append(e.pos, e.pos[i]+math.Max(h, 0))
	e.neg = append(e.neg, e.neg[i]+math.Max(-h, 0))
}

// force is the MACD histogram area between two raw bars on the move's side.
func (e *Engine) force(from, to int, up bool) float64 {
	if up {
		return e.pos[to+1] - e.pos[from]
	}
	return e.neg[to+1] - e.neg[from]
}

// merge applies containment processing and reports whether a new merged bar
// was appended.
func (e *Engine) merge(b Bar, i int) bool {
	n := len(e.m)
	if n == 0 {
		e.m = append(e.m, merged{b.High, b.Low, i, i})
		return false
	}
	last := &e.m[n-1]
	inside := b.High <= last.high && b.Low >= last.low
	outside := b.High >= last.high && b.Low <= last.low
	if !inside && !outside {
		e.m = append(e.m, merged{b.High, b.Low, i, i})
		return true
	}
	up := n < 2 || last.high > e.m[n-2].high
	if up {
		if b.High > last.high {
			last.high, last.hiIdx = b.High, i
		}
		if b.Low > last.low {
			last.low, last.loIdx = b.Low, i
		}
	} else {
		if b.High < last.high {
			last.high, last.hiIdx = b.High, i
		}
		if b.Low < last.low {
			last.low, last.loIdx = b.Low, i
		}
	}
	return false
}

// addFractal reports whether f changed the strokes: it either starts a new
// one or, being a more extreme fractal of the same type, extends the last.
func (e *Engine) addFractal(f Point) bool {
	n := len(e.pts)
	if n == 0 {
		e.pts = append(e.pts, f)
		return true
	}
	last := e.pts[n-1]
	if last.Top == f.Top {
		if (f.Top && f.Price > last.Price) || (!f.Top && f.Price < last.Price) {
			e.pts[n-1] = f
			return true
		}
		return false
	}
	if f.m-last.m < minGap {
		return false
	}
	if (f.Top && f.Price <= last.Price) || (!f.Top && f.Price >= last.Price) {
		return false
	}
	e.pts = append(e.pts, f)
	return true
}

// elem is one element of a characteristic sequence, in coordinates where the
// segment under test points up. lo0 is the low before containment merging.
type elem struct {
	hi, lo, lo0 float64
	at          int
}

// segEnd looks for the end of the segment that starts at pts[s] and returns
// the index of its last point, or -1 while it is not confirmed.
//
// The strokes against the segment form its characteristic sequence. After
// containment processing, a fractal in that sequence ends the segment at the
// fractal's extreme; when there is a gap between the fractal's first two
// elements, the opposite segment must first show a fractal of its own
// (second = true runs that check).
func segEnd(pts []Point, s int, second bool) int {
	sgn := 1.0
	if pts[s].Top {
		sgn = -1
	}
	base := sgn * pts[s].Price
	var seq []elem
	for j := s + 1; j+1 < len(pts); j += 2 {
		el := elem{hi: sgn * pts[j].Price, lo: sgn * pts[j+1].Price, at: j}
		el.lo0 = el.lo
		if second && el.lo < base {
			return -1 // price went back through the start: no turn here
		}
		if n := len(seq); n > 0 {
			// An element that makes a new extreme is never merged away: it
			// may be the one the segment ends on.
			if last := &seq[n-1]; el.hi <= last.hi && el.lo >= last.lo {
				last.lo = el.lo
				continue
			}
		}
		seq = append(seq, el)
		n := len(seq)
		if n < 3 {
			continue
		}
		a, b := seq[n-3], seq[n-2]
		if b.hi <= a.hi || b.hi <= el.hi {
			continue
		}
		if second || b.lo0 <= a.hi || segEnd(pts, b.at, true) >= 0 {
			return b.at
		}
	}
	return -1
}

// grow confirms whatever segments the latest stroke change completed, at both
// levels, and returns the newest confirmed segment end.
func (e *Engine) grow() *Point {
	if len(e.segs) == 0 {
		e.segs, e.segPts, e.big = []int{0}, []Point{e.pts[0]}, []int{0}
	}
	// Until the first segment is confirmed its start is only a guess: when
	// price runs through it, restart from the extreme reached before that.
	for len(e.segs) == 1 {
		s := e.segs[0]
		j := s + 2
		for ; j < len(e.pts); j += 2 {
			if (e.pts[s].Top && e.pts[j].Price > e.pts[s].Price) || (!e.pts[s].Top && e.pts[j].Price < e.pts[s].Price) {
				break
			}
		}
		if j >= len(e.pts) {
			break
		}
		best := s + 1
		for i := s + 3; i < j; i += 2 {
			if (e.pts[s].Top && e.pts[i].Price < e.pts[best].Price) || (!e.pts[s].Top && e.pts[i].Price > e.pts[best].Price) {
				best = i
			}
		}
		e.segs[0], e.segPts[0] = best, e.pts[best]
	}
	var last *Point
	for {
		end := segEnd(e.pts, e.segs[len(e.segs)-1], false)
		if end < 0 {
			break
		}
		e.segs = append(e.segs, end)
		e.segPts = append(e.segPts, e.pts[end])
		p := e.pts[end]
		last = &p
	}
	for last != nil {
		end := segEnd(e.segPts, e.big[len(e.big)-1], false)
		if end < 0 {
			break
		}
		e.big = append(e.big, end)
	}
	return last
}

// bigDir is the direction of the higher-level move still in progress.
func (e *Engine) bigDir() int {
	if len(e.big) < 2 {
		return 0
	}
	if e.segPts[e.big[len(e.big)-1]].Top {
		return -1
	}
	return 1
}

// moves returns the segment-level points up to now: the confirmed segment
// ends, then the best current reading of the unconfirmed tail, each tentative
// move running to the extreme reached since the previous one. The newest
// stroke point is always last.
func (e *Engine) moves() []int {
	mv := append(make([]int, 0, len(e.segs)+4), e.segs...)
	for cur := mv[len(mv)-1]; cur < len(e.pts)-1; {
		best := cur + 1
		for j := cur + 3; j < len(e.pts); j += 2 {
			if p := e.pts[j]; (p.Top && p.Price > e.pts[best].Price) || (!p.Top && p.Price < e.pts[best].Price) {
				best = j
			}
		}
		mv = append(mv, best)
		cur = best
	}
	return mv
}

// pivots builds the pivots of the moves between the given points. A pivot is
// three consecutive overlapping moves, the first of them running against the
// move that entered it.
func (e *Engine) pivots(mv []int) []Pivot {
	price := func(i int) float64 { return e.pts[mv[i]].Price }
	span := func(j int) (hi, lo float64) {
		a, b := price(j), price(j+1)
		return math.Max(a, b), math.Min(a, b)
	}
	var out []Pivot
	active := false
	seek := 1
	for j := 0; j+1 < len(mv); j++ {
		hi, lo := span(j)
		if active {
			z := &out[len(out)-1]
			if hi >= z.ZD && lo <= z.ZG {
				z.End = j
				z.GG = math.Max(z.GG, price(j))
				z.DD = math.Min(z.DD, price(j))
				continue
			}
			// The move never came back: the pivot is over and this move is
			// the first that may start the next one.
			active = false
			seek = j
		}
		if j < seek+2 || (j-seek)%2 != 0 {
			continue
		}
		z := Pivot{ZG: math.Inf(1), ZD: math.Inf(-1), GG: math.Inf(-1), DD: math.Inf(1), Start: j - 2, End: j, Count: 1}
		for s := j - 2; s <= j; s++ {
			h, l := span(s)
			z.ZG, z.ZD = math.Min(z.ZG, h), math.Max(z.ZD, l)
			z.GG, z.DD = math.Max(z.GG, price(s)), math.Min(z.DD, price(s))
		}
		if z.ZG <= z.ZD {
			continue
		}
		z.Enter = z.Start - 1
		if n := len(out); n > 0 {
			z.Enter = out[n-1].End
		}
		out = append(out, z)
		active = true
	}
	for i := 1; i < len(out); i++ {
		z, prev := &out[i], out[i-1]
		switch {
		case z.DD > prev.GG:
			z.Dir = 1
		case z.GG < prev.DD:
			z.Dir = -1
		default:
			continue
		}
		z.Count = 2
		if prev.Dir == z.Dir {
			z.Count = prev.Count + 1
		}
	}
	return out
}

// signal classifies the newest stroke point against the segment structure.
func (e *Engine) signal() *Signal {
	if len(e.segs) < 3 {
		return nil
	}
	mv := e.moves()
	k := len(mv) - 1 // moves; the last one ends at the newest point
	// Tentative moves only count once they are segment sized.
	for i := len(e.segs) - 1; i < k; i++ {
		if mv[i+1]-mv[i] < 3 {
			return nil
		}
	}
	n := len(e.pts)
	p := e.pts[n-1]
	long := !p.Top
	sg := 1.0
	name := "B"
	if !long {
		sg, name = -1, "S"
	}
	sig := func(kind string, stop float64) *Signal {
		return &Signal{Kind: name + kind, Long: long, Stop: stop, Big: e.bigDir()}
	}

	// The last move is judged against the pivots formed before it.
	if piv := e.pivots(mv[:k]); len(piv) > 0 && piv[len(piv)-1].End == k-2 {
		z := piv[len(piv)-1]
		edge, far := z.ZG, z.DD
		if !long {
			edge, far = z.ZD, z.GG
		}
		// 1st: a trend of >= 2 pivots makes a new extreme on less force than
		// the move that entered its last pivot (趋势背驰), and inside that
		// last move the final stroke is weaker too (区间套).
		if z.Dir == int(-sg) && z.Count >= 2 && sg*p.Price < sg*far && e.pts[mv[z.Start]].Top == p.Top {
			c := e.force(e.pts[mv[k-1]].Raw, p.Raw, !long)
			b := e.force(e.pts[mv[z.Enter]].Raw, e.pts[mv[z.Start]].Raw, !long)
			in1 := e.force(e.pts[n-2].Raw, p.Raw, !long)
			in0 := e.force(e.pts[n-4].Raw, e.pts[n-3].Raw, !long)
			if c < b && in1 < in0 {
				if long {
					e.lastB1 = n - 1
				} else {
					e.lastS1 = n - 1
				}
				return sig("1", p.Price)
			}
		}
		// 3rd: a move left a completed pivot and the pullback stayed out.
		if z.End-z.Start >= 3 && sg*p.Price > sg*edge {
			return sig("3", edge)
		}
	}
	// 2nd: the first pullback after a 1st point holds that point's extreme.
	ref := e.lastB1
	if !long {
		ref = e.lastS1
	}
	if k >= 2 && ref >= 0 && mv[k-2] == ref && sg*p.Price > sg*e.pts[ref].Price {
		return sig("2", e.pts[ref].Price)
	}
	return nil
}

package api

import (
	"math"
	"testing"
)

func TestAccBucketFinalize(t *testing.T) {
	b := accBucket{Scored: 49, Matured: 44, Hit: 25, Miss: 16, Invalid: 3}
	b.finalize()
	if b.Valid != 41 {
		t.Fatalf("valid = %d, want 41", b.Valid)
	}
	if b.Pending != 5 {
		t.Fatalf("pending = %d, want 5", b.Pending)
	}
	want := 25.0 / 41.0 * 100
	if math.Abs(b.HitRate-want) > 1e-9 {
		t.Fatalf("hit_rate = %v, want %v", b.HitRate, want)
	}
}

func TestAccBucketNoValid(t *testing.T) {
	b := accBucket{Scored: 3, Matured: 2, Invalid: 2}
	b.finalize()
	if b.Valid != 0 || b.HitRate != 0 {
		t.Fatalf("empty-valid hit_rate must stay 0, got valid=%d rate=%v", b.Valid, b.HitRate)
	}
}

func TestReliabilityOf(t *testing.T) {
	cases := []struct {
		samples int
		hit     float64
		fresh   bool
		cons    float64
		want    string
	}{
		{40, 60, true, 60, "A"},
		{12, 52, false, 40, "B"},
		{5, 30, false, 20, "C"},
		{1, 90, true, 99, "D"},
	}
	for _, c := range cases {
		if got := reliabilityOf(c.samples, c.hit, c.fresh, c.cons); got != c.want {
			t.Fatalf("reliabilityOf(%d,%v,%v,%v)=%s want %s", c.samples, c.hit, c.fresh, c.cons, got, c.want)
		}
	}
}

func TestClampPct(t *testing.T) {
	if clampPct(-5) != 0 || clampPct(150) != 100 || clampPct(42) != 42 {
		t.Fatal("clampPct wrong")
	}
}

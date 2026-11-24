package copytrade

import (
	"math"
	"testing"
)

func TestDirectionEvidenceDoesNotInflateTinySamplesOrDropLosses(t *testing.T) {
	e := MeasureDirection([]float64{.1, .1, math.NaN()})
	if e.Samples != 2 || DirectionReady(e) {
		t.Fatalf("tiny sample %+v", e)
	}
	e = MeasureDirection([]float64{0, -.1, .1})
	if e.Samples != 3 || e.Hits != 1 {
		t.Fatalf("flat or loss removed %+v", e)
	}
	good := make([]float64, 30)
	for i := range good {
		good[i] = .01
	}
	if !DirectionReady(MeasureDirection(good)) {
		t.Fatal("consistent sample rejected")
	}
}

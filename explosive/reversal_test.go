package explosive

import "testing"

func bars(closes []float64, vols []float64) []Kline {
	out := make([]Kline, len(closes))
	for i, c := range closes {
		v := 1000.0
		if vols != nil {
			v = vols[i]
		}
		out[i] = Kline{Time: int64(i) * 3600000, Open: c, High: c + 1, Low: c - 1, Close: c, Volume: v}
	}
	return out
}

func TestDetectReversalAfterDecline(t *testing.T) {
	closes := make([]float64, 0, 60)
	vols := make([]float64, 0, 60)
	for i := 0; i < 50; i++ {
		closes = append(closes, 100-float64(i)*0.6)
		vols = append(vols, 1000)
	}
	for i := 0; i < 10; i++ {
		closes = append(closes, 71+float64(i)*2.2)
		vols = append(vols, 1000)
	}
	vols[len(vols)-1] = 4000
	ok, score, reasons, neck := detectReversal(bars(closes, vols))
	if !ok || score < 3 {
		t.Fatalf("expected reversal, got ok=%v score=%d reasons=%v", ok, score, reasons)
	}
	if neck <= 0 {
		t.Fatalf("expected a neckline, got %v", neck)
	}
}

func TestDetectReversalIgnoresSteadyRise(t *testing.T) {
	closes := make([]float64, 0, 60)
	for i := 0; i < 60; i++ {
		closes = append(closes, 100+float64(i)*0.5)
	}
	ok, _, _, _ := detectReversal(bars(closes, nil))
	if ok {
		t.Fatal("steady rise should not be flagged as a reversal")
	}
}

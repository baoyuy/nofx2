package api

import (
	"math"
	"testing"

	"nofx/trader/binance"
)

func TestParseMarketAnalysisHandlesFencesThinkAndClamps(t *testing.T) {
	raw := "<think>long reasoning {not json}</think>\n```json\n" +
		`{"signal":"LONG","confidence":140,"entry":100,"stop_loss":95,"take_profit":110,"leverage":50,"summary":"ok","reasons":["a"],"risks":["b"]}` +
		"\n```"
	a, err := parseMarketAnalysis(raw)
	if err != nil {
		t.Fatal(err)
	}
	if a.Signal != "long" || a.Confidence != 100 || a.Leverage != 20 || a.StopLoss != 95 {
		t.Fatalf("unexpected parse: %+v", a)
	}
}

func TestParseMarketAnalysisUnknownSignalBecomesWait(t *testing.T) {
	a, err := parseMarketAnalysis(`{"signal":"buy now","confidence":30}`)
	if err != nil || a.Signal != "wait" || a.Leverage != 1 {
		t.Fatalf("got %+v, %v", a, err)
	}
	if _, err := parseMarketAnalysis("no json here"); err == nil {
		t.Fatal("expected error for non-JSON reply")
	}
}

func TestComputeIndicatorsOnRisingSeries(t *testing.T) {
	var k []binance.MarketKline
	for i := 0; i < 120; i++ {
		p := 100 + float64(i)
		k = append(k, binance.MarketKline{Time: int64(i * 60), Open: p - 0.5, High: p + 1, Low: p - 1, Close: p, Volume: 10})
	}
	s := computeIndicators(k)
	if math.Abs(s.MA7-(219+218+217+216+215+214+213)/7.0) > 1e-9 {
		t.Fatalf("MA7 = %f", s.MA7)
	}
	if s.RSI14 != 100 || s.MACD <= 0 || s.K <= 80 || s.BollUp <= s.BollMid || s.ATR14 <= 0 {
		t.Fatalf("unexpected indicators on uptrend: %+v", s)
	}
}

func TestMarketSymbolNormalization(t *testing.T) {
	for in, want := range map[string]string{"btc": "BTCUSDT", "eth/usdt": "ETHUSDT", " sol-usdt ": "SOLUSDT", "BTCUSDT": "BTCUSDT"} {
		if got := binance.NormalizeUSDTSymbol(in); got != want {
			t.Fatalf("%q -> %q, want %q", in, got, want)
		}
	}
}

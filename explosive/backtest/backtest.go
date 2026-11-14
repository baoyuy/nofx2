// Package backtest rebuilds the explosive board from historical candles and
// measures what happened next, so the ranking can be calibrated on data
// instead of guesses.
package backtest

import (
	"math"
	"time"
)

// Bar is one closed 5m candle, times in ms.
type Bar struct {
	Time   int64
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
	Quote  float64 // quote-asset volume (USDT)
	Taker  float64 // taker buy base volume
}

// Beijing is the calendar day the board uses.
var Beijing = time.FixedZone("CST", 8*3600)

// DayStart returns the Beijing-day start (ms) of a timestamp.
func DayStart(ms int64) int64 {
	t := time.UnixMilli(ms).In(Beijing)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, Beijing).UnixMilli()
}

// Row is one hour's reading of one coin, with what the rest of the day did.
type Row struct {
	Symbol string  `json:"symbol"`
	Day    int64   `json:"day"`  // Beijing day start, ms
	Hour   int     `json:"hour"` // hours since the day started
	Price  float64 `json:"price"`
	// Features.
	DayGain     float64 `json:"day_gain"`
	ElapsedH    float64 `json:"elapsed_h"`
	VolSlope    float64 `json:"vol_slope"`
	HighDist    float64 `json:"high_dist"`
	Change1h    float64 `json:"change_1h"`
	Change4h    float64 `json:"change_4h"`
	ATRPct      float64 `json:"atr_pct"`
	TakerRatio  float64 `json:"taker_ratio"`
	Efficiency  float64 `json:"efficiency"`
	RelStrength float64 `json:"rel_strength"`
	QuoteVol    float64 `json:"quote_vol"`
	// Outcome to the Beijing-day close.
	Remaining float64 `json:"remaining"`
	MaxDD     float64 `json:"max_dd"`
}

// CloseAt returns the close of the last bar closing at or before ms.
func closeAt(bars []Bar, ms int64) (float64, int) {
	idx := -1
	for i := range bars {
		if bars[i].Time+5*60*1000 <= ms {
			idx = i
		} else {
			break
		}
	}
	if idx < 0 {
		return 0, -1
	}
	return bars[idx].Close, idx
}

// BuildRows turns one symbol's 5m bars into hourly readings, one per Beijing
// day. btcDay maps a day start to BTC's own gain that day, for relative
// strength.
func BuildRows(symbol string, bars []Bar, btcDay map[int64]float64, minGain, minQuote float64) []Row {
	if len(bars) == 0 {
		return nil
	}
	byDay := map[int64][]Bar{}
	for _, b := range bars {
		d := DayStart(b.Time)
		byDay[d] = append(byDay[d], b)
	}
	days := make([]int64, 0, len(byDay))
	for d := range byDay {
		days = append(days, d)
	}
	// Deterministic order.
	for i := 0; i < len(days); i++ {
		for j := i + 1; j < len(days); j++ {
			if days[j] < days[i] {
				days[i], days[j] = days[j], days[i]
			}
		}
	}

	var out []Row
	for _, day := range days {
		dayBars := byDay[day]
		// Only complete days: today's partial close would make a fake label.
		if len(dayBars) < 60 || dayBars[len(dayBars)-1].Time+5*60*1000 < day+24*3600*1000 {
			continue
		}
		open := dayBars[0].Open
		if open <= 0 {
			continue
		}
		dayClose := dayBars[len(dayBars)-1].Close
		btc, _ := btcDay[day]
		for hour := 1; hour <= 21; hour++ {
			price, idx := closeAt(dayBars, day+int64(hour)*3600*1000)
			if idx < 24 || price <= 0 {
				continue
			}
			gain := price/open - 1
			if gain < minGain {
				continue
			}
			quote := 0.0
			for i := 0; i <= idx; i++ {
				quote += dayBars[i].Quote
			}
			if quote < minQuote {
				continue
			}
			high := 0.0
			for i := 0; i <= idx; i++ {
				high = math.Max(high, dayBars[i].High)
			}
			row := Row{
				Symbol: symbol, Day: day, Hour: hour, Price: price,
				DayGain: gain, ElapsedH: float64(hour), QuoteVol: quote,
				RelStrength: gain - btc,
			}
			if high > 0 {
				row.HighDist = (high - price) / high * 100
			}
			// Volume slope: last 3h against the 3h before, when both exist.
			if idx >= 72 {
				var recent, prev float64
				for i := idx - 35; i <= idx; i++ {
					recent += dayBars[i].Volume
				}
				for i := idx - 71; i <= idx-36; i++ {
					prev += dayBars[i].Volume
				}
				if prev > 0 {
					row.VolSlope = math.Max(-1, math.Min(3, recent/prev-1))
				}
			}
			if idx >= 12 {
				if p := dayBars[idx-12].Close; p > 0 {
					row.Change1h = price/p - 1
				}
			}
			if idx >= 48 {
				if p := dayBars[idx-48].Close; p > 0 {
					row.Change4h = price/p - 1
				}
			}
			// ATR(14) as percent of price.
			var tr float64
			for i := idx - 13; i <= idx; i++ {
				v := dayBars[i].High - dayBars[i].Low
				if prev := dayBars[i-1].Close; prev > 0 {
					v = math.Max(v, math.Max(math.Abs(dayBars[i].High-prev), math.Abs(dayBars[i].Low-prev)))
				}
				tr += v
			}
			row.ATRPct = tr / 14 / price * 100
			// Taker buy share and path efficiency over the last hour.
			var buy, total, path float64
			for i := idx - 11; i <= idx; i++ {
				buy += dayBars[i].Taker
				total += dayBars[i].Volume
				if i > idx-11 {
					path += math.Abs(dayBars[i].Close - dayBars[i-1].Close)
				}
			}
			if total > 0 && buy > 0 {
				row.TakerRatio = math.Max(0, math.Min(1, buy/total))
			}
			if path > 0 {
				row.Efficiency = math.Min(1, math.Abs(price-dayBars[idx-11].Close)/path)
			}
			row.Remaining = dayClose/price - 1
			dd := 0.0
			for i := idx + 1; i < len(dayBars); i++ {
				if dayBars[i].Low > 0 {
					dd = math.Min(dd, dayBars[i].Low/price-1)
				}
			}
			row.MaxDD = dd
			out = append(out, row)
		}
	}
	return out
}

// DayGain of one symbol over one Beijing day, from its bars.
func DayGain(bars []Bar, day int64) float64 {
	var first, last float64
	for _, b := range bars {
		if DayStart(b.Time) != day {
			continue
		}
		if first == 0 {
			first = b.Open
		}
		last = b.Close
	}
	if first <= 0 {
		return 0
	}
	return last/first - 1
}

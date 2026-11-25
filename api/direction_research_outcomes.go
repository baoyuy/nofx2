package api

import (
	"context"
	"encoding/json"
	"nofx/logger"
	"nofx/trader/binance"
	"time"
)

type directionObserved struct {
	Symbol     string  `json:"symbol"`
	Direction  string  `json:"direction"`
	Return     float64 `json:"direction_return"`
	NetReturn  float64 `json:"net_return_assumed_cost"`
	MaxAdverse float64 `json:"max_adverse"`
	EndAt      int64   `json:"end_at"`
}

// Require exact, complete 15m bars inside the four-hour window. The first
// partial bar is excluded: its high/low may predate the selection. This leaves
// that partial interval unverified, which is exposed in the stored result.
func observeDirection(p directionPick, at int64, bars []binance.MarketKline) (directionObserved, bool) {
	o := directionObserved{Symbol: p.Symbol, Direction: p.Direction}
	if p.EntryPrice <= 0 || (p.Direction != "long" && p.Direction != "short") {
		return o, false
	}
	start := (at/1000 + 899) / 900 * 900
	end := (at/1000 + 4*3600) / 900 * 900
	expected := (end - start) / 900
	if expected < 15 {
		return o, false
	}
	var count int64
	var last, exit int64
	worst := 0.0
	price := 0.0
	for _, b := range bars {
		if b.Time < start || b.Time+900 > end {
			continue
		}
		if b.Time != start+count*900 || b.Close <= 0 || b.High < b.Low {
			return o, false
		}
		adverse := 1 - b.Low/p.EntryPrice
		if p.Direction == "short" {
			adverse = b.High/p.EntryPrice - 1
		}
		if adverse > worst {
			worst = adverse
		}
		count++
		last = b.Time
		price = b.Close
		exit = b.Time + 900
	}
	if count != expected || last+900 != end {
		return o, false
	}
	r := price/p.EntryPrice - 1
	if p.Direction == "short" {
		r = -r
	}
	o.Return = r
	o.NetReturn = r - .002
	o.MaxAdverse = worst
	o.EndAt = exit * 1000
	return o, true
}

// directionGiveUpMs is how long a research record may wait for candles
// before it is finished with the picks that could be observed. 15m candles
// for a 4h window are normally available within minutes; a day is generous.
const directionGiveUpMs = int64(24 * 3600 * 1000)

func (s *Server) evaluateDirectionResearch(ctx context.Context) {
	records, err := s.store.CopyTrade().PendingDirectionResearch(time.Now().UnixMilli())
	if err != nil {
		logger.Warnf("direction research pending: %v", err)
		return
	}
	cache := map[string][]binance.MarketKline{}
	for _, record := range records {
		var payload struct {
			Long  []directionPick `json:"long"`
			Short []directionPick `json:"short"`
		}
		if json.Unmarshal([]byte(record.Payload), &payload) != nil {
			continue
		}
		out := []directionObserved{}
		complete := true
		// A record that can never complete (a pick's candles are gone, e.g.
		// a delisted coin) used to stay pending forever, and up to 200 such
		// records, oldest first, could block every newer evaluation. Past the
		// give-up age, finish it with whatever picks could be observed and
		// mark the rest unobservable, instead of retrying it every tick.
		expired := time.Now().UnixMilli()-record.At > directionGiveUpMs
		var unobserved []string
		for _, p := range append(payload.Long, payload.Short...) {
			bars, ok := cache[p.Symbol]
			if !ok {
				bars, err = binance.GetPublicKlines(ctx, p.Symbol, "15m", 1500)
				if err != nil {
					if expired {
						unobserved = append(unobserved, p.Symbol)
						continue
					}
					complete = false
					break
				}
				cache[p.Symbol] = bars
			}
			result, ok := observeDirection(p, record.At, bars)
			if !ok {
				if expired {
					unobserved = append(unobserved, p.Symbol)
					continue
				}
				complete = false
				break
			}
			out = append(out, result)
		}
		if !complete {
			continue
		}
		data, err := json.Marshal(map[string]any{"items": out, "unobservable": unobserved, "cost_assumption": .002, "partial_start_interval_unverified": true, "endpoint_tolerance_seconds": 900, "version": directionRuleVersion})
		if err == nil {
			if err = s.store.CopyTrade().FinishDirectionResearch(record.ID, string(data)); err != nil {
				logger.Warnf("direction research save: %v", err)
			}
		}
	}
}

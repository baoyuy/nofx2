package api

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"time"

	"nofx/copytrade"
	"nofx/kernel"
	"nofx/trader/binance"
)

// stableRanksForProgram builds the calibrated 4h "stable" ranking used by the
// program-stable (no-AI) strategy: only volume-in-scope coins with a fresh AI
// score and a clear long/short direction, scored by the same composite the
// 4h-stable ranking uses.
func jsonUnmarshalPayload(payload string, v any) error { return json.Unmarshal([]byte(payload), v) }

func (s *Server) stableRanksForProgram() []kernel.StableRank {
	if s.copyTrade == nil {
		return nil
	}
	_, uid, err := s.resolveScoreModelAny("")
	if err != nil {
		return nil
	}
	cfg := s.loadCoinScoreAuto()
	minVol := cfg.MinQuoteVolume
	if minVol <= 0 {
		minVol = coinScoreDefaultMinVolume
	}
	ttlMin := cfg.PoolTTLMinutes
	if ttlMin < 1 {
		ttlMin = 12
	}
	ttlMs := int64(ttlMin) * 60 * 1000
	neutral := coinScoreNeutralMovePct / 100.0

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	now := time.Now()
	nowMs := now.UnixMilli()
	sums := copytrade.Symbols(s.copyTrade.Snapshots())
	if vols, e := binance.PublicQuoteVolumes(ctx); e == nil && len(vols) > 0 {
		copytrade.AttachRanks(sums, vols)
	}
	rows, _ := s.store.CopyTrade().ListCoinScores(uid)
	type ai struct {
		at    int64
		bias  string
		score float64
	}
	am := map[string]ai{}
	for _, r := range rows {
		var res coinScoreResult
		if jsonUnmarshalPayload(r.Payload, &res) != nil || res.Analysis == nil {
			continue
		}
		if _, ok := am[r.Symbol]; ok {
			continue
		}
		am[r.Symbol] = ai{r.AnalyzedAt, res.Analysis.Bias, res.Analysis.Score}
	}
	out := make([]kernel.StableRank, 0, len(sums))
	for i := range sums {
		sum := &sums[i]
		if minVol > 0 && (sum.Volume24h <= 0 || sum.Volume24h < minVol) {
			continue
		}
		a, ok := am[sum.Symbol]
		if !ok || nowMs-a.at > ttlMs {
			continue
		}
		dir := a.bias
		if dir != "long" && dir != "short" {
			continue
		}
		// stable components (same as the 4h-stable ranking)
		aiRows, _ := s.store.CopyTrade().RecentMatureScoreOutcomes(uid, sum.Symbol, now.Add(-30*24*time.Hour).UnixMilli())
		tradeRows, _ := s.store.CopyTrade().RecentMatureSignals(sum.Symbol, now.Add(-30*24*time.Hour).Unix())
		var ah, an, th, tn int
		for _, o := range aiRows {
			if (o.Bias != "long" && o.Bias != "short") || o.Ret4h == nil {
				continue
			}
			if math.Abs(*o.Ret4h) < neutral {
				continue
			}
			an++
			if *o.Ret4h > 0 {
				ah++
			}
		}
		for _, sig := range tradeRows {
			if (sig.Direction != "long" && sig.Direction != "short") || sig.Ret4h == nil {
				continue
			}
			ex := *sig.Ret4h
			if sig.Bench4h != nil {
				ex -= *sig.Bench4h
			}
			tn++
			if (sig.Direction == "long" && ex > 0) || (sig.Direction == "short" && ex < 0) {
				th++
			}
		}
		risk := float64(sum.Liquidated+sum.Long.Trapped+sum.Short.Trapped) / float64(max(1, sum.Long.Traders+sum.Short.Traders))
		fresh := copytrade.StableFresh(a.at, now)
		sd := copytrade.StableScore(ah, an, th, tn, dir, risk, fresh)
		fourH := clampPct(sd.Accuracy * 100)
		histHit := clampPct(sd.AIAccuracy * 100)
		dirQ := clampPct(sum.Rank.Score)
		consensus := clampPct(sum.Confidence.Score)
		freshPct := 0.0
		if sd.Fresh {
			freshPct = 100
		}
		ded := 20 * sd.Risk
		if sd.AI_Samples < 4 {
			ded += 10
		}
		if sum.Confidence.Agreement < 0.5 {
			ded += 10
		}
		comp := clampPct(.30*fourH + .25*dirQ + .20*histHit + .15*freshPct + .10*consensus - ded)
		price := 0.0
		if tk, e := binance.GetPublicTicker(ctx, sum.Symbol); e == nil && tk != nil {
			price = tk.LastPrice
		}
		out = append(out, kernel.StableRank{Symbol: sum.Symbol, Direction: dir, Stable: comp, AI: clampPct(a.score), Price: price})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Stable > out[j].Stable })
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

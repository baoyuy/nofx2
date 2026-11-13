package explosive

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"sync"
	"time"

	"nofx/logger"
	"nofx/store"
)

const (
	// snapshotEvery is how often the board is recorded. Fifteen minutes keeps
	// neighbouring samples from being pure copies of each other.
	snapshotEvery = 15 * time.Minute
	// evalEvery is how often forward returns are filled in.
	evalEvery = 5 * time.Minute
	// signalRetention keeps a quarter of history for tuning.
	signalRetention = 90 * 24 * time.Hour
	// accuracyCacheFor: the page asks often, the record changes hourly.
	accuracyCacheFor = 10 * time.Minute
	// sustainedFloor is the drawdown a "sustained" call may not break.
	sustainedFloor = -0.03
	// scoreVersion marks the weight set that produced the stored scores.
	scoreVersion = 1
	// evalBatch bounds how many rows one pass fills in.
	evalBatch = 80
)

// Recorder writes the board to the database and fills in what happened next.
// It never changes the ranking: it is the record the ranking is judged on.
type Recorder struct {
	store *store.ExplosiveStore
	now   func() time.Time
	// board and rangeBars are injectable for tests; the defaults are the
	// live screener.
	board     func(ctx context.Context) (Board, error)
	rangeBars func(ctx context.Context, symbol, interval string, startMs, endMs int64) ([]Kline, error)

	mu       sync.Mutex
	lastEval time.Time
	acc      Accuracy
	accAt    time.Time
}

// NewRecorder returns a recorder backed by st.
func NewRecorder(st *store.ExplosiveStore) *Recorder {
	return &Recorder{
		store: st,
		now:   time.Now,
		board: func(ctx context.Context) (Board, error) { return Shared.Board(ctx, true) },
		rangeBars: func(ctx context.Context, symbol, interval string, startMs, endMs int64) ([]Kline, error) {
			return Shared.KlinesRange(ctx, symbol, interval, startMs, endMs)
		},
	}
}

// Run records and evaluates until ctx ends.
func (r *Recorder) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		r.tick(ctx)
	}
}

func (r *Recorder) tick(ctx context.Context) {
	now := r.now()
	if err := r.snapshot(ctx, now); err != nil {
		logger.Warnf("explosive: record snapshot: %v", err)
	}
	r.mu.Lock()
	due := now.Sub(r.lastEval) >= evalEvery
	if due {
		r.lastEval = now
	}
	r.mu.Unlock()
	if due {
		if err := r.evaluate(ctx, now); err != nil {
			logger.Warnf("explosive: evaluate signals: %v", err)
		}
	}
	if now.Hour() == 0 && now.Minute() < 2 {
		_ = r.store.PruneSignals(now.Add(-signalRetention).Unix())
	}
}

// snapshot stores one bucket's board, once.
func (r *Recorder) snapshot(ctx context.Context, now time.Time) error {
	bucket := now.Truncate(snapshotEvery).Unix()
	done, err := r.store.HasSignals(bucket)
	if err != nil || done {
		return err
	}
	board, err := r.board(ctx)
	if err != nil {
		return err
	}
	if len(board.Rows) == 0 {
		return nil
	}
	rows := make([]store.ExplosiveSignal, 0, len(board.Rows))
	for i, row := range board.Rows {
		tags, _ := json.Marshal(row.Tags)
		rows = append(rows, store.ExplosiveSignal{
			TS: bucket, Symbol: row.Symbol, Rank: i + 1, Price: row.Price,
			Score: row.Score, Strength: row.Strength, Risk: row.Risk, ScoreVersion: scoreVersion,
			Change24h: row.Change24h, Change5m: row.Change5m, Change15m: row.Change15m,
			Change1h: row.Change1h, Change4h: row.Change4h,
			VolRatio: row.VolRatio, OIChange1h: row.OIChange1h, Funding: row.Funding,
			ATRPct: row.ATRPct, DistanceHigh: row.DistanceHigh,
			EMA20: row.EMA20, EMA60: row.EMA60, EMA120: row.EMA120, EMASlope: row.EMASlope,
			TakerBuyRatio: row.TakerBuyRatio, RiseEfficiency: row.RiseEfficiency,
			Tags: string(tags), State: string(row.State),
		})
	}
	if err := r.store.SaveSignals(rows); err != nil {
		return err
	}
	logger.Infof("explosive: recorded %d signals for %s", len(rows), now.Format("15:04"))
	return nil
}

// evaluate fills in the 1h, 4h and 24h outcome of recorded rows.
func (r *Recorder) evaluate(ctx context.Context, now time.Time) error {
	horizons := []struct {
		name string
		d    time.Duration
	}{{"1h", time.Hour}, {"4h", 4 * time.Hour}, {"24h", 24 * time.Hour}}

	// One BTC fetch serves every row in this pass.
	bench, _ := r.rangeBars(ctx, "BTCUSDT", "5m",
		now.Add(-26*time.Hour).UnixMilli(), now.UnixMilli())
	benchClose := map[int64]float64{}
	for _, b := range bench {
		benchClose[b.Time] = b.Close
	}

	for _, h := range horizons {
		rows, err := r.store.DueSignals(h.name, now.Add(-h.d).Unix(), evalBatch)
		if err != nil {
			return err
		}
		for i := range rows {
			row := &rows[i]
			endMs := (row.TS + int64(h.d/time.Second)) * 1000
			bars, err := r.rangeBars(ctx, row.Symbol, "5m", row.TS*1000, endMs)
			if err != nil || len(bars) == 0 {
				continue
			}
			last := bars[len(bars)-1]
			if last.Close <= 0 || row.Price <= 0 {
				continue
			}
			ret := last.Close/row.Price - 1
			dd := 0.0
			for _, b := range bars {
				if b.Low > 0 {
					dd = math.Min(dd, b.Low/row.Price-1)
				}
			}
			benchRet := 0.0
			if from, ok := benchClose[nearestBar(benchClose, row.TS*1000)]; ok {
				if to, ok := benchClose[nearestBar(benchClose, endMs)]; ok && from > 0 {
					benchRet = to/from - 1
				}
			}
			switch h.name {
			case "1h":
				row.Ret1h, row.Bench1h, row.MaxDD1h, row.Done1h = &ret, &benchRet, &dd, true
			case "4h":
				row.Ret4h, row.Bench4h, row.MaxDD4h, row.Done4h = &ret, &benchRet, &dd, true
			case "24h":
				row.Ret24h, row.Bench24h, row.MaxDD24h, row.Done24h = &ret, &benchRet, &dd, true
			}
			if err := r.store.UpdateSignal(row); err != nil {
				return err
			}
		}
	}
	return nil
}

// nearestBar returns the open time of the last bar at or before ts, capped to
// the latest known one, so a slightly offset timestamp still resolves.
func nearestBar(closes map[int64]float64, ts int64) int64 {
	best := int64(-1)
	for t := range closes {
		if t <= ts && t > best {
			best = t
		}
	}
	if best >= 0 {
		return best
	}
	for t := range closes {
		if t > best {
			best = t
		}
	}
	return best
}

// BandStat is one score band's measured outcome.
type BandStat struct {
	Band    string  `json:"band"`
	Samples int     `json:"samples"`
	Up1h    float64 `json:"up_1h"`
	Up4h    float64 `json:"up_4h"`
	Up24h   float64 `json:"up_24h"`
	// Beat4h is the share that beat BTC over the same four hours.
	Beat4h      float64 `json:"beat_4h"`
	AvgExcess4h float64 `json:"avg_excess_4h"`
	AvgDD4h     float64 `json:"avg_dd_4h"`
	// Sustained is the share that rose over 4h without a 3% drawdown.
	Sustained float64 `json:"sustained"`
}

// TagStat is one signal tag's measured outcome.
type TagStat struct {
	Tag       string  `json:"tag"`
	Samples   int     `json:"samples"`
	Up4h      float64 `json:"up_4h"`
	Sustained float64 `json:"sustained"`
}

// Accuracy is the board's measured record.
type Accuracy struct {
	Days      int        `json:"days"`
	Total     int        `json:"total"`
	Bands     []BandStat `json:"bands"`
	Top       []BandStat `json:"top"`
	Tags      []TagStat  `json:"tags"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type accAcc struct {
	n, up1, up4, up24, beat4, sustained int
	excess, dd                          float64
}

func (a *accAcc) add(row store.ExplosiveSignal) {
	if row.Ret4h != nil {
		a.n++
		if *row.Ret4h > 0 {
			a.up4++
		}
		if row.Bench4h != nil {
			if *row.Ret4h-*row.Bench4h > 0 {
				a.beat4++
			}
			a.excess += *row.Ret4h - *row.Bench4h
		}
		if row.MaxDD4h != nil {
			a.dd += *row.MaxDD4h
		}
		if *row.Ret4h > 0 && row.MaxDD4h != nil && *row.MaxDD4h > sustainedFloor {
			a.sustained++
		}
	}
	if row.Ret1h != nil && *row.Ret1h > 0 {
		a.up1++
	}
	if row.Ret24h != nil && *row.Ret24h > 0 {
		a.up24++
	}
}

func (a *accAcc) stat(band string) BandStat {
	s := BandStat{Band: band, Samples: a.n}
	if a.n == 0 {
		return s
	}
	s.Up1h = float64(a.up1) / float64(a.n)
	s.Up4h = float64(a.up4) / float64(a.n)
	s.Up24h = float64(a.up24) / float64(a.n)
	s.Beat4h = float64(a.beat4) / float64(a.n)
	s.AvgExcess4h = a.excess / float64(a.n)
	s.AvgDD4h = a.dd / float64(a.n)
	s.Sustained = float64(a.sustained) / float64(a.n)
	return s
}

func strengthBand(strength float64) string {
	switch {
	case strength >= 80:
		return "≥80"
	case strength >= 60:
		return "60-80"
	case strength >= 40:
		return "40-60"
	default:
		return "<40"
	}
}

// Accuracy aggregates the stored record. Results are cached briefly.
func (r *Recorder) Accuracy(days int, now time.Time) (Accuracy, error) {
	if days <= 0 || days > 180 {
		days = 30
	}
	r.mu.Lock()
	if !r.accAt.IsZero() && now.Sub(r.accAt) < accuracyCacheFor && r.acc.Days == days {
		acc := r.acc
		r.mu.Unlock()
		return acc, nil
	}
	r.mu.Unlock()

	rows, err := r.store.SignalsSince(now.Add(-time.Duration(days) * 24 * time.Hour).Unix())
	if err != nil {
		return Accuracy{}, err
	}
	bands := map[string]*accAcc{"≥80": {}, "60-80": {}, "40-60": {}, "<40": {}}
	top, rest := &accAcc{}, &accAcc{}
	tags := map[string]*accAcc{}
	total := 0
	for _, row := range rows {
		if row.Ret4h == nil && row.Ret1h == nil {
			continue
		}
		total++
		bands[strengthBand(row.Strength)].add(row)
		if row.Rank <= 5 {
			top.add(row)
		} else {
			rest.add(row)
		}
		var rowTags []string
		if json.Unmarshal([]byte(row.Tags), &rowTags) == nil {
			for _, t := range rowTags {
				if tags[t] == nil {
					tags[t] = &accAcc{}
				}
				tags[t].add(row)
			}
		}
	}
	acc := Accuracy{Days: days, Total: total, UpdatedAt: now}
	for _, b := range []string{"≥80", "60-80", "40-60", "<40"} {
		acc.Bands = append(acc.Bands, bands[b].stat(b))
	}
	acc.Top = []BandStat{top.stat("top5"), rest.stat("rest")}
	for t, a := range tags {
		s := a.stat(t)
		acc.Tags = append(acc.Tags, TagStat{Tag: t, Samples: s.Samples, Up4h: s.Up4h, Sustained: s.Sustained})
	}
	sort.Slice(acc.Tags, func(i, j int) bool { return acc.Tags[i].Samples > acc.Tags[j].Samples })
	if len(acc.Tags) > 8 {
		acc.Tags = acc.Tags[:8]
	}
	r.mu.Lock()
	r.acc, r.accAt = acc, now
	r.mu.Unlock()
	return acc, nil
}

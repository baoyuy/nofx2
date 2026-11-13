package explosive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"nofx/internal/apischeduler"
	"nofx/logger"
	"nofx/trader/binance"
)

const (
	// boardTTL is how often the expensive pass (candles and open interest) is
	// refreshed. The board itself is rebuilt from those snapshots plus the
	// live feed on every read, so callers never wait for a scan.
	boardTTL = 30 * time.Second
	// liveMaxAge is how old a websocket tick may be before REST is used.
	liveMaxAge = 10 * time.Second
	// candidateLimit is how many of the day's top gainers get 5m candles;
	// refineLimit how many of those get 1h candles and open interest.
	candidateLimit = 40
	refineLimit    = 24
	fetchWorkers   = 24
	maxBoardRows   = 20
	scanTimeout    = 45 * time.Second
)

// symbolSnapshot is what the last expensive pass learned about one coin. The
// score is recomputed from it with the live price, so a page refresh costs no
// requests.
type symbolSnapshot struct {
	Bars5m     []Kline
	Bars1h     []Kline
	OIChange1h float64
	OIKnown    bool
	Funding    float64
	Price      float64
	Change24h  float64
	Volume24h  float64
}

// Screener builds the board from Binance public futures data. The websocket
// feed keeps prices live; candles, open interest and funding are refreshed on
// a slower cycle in the background.
type Screener struct {
	BaseURL string
	HTTP    *http.Client
	live    *LiveTickers

	mu         sync.Mutex
	snaps      map[string]symbolSnapshot
	watched    map[string]bool // symbols that passed the gates this pass
	scanned    int
	count      int // candidates that passed the gates
	fullAt     time.Time
	btcChange  float64
	refreshing bool

	funding   map[string]float64
	fundingAt time.Time

	startOnce sync.Once
	flight    singleflight.Group
}

// New returns a screener for Binance USDT-M futures.
func New() *Screener {
	return &Screener{
		BaseURL: "https://fapi.binance.com",
		HTTP:    &http.Client{Timeout: 15 * time.Second},
		live:    newLiveTickers(),
	}
}

// Shared is used by the API, so a page poll and a strategy read one board.
var Shared = New()

type ticker struct {
	Symbol      string
	Price       float64
	ChangePct   float64
	QuoteVolume float64
}

// Board returns the current rows. A cold screener waits for the first pass;
// afterwards a read rebuilds from the cached snapshots and the live feed, and
// a stale snapshot is refreshed in the background. refresh asks for that
// background pass explicitly without making the caller wait.
func (s *Screener) Board(ctx context.Context, refresh bool) (Board, error) {
	s.startOnce.Do(func() { s.live.Start(context.Background()) })

	s.mu.Lock()
	cold := s.fullAt.IsZero()
	stale := cold || time.Since(s.fullAt) > boardTTL
	refreshing := s.refreshing
	s.mu.Unlock()

	if cold {
		if err := s.refresh(ctx); err != nil {
			return Board{}, err
		}
	} else if !refreshing && (stale || refresh || s.hotWithoutSnapshot()) {
		go func() { _ = s.refresh(context.Background()) }()
	}
	return s.build(maxBoardRows), nil
}

// ScanAll runs a pass synchronously and returns every refined row, for probes
// and backtests.
func (s *Screener) ScanAll(ctx context.Context) (Board, error) {
	s.startOnce.Do(func() { s.live.Start(context.Background()) })
	if err := s.refresh(ctx); err != nil {
		return Board{}, err
	}
	return s.build(0), nil
}

// refresh runs one expensive pass. Concurrent callers share it.
func (s *Screener) refresh(ctx context.Context) error {
	_, err, _ := s.flight.Do("scan", func() (any, error) {
		s.mu.Lock()
		s.refreshing = true
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			s.refreshing = false
			s.mu.Unlock()
		}()

		scanCtx, cancel := context.WithTimeout(ctx, scanTimeout)
		defer cancel()
		snaps, watched, scanned, count, btc, err := s.scan(scanCtx)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.snaps, s.watched, s.scanned, s.count = snaps, watched, scanned, count
		s.btcChange, s.fullAt = btc, time.Now()
		s.mu.Unlock()
		return nil, nil
	})
	return err
}

// hotWithoutSnapshot tells whether a live top gainer has never been scanned.
// Waiting a full cycle would leave a fresh mover off the board.
func (s *Screener) hotWithoutSnapshot() bool {
	live := s.live.Snapshot()
	if len(live) == 0 {
		return false
	}
	type pair struct {
		symbol string
		change float64
	}
	top := make([]pair, 0, len(live))
	for symbol, t := range live {
		if !strings.HasSuffix(symbol, "USDT") || t.QuoteVolume < MinVolume24h || t.ChangePct <= 0 {
			continue
		}
		top = append(top, pair{symbol, t.ChangePct})
	}
	if len(top) == 0 {
		return false
	}
	sort.Slice(top, func(i, j int) bool { return top[i].change > top[j].change })
	if len(top) > maxBoardRows {
		top = top[:maxBoardRows]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A scan just ran: the missing symbol is one the gates or the ranking
	// dropped, and re-scanning immediately would never add it.
	if time.Since(s.fullAt) < 10*time.Second {
		return false
	}
	for _, p := range top {
		if !s.watched[p.symbol] {
			return true
		}
	}
	return false
}

// build recomputes the rows from the cached snapshots and live prices. It is
// cheap and never touches the network. limit <= 0 keeps every row.
func (s *Screener) build(limit int) Board {
	s.mu.Lock()
	snaps, scanned, candidates, fullAt, btc, refreshing := s.snaps, s.scanned, s.count, s.fullAt, s.btcChange, s.refreshing
	s.mu.Unlock()

	liveFresh := s.live.Fresh(liveMaxAge)
	statsFresh := s.live.StatsFresh(2 * statsPoll)
	if statsFresh {
		if v := s.live.BTCChange(); !math.IsNaN(v) {
			btc = v
		}
	}

	rows := make([]Row, 0, len(snaps))
	for symbol, snap := range snaps {
		price, change, volume := snap.Price, snap.Change24h, snap.Volume24h
		if liveFresh {
			if t, ok := s.live.Get(symbol); ok {
				price = t.Price
				if statsFresh {
					change, volume = t.ChangePct, t.QuoteVolume
				}
			}
		}
		bars5, bars1 := snap.Bars5m, snap.Bars1h
		if b := s.live.Bars(symbol, kline5m); len(b) >= minBars5m {
			bars5 = b
		}
		if b := s.live.Bars(symbol, kline1h); len(b) >= minBars1h {
			bars1 = b
		}
		if liveFresh {
			// The futures kline stream is not delivered on every network, so
			// the forming bar is built from the live price: a coin that is
			// dropping right now must score lower right now.
			bars5 = formingBar(bars5, price, 5*time.Minute, time.Now())
		}
		dayGain, elapsed, volSlope, dayHighDist, hasDay := dayFeatures(bars1, price, time.Now())
		if hasDay && dayGain <= 0 {
			continue // today's gainers only: this coin is down on the day
		}
		row, ok := Evaluate(Input{
			Symbol: symbol, Price: price, Change24h: change, Volume24h: volume,
			Bars5m: bars5, Bars1h: bars1,
			OIChange1h: snap.OIChange1h, OIKnown: snap.OIKnown,
			Funding: snap.Funding, BTCChange24h: btc,
			HasDay: hasDay, DayGain: dayGain, ElapsedH: elapsed, VolumeSlope: volSlope, DayHighDist: dayHighDist,
		})
		if !ok || row.DistanceHigh > MaxDistanceHigh {
			continue
		}
		rows = append(rows, row)
	}
	// Ordered by the expected rest of the day, not by what already happened:
	// a +20% coin with room left outranks a +300% one that is done.
	sort.SliceStable(rows, func(a, b int) bool {
		if rows[a].PredictedRemaining != rows[b].PredictedRemaining {
			return rows[a].PredictedRemaining > rows[b].PredictedRemaining
		}
		return rows[a].Strength > rows[b].Strength
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}

	board := Board{Rows: rows, Scanned: scanned, Candidates: candidates, FetchedAt: fullAt, Refreshing: refreshing}
	if !fullAt.IsZero() {
		board.AgeSeconds = int(time.Since(fullAt).Seconds())
		board.Stale = board.AgeSeconds > int(boardTTL.Seconds())
	}
	if len(rows) == 0 && !fullAt.IsZero() {
		board.Reason = "no coin passed the gates"
	}
	return board
}

func (s *Screener) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := apischeduler.Do(s.HTTP, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("binance futures HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

// tickers lists every contract with its 24h change and turnover, from the
// live feed when it is fresh and from REST otherwise.
func (s *Screener) tickers(ctx context.Context) ([]ticker, error) {
	if s.live.Fresh(liveMaxAge) && s.live.StatsFresh(2*statsPoll) {
		snapshot := s.live.Snapshot()
		out := make([]ticker, 0, len(snapshot))
		for symbol, t := range snapshot {
			out = append(out, ticker{Symbol: symbol, Price: t.Price, ChangePct: t.ChangePct, QuoteVolume: t.QuoteVolume})
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	var rows []struct {
		Symbol, LastPrice, PriceChangePercent, QuoteVolume string
	}
	if err := s.get(ctx, "/fapi/v1/ticker/24hr", &rows); err != nil {
		return nil, err
	}
	out := make([]ticker, 0, len(rows))
	for _, r := range rows {
		price, e1 := strconv.ParseFloat(r.LastPrice, 64)
		change, e2 := strconv.ParseFloat(r.PriceChangePercent, 64)
		volume, e3 := strconv.ParseFloat(r.QuoteVolume, 64)
		if e1 != nil || e2 != nil || e3 != nil || price <= 0 {
			continue
		}
		out = append(out, ticker{Symbol: r.Symbol, Price: price, ChangePct: change, QuoteVolume: volume})
	}
	return out, nil
}

// fundingRates is the current funding rate per symbol, cached for a minute.
func (s *Screener) fundingRates(ctx context.Context) map[string]float64 {
	s.mu.Lock()
	if s.funding != nil && time.Since(s.fundingAt) < time.Minute {
		f := s.funding
		s.mu.Unlock()
		return f
	}
	s.mu.Unlock()

	var rows []struct{ Symbol, LastFundingRate string }
	if err := s.get(ctx, "/fapi/v1/premiumIndex", &rows); err != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.funding // stale is better than empty; nil on the first failure
	}
	out := make(map[string]float64, len(rows))
	for _, r := range rows {
		if v, err := strconv.ParseFloat(r.LastFundingRate, 64); err == nil {
			out[r.Symbol] = v
		}
	}
	s.mu.Lock()
	s.funding, s.fundingAt = out, time.Now()
	s.mu.Unlock()
	return out
}

func (s *Screener) klines(ctx context.Context, symbol, interval string, limit int) ([]Kline, error) {
	q := url.Values{}
	q.Set("symbol", symbol)
	q.Set("interval", interval)
	q.Set("limit", strconv.Itoa(limit))
	var rows [][]json.RawMessage
	if err := s.get(ctx, "/fapi/v1/klines?"+q.Encode(), &rows); err != nil {
		return nil, err
	}
	out := parseKlines(rows)
	if len(out) > 1 {
		out = out[:len(out)-1] // the newest candle is still forming
	}
	return out, nil
}

// KlinesRange fetches bars whose open time falls in [startMs, endMs].
func (s *Screener) KlinesRange(ctx context.Context, symbol, interval string, startMs, endMs int64) ([]Kline, error) {
	q := url.Values{}
	q.Set("symbol", symbol)
	q.Set("interval", interval)
	q.Set("startTime", strconv.FormatInt(startMs, 10))
	q.Set("endTime", strconv.FormatInt(endMs, 10))
	q.Set("limit", "500")
	var rows [][]json.RawMessage
	if err := s.get(ctx, "/fapi/v1/klines?"+q.Encode(), &rows); err != nil {
		return nil, err
	}
	return parseKlines(rows), nil
}

func parseKlines(rows [][]json.RawMessage) []Kline {
	out := make([]Kline, 0, len(rows))
	for _, r := range rows {
		if len(r) < 6 {
			continue
		}
		var takerBuy float64
		if len(r) > 9 {
			takerBuy = num(r[9])
		}
		out = append(out, Kline{
			Time: int64(num(r[0])), Open: num(r[1]), High: num(r[2]), Low: num(r[3]),
			Close: num(r[4]), Volume: num(r[5]), TakerBuy: takerBuy,
		})
	}
	return out
}

// oiChange1h is the open-interest change over the last hour.
func (s *Screener) oiChange1h(ctx context.Context, symbol string) (float64, error) {
	q := url.Values{}
	q.Set("symbol", symbol)
	q.Set("period", "5m")
	q.Set("limit", "13")
	var rows []struct {
		SumOpenInterest string `json:"sumOpenInterest"`
	}
	if err := s.get(ctx, "/futures/data/openInterestHist?"+q.Encode(), &rows); err != nil {
		return 0, err
	}
	if len(rows) < 2 {
		return 0, errors.New("no open interest history")
	}
	first, err1 := strconv.ParseFloat(rows[0].SumOpenInterest, 64)
	last, err2 := strconv.ParseFloat(rows[len(rows)-1].SumOpenInterest, 64)
	if err1 != nil || err2 != nil || first <= 0 {
		return 0, errors.New("invalid open interest")
	}
	return last/first - 1, nil
}

func num(raw json.RawMessage) float64 {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		v, _ := strconv.ParseFloat(s, 64)
		return v
	}
	var f float64
	_ = json.Unmarshal(raw, &f)
	return f
}

// scan runs one expensive pass and returns per-symbol snapshots.
func (s *Screener) scan(ctx context.Context) (map[string]symbolSnapshot, map[string]bool, int, int, float64, error) {
	tickers, err := s.tickers(ctx)
	if err != nil {
		return nil, nil, 0, 0, 0, err
	}
	btcChange := 0.0
	for _, t := range tickers {
		if t.Symbol == "BTCUSDT" {
			btcChange = t.ChangePct
			break
		}
	}
	// Gates first: liquid, rising, crypto-only markets.
	candidates := make([]ticker, 0, len(tickers))
	for _, t := range tickers {
		// USDT-margined perpetuals only: USDC pairs and others are thinner
		// and would mix quote currencies in the same ranking.
		if !strings.HasSuffix(t.Symbol, "USDT") {
			continue
		}
		if t.QuoteVolume < MinVolume24h || t.ChangePct <= 0 {
			continue
		}
		if binance.IsTradFiSymbol(t.Symbol) {
			continue
		}
		candidates = append(candidates, t)
	}
	sort.SliceStable(candidates, func(a, b int) bool { return candidates[a].ChangePct > candidates[b].ChangePct })
	if len(candidates) > candidateLimit {
		candidates = candidates[:candidateLimit]
	}
	if len(candidates) == 0 {
		return map[string]symbolSnapshot{}, map[string]bool{}, len(tickers), 0, btcChange, nil
	}

	// Stage one: 5m candles, to rank by speed and volume before spending the
	// heavier 1h and open-interest requests.
	type stageOne struct {
		t    ticker
		bars []Kline
		pre  float64
	}
	one := make([]stageOne, len(candidates))
	var wg sync.WaitGroup
	slots := make(chan struct{}, fetchWorkers)
	for i, t := range candidates {
		wg.Add(1)
		go func(i int, t ticker) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			bars, err := s.klines(ctx, t.Symbol, "5m", 60)
			if err != nil || len(bars) < minBars5m {
				return
			}
			s.live.Seed(t.Symbol, kline5m, bars)
			one[i] = stageOne{t: t, bars: bars, pre: preScore(bars, t.ChangePct, btcChange)}
		}(i, t)
	}
	wg.Wait()
	kept := one[:0]
	for _, o := range one {
		if len(o.bars) > 0 {
			kept = append(kept, o)
		}
	}
	sort.SliceStable(kept, func(a, b int) bool { return kept[a].pre > kept[b].pre })
	if len(kept) > refineLimit {
		kept = kept[:refineLimit]
	}

	// Stage two: the trend frame and open interest.
	funding := s.fundingRates(ctx)
	type refined struct {
		symbol string
		snap   symbolSnapshot
		ok     bool
	}
	var mu sync.Mutex
	oiFails := 0
	out := make([]refined, len(kept))
	for i, o := range kept {
		wg.Add(1)
		go func(i int, o stageOne) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			hourly, err := s.klines(ctx, o.t.Symbol, "1h", 150)
			if err != nil || len(hourly) < minBars1h {
				return
			}
			s.live.Seed(o.t.Symbol, kline1h, hourly)
			oi, oiErr := s.oiChange1h(ctx, o.t.Symbol)
			if oiErr != nil {
				mu.Lock()
				if oiFails < 2 {
					logger.Warnf("explosive: open interest %s: %v", o.t.Symbol, oiErr)
				}
				oiFails++
				mu.Unlock()
			}
			out[i] = refined{symbol: o.t.Symbol, ok: true, snap: symbolSnapshot{
				Bars5m: o.bars, Bars1h: hourly, OIChange1h: oi, OIKnown: oiErr == nil,
				Funding: funding[o.t.Symbol], Price: o.t.Price, Change24h: o.t.ChangePct, Volume24h: o.t.QuoteVolume,
			}}
		}(i, o)
	}
	wg.Wait()
	if oiFails > 0 {
		logger.Warnf("explosive: open interest failed for %d of %d symbols", oiFails, len(kept))
	}

	snaps := make(map[string]symbolSnapshot, len(out))
	candidateSymbols := make([]string, 0, len(kept))
	watched := make(map[string]bool, len(kept))
	for _, o := range kept {
		candidateSymbols = append(candidateSymbols, o.t.Symbol)
		watched[o.t.Symbol] = true
	}
	refinedSymbols := make([]string, 0, len(out))
	for _, r := range out {
		if r.ok {
			snaps[r.symbol] = r.snap
			refinedSymbols = append(refinedSymbols, r.symbol)
		}
	}
	// Keep the kline streams in step with what the board watches.
	s.live.SetSymbols(candidateSymbols, refinedSymbols)
	return snaps, watched, len(tickers), len(candidates), btcChange, nil
}

// preScore ranks stage-one candidates before the heavier requests. It keeps
// relative strength at 40% so a leader that is consolidating after a big run
// is not dropped in favour of a coin that only just ticked up.
func preScore(bars []Kline, change24h, btcChange float64) float64 {
	cl := closes(bars)
	if len(cl) < 2 {
		return 0
	}
	ret := (cl[len(cl)-1]/cl[len(cl)-2] - 1) * 100
	speed := clamp01((ret / max(atrPct(bars, 14), 0.05)) / speedFull)
	vol := clamp01((volumeRatio(bars) - 1) / (volumeFull - 1))
	rel := clamp01((change24h - btcChange) / 150)
	return 0.35*speed + 0.25*vol + 0.40*rel
}

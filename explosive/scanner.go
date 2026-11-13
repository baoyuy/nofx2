package explosive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"nofx/internal/apischeduler"
	"nofx/trader/binance"
)

const (
	// boardTTL is how long a scan is reused. The page polls faster than this;
	// callers with refresh=1 force a new pass.
	boardTTL = 60 * time.Second
	// candidateLimit is how many of the day's top gainers get 5m candles;
	// refineLimit how many of those get 1h candles and open interest. Both
	// bound the request budget of one scan.
	candidateLimit = 40
	refineLimit    = 24
	fetchWorkers   = 16
	maxBoardRows   = 20
	scanTimeout    = 45 * time.Second
)

// Screener builds the board from Binance public futures data. All calls go
// through the shared API scheduler, so they share its budget with the rest of
// the system.
type Screener struct {
	BaseURL string
	HTTP    *http.Client

	mu        sync.Mutex
	board     Board
	boardAt   time.Time
	funding   map[string]float64
	fundingAt time.Time
	flight    singleflight.Group
}

// New returns a screener for Binance USDT-M futures.
func New() *Screener {
	return &Screener{
		BaseURL: "https://fapi.binance.com",
		HTTP:    &http.Client{Timeout: 15 * time.Second},
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

// Board returns the ranked rows, reusing a recent scan unless refresh is set.
func (s *Screener) Board(ctx context.Context, refresh bool) (Board, error) {
	s.mu.Lock()
	cached, at := s.board, s.boardAt
	s.mu.Unlock()
	if !refresh && !at.IsZero() && time.Since(at) < boardTTL {
		return cached, nil
	}
	v, err, _ := s.flight.Do("board", func() (any, error) {
		scanCtx, cancel := context.WithTimeout(context.Background(), scanTimeout)
		defer cancel()
		return s.scan(scanCtx)
	})
	if err != nil {
		return Board{}, err
	}
	board := v.(Board)
	s.mu.Lock()
	s.board, s.boardAt = board, time.Now()
	s.mu.Unlock()
	return board, nil
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

// tickers lists every USDT perpetual with its 24h change and turnover.
func (s *Screener) tickers(ctx context.Context) ([]ticker, error) {
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
	out := make([]Kline, 0, len(rows))
	for _, r := range rows {
		if len(r) < 6 {
			continue
		}
		out = append(out, Kline{
			Time: int64(num(r[0])), Open: num(r[1]), High: num(r[2]), Low: num(r[3]),
			Close: num(r[4]), Volume: num(r[5]),
		})
	}
	if len(out) > 1 {
		out = out[:len(out)-1] // the newest candle is still forming
	}
	return out, nil
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

// scan runs one full pass.
func (s *Screener) scan(ctx context.Context) (Board, error) {
	tickers, err := s.tickers(ctx)
	if err != nil {
		return Board{}, err
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
	board := Board{Scanned: len(tickers), Candidates: len(candidates), FetchedAt: time.Now()}
	sort.SliceStable(candidates, func(a, b int) bool { return candidates[a].ChangePct > candidates[b].ChangePct })
	if len(candidates) > candidateLimit {
		candidates = candidates[:candidateLimit]
	}
	if len(candidates) == 0 {
		board.Reason = "no liquid gainer right now"
		return board, nil
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
			one[i] = stageOne{t: t, bars: bars, pre: preScore(bars)}
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
	if len(kept) == 0 {
		board.Reason = "candle data unavailable for the gainers"
		return board, nil
	}

	// Stage two: the trend frame and open interest.
	funding := s.fundingRates(ctx)
	type refined struct {
		row Row
		ok  bool
	}
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
			oi, oiErr := s.oiChange1h(ctx, o.t.Symbol)
			row, ok := Evaluate(Input{
				Symbol: o.t.Symbol, Price: o.t.Price, Change24h: o.t.ChangePct, Volume24h: o.t.QuoteVolume,
				Bars5m: o.bars, Bars1h: hourly, OIChange1h: oi, OIKnown: oiErr == nil, Funding: funding[o.t.Symbol],
			})
			out[i] = refined{row: row, ok: ok}
		}(i, o)
	}
	wg.Wait()

	rows := make([]Row, 0, len(out))
	for _, r := range out {
		if !r.ok || r.row.DistanceHigh > MaxDistanceHigh {
			continue
		}
		rows = append(rows, r.row)
	}
	sort.SliceStable(rows, func(a, b int) bool { return rows[a].Score > rows[b].Score })
	if len(rows) > maxBoardRows {
		rows = rows[:maxBoardRows]
	}
	board.Rows = rows
	if len(rows) == 0 {
		board.Reason = "no coin passed the gates"
	}
	return board, nil
}

// preScore ranks stage-one candidates by the two parts that need only 5m
// candles: speed and volume.
func preScore(bars []Kline) float64 {
	cl := closes(bars)
	if len(cl) < 2 {
		return 0
	}
	ret := (cl[len(cl)-1]/cl[len(cl)-2] - 1) * 100
	speed := clamp01((ret / max(atrPct(bars, 14), 0.05)) / speedFull)
	vol := clamp01((volumeRatio(bars) - 1) / (volumeFull - 1))
	return wSpeed*speed + wVolume*vol
}

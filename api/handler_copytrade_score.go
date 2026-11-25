package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"nofx/copytrade"
	"nofx/logger"
	"nofx/mcp"
	"nofx/store"
	"nofx/trader/binance"
)

// Per-coin AI reading for the copy-trade "by coin" page. Read-only guidance:
// direction + 0-100 score + reasons/risks, no entry/stop, never feeds an order.

const copytradeScoreModelKey = "copytrade_score_model"

// copytradeScoreOwnerKey is the account that picked the scoring model. The
// scheduler scores (and builds the AI coin pool) for this account, so the pool
// shows up in that account's strategy editor.
const copytradeScoreOwnerKey = "copytrade_score_owner"

// coinScoreAuto is the in-memory status of the analyze-all run.
type coinScoreAuto struct {
	mu            sync.Mutex
	running       bool
	lastRun       int64
	lastCount     int
	lastErr       string
	done          int
	total         int
	inflight      map[string]bool
	manualRunning bool
	lastDiagMs    int64
	lastFail      map[string]int64
	failCount     map[string]int
	retryAfter    map[string]int64
}

type coinScoreAnalysis struct {
	Bias          string         `json:"bias"` // long | short | neutral
	BullScore     float64        `json:"bull_score"`
	BearScore     float64        `json:"bear_score"`
	Edge          float64        `json:"edge"`          // bull - bear
	SmoothedEdge  float64        `json:"smoothed_edge"` // EMA-smoothed edge
	Conviction    float64        `json:"conviction"`    // |edge|
	Horizon       string         `json:"horizon"`       // short | swing
	TFAlignment   map[string]int `json:"tf_alignment,omitempty"`
	LongCase      []string       `json:"long_case"`
	ShortCase     []string       `json:"short_case"`
	Invalidates   string         `json:"invalidates"`
	Changed       bool           `json:"changed,omitempty"`
	ChangeReason  string         `json:"change_reason,omitempty"`
	MarketContext string         `json:"market_context,omitempty"`
	Sources       []string       `json:"sources,omitempty"`
	// Legacy display fields, derived from the v2 fields above.
	Score      float64  `json:"score"`
	Confidence float64  `json:"confidence"`
	Reasons    []string `json:"reasons"`
	Risks      []string `json:"risks"`
}

type coinScoreResult struct {
	Symbol     string             `json:"symbol"`
	Interval   string             `json:"interval"`
	ModelID    string             `json:"model_id"`
	ModelName  string             `json:"model_name,omitempty"`
	AnalyzedAt int64              `json:"analyzed_at"`
	Price      float64            `json:"price"`
	Analysis   *coinScoreAnalysis `json:"analysis,omitempty"`
	Raw        string             `json:"raw,omitempty"`
	ParseError string             `json:"parse_error,omitempty"`
}

func (s *Server) registerCopyTradeScoreRoutes(protected *gin.RouterGroup) {
	s.route(protected, "GET", "/copytrade/direction-shortlist", "Research-only top ten per direction; no trading side effects", s.handleDirectionShortlist)
	s.route(protected, "GET", "/copytrade/coin-score", "Saved AI reading for one coin (?symbol=)", s.handleCoinScoreGet)
	s.route(protected, "POST", "/copytrade/coin-score", "Run the AI reading for one coin now (body: symbol, interval?, model_id?, lang?)", s.handleCoinScoreAnalyze)
	s.route(protected, "GET", "/copytrade/coin-score/models", "Enabled models and the saved coin-score model", s.handleCoinScoreModels)
	s.route(protected, "PUT", "/copytrade/coin-score/model", "Set the coin-score model (body: model_id)", s.handleCoinScoreModelPut)
	s.route(protected, "GET", "/copytrade/coin-scores", "Saved AI readings for every coin (list column)", s.handleCoinScoresList)
	s.route(protected, "GET", "/copytrade/coin-score/outcomes", "Forward-return hit rate of past scores (by side, band, horizon)", s.handleCoinScoreOutcomes)
	s.route(protected, "GET", "/copytrade/coin-pool", "Current AI coin pool for this user", s.handleCoinPoolGet)
	s.route(protected, "POST", "/copytrade/coin-pool/generate", "Rebuild the AI coin pool (<=10, long/short) from current scores", s.handleCoinPoolGenerate)
	s.route(protected, "GET", "/copytrade/coin-status", "Unified AI scoring scope/status counts per coin (single volume snapshot)", s.handleCoinStatus)
	s.route(protected, "GET", "/copytrade/coin-accuracy", "Forward-4h accuracy over valid AI samples only (overall + long/short)", s.handleCoinAccuracy)
	s.route(protected, "GET", "/copytrade/coin-ranking", "Ranked coins (?mode=stable|ai): stable=all coins calibrated 4h; ai=scored+fresh only", s.handleCoinRanking)
	s.route(protected, "GET", "/copytrade/coin-score/settings", "Scheduled analyze-all settings and latest run status", s.handleCoinScoreSettingsGet)
	s.route(protected, "PUT", "/copytrade/coin-score/settings", "Save scheduled analyze-all settings (body: enabled, interval_minutes)", s.handleCoinScoreSettingsPut)
	s.route(protected, "POST", "/copytrade/coin-score/run-all", "Analyze every coin on the by-coin page now (async; poll settings for progress)", s.handleCoinScoreRunAll)
}

// scoreModel resolves which model to use: the request's, else the saved
// setting, else the user's default enabled model.
func (s *Server) scoreModel(userID, requested string) (*store.AIModel, error) {
	if requested != "" {
		return s.store.AIModel().Get(userID, requested)
	}
	if saved, err := s.store.GetSystemConfig(copytradeScoreModelKey); err == nil && saved != "" {
		if m, err := s.store.AIModel().Get(userID, saved); err == nil && m.Enabled {
			return m, nil
		}
	}
	if m, err := s.store.AIModel().GetDefault(userID); err == nil && m.Enabled {
		return m, nil
	}
	// Fall back to the first enabled model the user has.
	all, err := s.store.AIModel().List(userID)
	if err != nil {
		return nil, err
	}
	for _, m := range all {
		if m.Enabled {
			return m, nil
		}
	}
	return nil, fmt.Errorf("no enabled AI model configured")
}

func (s *Server) handleCoinScoreModels(c *gin.Context) {
	userID := c.GetString("user_id")
	all, err := s.store.AIModel().List(userID)
	if err != nil {
		SafeInternalError(c, "List AI models", err)
		return
	}
	type modelLite struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Provider string `json:"provider"`
	}
	var models []modelLite
	defaultID := ""
	for _, m := range all {
		if !m.Enabled {
			continue
		}
		models = append(models, modelLite{ID: m.ID, Name: m.Name, Provider: m.Provider})
		if defaultID == "" {
			defaultID = m.ID
		}
	}
	selected := defaultID
	if saved, err := s.store.GetSystemConfig(copytradeScoreModelKey); err == nil && saved != "" {
		selected = saved
	}
	c.JSON(http.StatusOK, gin.H{"models": models, "selected": selected, "default": defaultID})
}

func (s *Server) handleCoinScoreModelPut(c *gin.Context) {
	userID := c.GetString("user_id")
	var req struct {
		ModelID string `json:"model_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ModelID == "" {
		SafeBadRequest(c, "model_id is required")
		return
	}
	m, err := s.store.AIModel().Get(userID, req.ModelID)
	if err != nil || !m.Enabled {
		SafeBadRequest(c, "model not found or not enabled")
		return
	}
	if err := s.store.SetSystemConfig(copytradeScoreModelKey, req.ModelID); err != nil {
		SafeInternalError(c, "Save coin-score model", err)
		return
	}
	if err := s.store.SetSystemConfig(copytradeScoreOwnerKey, userID); err != nil {
		SafeInternalError(c, "Save coin-score owner", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"selected": req.ModelID})
}

func (s *Server) handleCoinScoreGet(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	userID := c.GetString("user_id")
	symbol, ok := marketSymbol(c, c.Query("symbol"))
	if !ok {
		return
	}
	row, err := s.store.CopyTrade().GetCoinScore(userID, symbol)
	if err != nil {
		SafeInternalError(c, "Read coin score", err)
		return
	}
	if row == nil {
		c.JSON(http.StatusOK, gin.H{"symbol": symbol, "analysis": nil})
		return
	}
	var out coinScoreResult
	if err := json.Unmarshal([]byte(row.Payload), &out); err != nil {
		c.JSON(http.StatusOK, gin.H{"symbol": symbol, "analysis": nil})
		return
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleCoinScoreAnalyze(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	var req struct {
		Symbol   string `json:"symbol"`
		Interval string `json:"interval"`
		ModelID  string `json:"model_id"`
		Lang     string `json:"lang"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "symbol is required")
		return
	}
	symbol, ok := marketSymbol(c, req.Symbol)
	if !ok {
		return
	}
	interval := req.Interval
	if interval == "" {
		interval = "1h"
	}
	if !binance.ValidMarketIntervals[interval] {
		SafeBadRequest(c, "unsupported interval")
		return
	}
	userID := c.GetString("user_id")
	model, err := s.scoreModel(userID, req.ModelID)
	if err != nil {
		SafeBadRequest(c, "No AI model for scoring: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	res, err := s.analyzeCoinCore(ctx, userID, model, symbol, interval, req.Lang)
	// An unreadable reply is still shown to the user with its raw text, so a
	// manual run can be inspected; it is simply not saved as a reading.
	if err != nil && !errors.Is(err, errUnparseableReading) {
		SafeError(c, http.StatusBadGateway, "AI call failed: "+SanitizeError(err, "check the AI connection settings"), err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// analyzeCoinCore builds the copy-trade-aware prompt, calls the model, parses
// the strict JSON and saves the reading. A parse failure is kept in the result
// (with the raw text); only a transport/AI error is returned as err.
func (s *Server) analyzeCoinCore(ctx context.Context, userID string, model *store.AIModel, symbol, interval, lang string) (coinScoreResult, error) {
	var tfs []scoreTF
	for _, spec := range []struct {
		iv string
		n  int
	}{{"15m", 80}, {"1h", 120}, {"4h", 60}} {
		k, err := binance.GetPublicKlines(ctx, symbol, spec.iv, spec.n)
		if err != nil || len(k) < 25 {
			continue
		}
		tfs = append(tfs, scoreTF{Interval: spec.iv, Klines: k, Ind: computeIndicators(k)})
	}
	if len(tfs) == 0 {
		return coinScoreResult{}, fmt.Errorf("not enough candles for %s", symbol)
	}
	ticker, _ := binance.GetPublicTicker(ctx, symbol)
	deriv := binance.GetDerivatives(ctx, symbol)
	regime := binance.GetBTCRegime(ctx)
	btcTicker, _ := binance.GetPublicTicker(ctx, "BTCUSDT")
	var coin24, btc24 float64
	if ticker != nil {
		coin24 = ticker.PriceChangePct
	}
	if btcTicker != nil {
		btc24 = btcTicker.PriceChangePct
	}
	snaps := s.copyTrade.Snapshots()
	var sum *copytrade.SymbolSummary
	for _, x := range copytrade.Symbols(snaps) {
		if x.Symbol == symbol {
			xx := x
			sum = &xx
			break
		}
	}
	holders := copytrade.Holders(snaps, symbol)
	prev := s.loadScoreMemory(userID, symbol)
	rel := relativeStrength(ctx, tfs, coin24, btc24)
	system, user := buildCoinScorePrompt(lang, symbol, tfs, ticker, deriv, regime, sum, holders, prev, rel)

	primary := "1h"
	if interval != "" {
		primary = interval
	}
	price := tfs[0].Klines[len(tfs[0].Klines)-1].Close
	for _, tf := range tfs {
		if tf.Interval == "1h" {
			price = tf.Klines[len(tf.Klines)-1].Close
		}
	}
	if err := ctx.Err(); err != nil {
		return coinScoreResult{}, err
	}
	// Background scoring must release the shared model channel when its task
	// expires, rather than continuing requests/retries for several minutes.
	timeout := 45 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	raw, err := s.runRealAITestContext(ctx, userID, model.ID, system, user, mcp.WithMaxRetries(1), mcp.WithTimeout(timeout), mcp.WithReasoningEffort("low"))
	if err != nil {
		return coinScoreResult{}, err
	}
	res := coinScoreResult{Symbol: symbol, Interval: "15m+1h+4h", ModelID: model.ID, ModelName: model.Name,
		AnalyzedAt: time.Now().UnixMilli(), Price: price, Raw: raw}
	if a, perr := parseCoinScore(raw); perr != nil {
		// An unparseable reply is a FAILED reading, not a new one. It used to
		// be saved over the coin's last valid reading (so the pool, rankings
		// and program lost the coin) and returned as success (so the
		// scheduler cleared its backoff), while score memory kept the old
		// time — so the coin was due again and re-called every tick, about
		// every 15 seconds. Now nothing is saved and the error is returned,
		// so the last good reading stands and the failure backoff applies.
		res.ParseError = "The AI reply was not valid JSON; showing the raw text."
		return res, errUnparseableReading
	} else {
		// Light smoothing only. The previous 0.3-new/0.7-old weighting, on
		// top of a prompt that told the model to keep its last call, made a
		// 4h signal follow readings from hours earlier. Now the newest reading
		// dominates, and a previous reading older than one 4h horizon is
		// ignored entirely rather than dragging the new one.
		a.SmoothedEdge = smoothEdge(a.Edge, prev, res.AnalyzedAt)
		res.Analysis = a
	}
	_ = primary
	if data, merr := json.Marshal(res); merr == nil {
		scfg := s.loadCoinScoreAuto()
		row := store.CopyTradeCoinScore{UserID: userID, Symbol: symbol, ModelID: model.ID, Interval: "15m+1h+4h",
			Payload: string(data), AnalyzedAt: res.AnalyzedAt, ConfigVersion: coinScoreConfigVersion, MinVolume: scfg.minVolume()}
		if serr := s.store.CopyTrade().SaveCoinScore(&row); serr != nil {
			logger.Warnf("copy-trade coin score: save %s: %v", symbol, serr)
			res.ParseError = strings.TrimSpace(res.ParseError + " (result not saved)")
		}
		s.saveScoreMemory(userID, symbol, res)
	}
	if res.Analysis != nil {
		// Count this coin+direction at most once per 4h for the forward check.
		if exists, _ := s.store.CopyTrade().HasRecentOutcome(userID, symbol, res.Analysis.Bias, res.AnalyzedAt-outcomeDedupMs); !exists {
			o := &store.CopyTradeScoreOutcome{UserID: userID, Symbol: symbol, AnalyzedAt: res.AnalyzedAt,
				Bias: res.Analysis.Bias, Bull: res.Analysis.BullScore, Bear: res.Analysis.BearScore, EntryPrice: res.Price,
				ConfigVersion: coinScoreConfigVersion,
				Features:      scoreFeaturesJSON(ticker, deriv, sum, rel, tfs)}
			if err := s.store.CopyTrade().SaveScoreOutcome(o); err != nil {
				logger.Warnf("copy-trade outcomes: enqueue %s: %v", symbol, err)
			}
		}
	}
	return res, nil
}

// ---- scheduled / manual analyze-all ----

const (
	coinScoreAutoKey          = "copytrade_coin_score_auto"
	coinScoreMinInterval      = 5
	coinScoreDefaultMins      = 60
	coinScoreConfigVersion    = "copytrade-ai-v13"
	coinScoreDefaultMinVolume = 100_000_000 // 1e8 USDT (Binance USDT-M 24h quote volume)
	// coinScoreNeutralMovePct: a 4h move smaller than this (%) is a neutral /
	// invalid sample — it must NOT count as a hit or a miss.
	coinScoreNeutralMovePct = 0.5
)

type coinScoreAutoConfig struct {
	Enabled            bool    `json:"enabled"`
	IntervalMinutes    int     `json:"interval_minutes"`
	PoolAuto           bool    `json:"pool_auto"`
	TopN               int     `json:"top_n"`
	Concurrency        int     `json:"concurrency"`
	MinQuoteVolume     float64 `json:"min_quote_volume"`
	PoolRefreshMinutes int     `json:"pool_refresh_minutes"`
	PoolTTLMinutes     int     `json:"pool_ttl_minutes"`
	// PoolRankMode: "measured" (default) or "conviction" (previous behaviour).
	PoolRankMode string `json:"pool_rank_mode,omitempty"`
}

// scoreFreshMs is how long a coin's AI reading stays valid, for EVERY consumer:
// the pool, the program-stable trader, the rankings, the status page, the
// shortlist and the stable-direction badge.
//
// It must cover one full scoring interval plus a margin, because a coin is
// re-scored once per interval. Before, the consumers disagreed: the program,
// the coin list, status and shortlist used PoolTTLMinutes (default 12), the
// stable badge used 30, and the pool used interval+refresh (~65). With hourly
// scoring, a coin was "fresh" for the program only ~12 minutes of every 60, so
// it closed positions as "score expired" and reopened them next hour: 52
// program trades held 13 minutes on average and lost to fees.
//
// PoolTTLMinutes remains what it says: how long a POOL PICK lasts, so a pool
// built from readings that then stop arriving cannot be traded forever.
func (c coinScoreAutoConfig) scoreFreshMs() int64 {
	interval := c.IntervalMinutes
	if interval < coinScoreMinInterval {
		interval = coinScoreDefaultMins
	}
	refresh := c.PoolRefreshMinutes
	if refresh < 1 {
		refresh = 5
	}
	// One interval, plus the pool refresh cadence, plus a quarter interval for
	// a run that finishes late. Never shorter than the pick TTL.
	mins := interval + refresh + interval/4
	if ttl := c.PoolTTLMinutes; ttl > mins {
		mins = ttl
	}
	return int64(mins) * 60 * 1000
}

// minVolume is the 24h quote-volume floor for the scoring universe AND every
// page that reports on it.
//
// 0 used to mean different things in different places: the scoring universe
// read it as "no filter" while the status, ranking, accuracy, shortlist and
// program-stable code substituted coinScoreDefaultMinVolume. Saving 0 then
// spent tokens scoring small coins that those pages still reported as
// "out of AI scope" and that never entered a ranking. One rule now: 0 means
// the default floor, and the settings page says so.
func (c coinScoreAutoConfig) minVolume() float64 {
	if c.MinQuoteVolume > 0 {
		return c.MinQuoteVolume
	}
	return coinScoreDefaultMinVolume
}

// rankMode returns the effective pool ranking mode; anything unrecognised,
// including an unset value from settings saved before this option existed,
// means the measured-history ranking.
func (c coinScoreAutoConfig) rankMode() string {
	if c.PoolRankMode == poolRankConviction {
		return poolRankConviction
	}
	return poolRankMeasured
}

func (s *Server) loadCoinScoreAuto() coinScoreAutoConfig {
	cfg := coinScoreAutoConfig{IntervalMinutes: coinScoreDefaultMins}
	if raw, err := s.store.GetSystemConfig(coinScoreAutoKey); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	if cfg.IntervalMinutes < coinScoreMinInterval {
		cfg.IntervalMinutes = coinScoreDefaultMins
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 4
	}
	if cfg.PoolRefreshMinutes < 1 {
		cfg.PoolRefreshMinutes = 5
	}
	if cfg.PoolTTLMinutes < 1 {
		cfg.PoolTTLMinutes = 12
	}
	if cfg.PoolTTLMinutes < cfg.PoolRefreshMinutes {
		cfg.PoolTTLMinutes = cfg.PoolRefreshMinutes * 2
	}
	return cfg
}

// resolveScoreModelAny picks the model and owning user for a batch run. A
// request-scoped run passes the user. The scheduler (no request) uses, in
// order: the account that picked the scoring model, the owner of the saved
// scoring model, then any enabled model and its owner. Scores and the AI coin
// pool are stored under the returned user.
func (s *Server) resolveScoreModelAny(userID string) (*store.AIModel, string, error) {
	if userID != "" {
		m, err := s.scoreModel(userID, "")
		if err != nil {
			return nil, "", err
		}
		return m, userID, nil
	}
	if owner, err := s.store.GetSystemConfig(copytradeScoreOwnerKey); err == nil && owner != "" {
		if m, err := s.scoreModel(owner, ""); err == nil {
			return m, owner, nil
		}
	}
	if saved, err := s.store.GetSystemConfig(copytradeScoreModelKey); err == nil && saved != "" {
		if m, err := s.store.AIModel().GetByID(saved); err == nil && m.Enabled && m.UserID != "" && m.UserID != "default" {
			return m, m.UserID, nil
		}
	}
	m, err := s.store.AIModel().GetAnyEnabled()
	if err != nil {
		return nil, "", err
	}
	return m, m.UserID, nil
}

func (s *Server) coinScoreBegin() bool {
	s.coinScore.mu.Lock()
	defer s.coinScore.mu.Unlock()
	if s.coinScore.running {
		return false
	}
	s.coinScore.running = true
	s.coinScore.manualRunning = true
	s.coinScore.done, s.coinScore.total = 0, 0
	s.coinScore.lastErr = ""
	return true
}
func (s *Server) coinScoreSetTotal(n int) {
	s.coinScore.mu.Lock()
	s.coinScore.total = n
	s.coinScore.mu.Unlock()
}
func (s *Server) coinScoreInc() { s.coinScore.mu.Lock(); s.coinScore.done++; s.coinScore.mu.Unlock() }
func (s *Server) coinScoreMarkFail(sym string) {
	s.coinScore.mu.Lock()
	if s.coinScore.lastFail == nil {
		s.coinScore.lastFail = map[string]int64{}
	}
	if s.coinScore.failCount == nil {
		s.coinScore.failCount = map[string]int{}
	}
	if s.coinScore.retryAfter == nil {
		s.coinScore.retryAfter = map[string]int64{}
	}
	n := s.coinScore.failCount[sym] + 1
	s.coinScore.failCount[sym] = n
	// exponential backoff: 1,2,4,8,16,32,60,60... minutes.
	backoff := time.Minute << uint(min(n-1, 6))
	if backoff > 60*time.Minute {
		backoff = 60 * time.Minute
	}
	now := time.Now()
	s.coinScore.lastFail[sym] = now.UnixMilli()
	s.coinScore.retryAfter[sym] = now.Add(backoff).UnixMilli()
	s.coinScore.mu.Unlock()
}

func (s *Server) coinScoreClearFail(sym string) {
	s.coinScore.mu.Lock()
	if s.coinScore.lastFail != nil {
		delete(s.coinScore.lastFail, sym)
	}
	if s.coinScore.failCount != nil {
		delete(s.coinScore.failCount, sym)
	}
	if s.coinScore.retryAfter != nil {
		delete(s.coinScore.retryAfter, sym)
	}
	s.coinScore.mu.Unlock()
}

func (s *Server) coinScoreSetErr(msg string) {
	if msg == "" {
		return
	}
	s.coinScore.mu.Lock()
	s.coinScore.lastErr = msg
	s.coinScore.mu.Unlock()
}
func (s *Server) coinScoreFinish(ok int, lastErr string) {
	s.coinScore.mu.Lock()
	s.coinScore.running = false
	s.coinScore.manualRunning = false
	s.coinScore.lastRun = time.Now().UnixMilli()
	s.coinScore.lastCount = ok
	if lastErr != "" {
		s.coinScore.lastErr = lastErr
	}
	s.coinScore.mu.Unlock()
}

// runCoinScoreAll analyzes every coin currently on the by-coin page. Runs at
// most one at a time; the scheduler and the manual button share this.
// coinUniverse returns the tradable coin symbols with real 24h quote volumes
// attached, filtered by a minimum volume (unknown/0 volume is not filtered) and
// capped by topN. The copy-trade snapshots themselves do not carry volume, so we
// must attach it here or the volume filter would see 0 for every coin.
func (s *Server) coinUniverse(ctx context.Context, minVol float64, topN int) []string {
	sums := copytrade.Symbols(s.copyTrade.Snapshots())
	if vols, err := binance.PublicQuoteVolumes(ctx); err == nil && len(vols) > 0 {
		copytrade.AttachRanks(sums, vols)
	}
	out := make([]string, 0, len(sums))
	for _, x := range sums {
		// Unknown volume (0) is OUT of scope, as on every page that reports on
		// this universe. It used to be kept here, and when the volume fetch
		// failed with no cache the filter switched off entirely, so tokens were
		// spent scoring coins the rankings then excluded.
		if minVol > 0 && (x.Volume24h <= 0 || x.Volume24h < minVol) {
			continue
		}
		out = append(out, x.Symbol)
	}
	if topN > 0 && topN < len(out) {
		out = out[:topN]
	}
	return out
}

// runCoinScoreAll scores the whole universe. The caller must already have
// claimed the run with coinScoreBegin, so a refusal can be reported to whoever
// asked instead of being swallowed here.
func (s *Server) runCoinScoreAll(ctx context.Context, userID string, topN, skipFreshMins int) {
	model, uid, err := s.resolveScoreModelAny(userID)
	if err != nil {
		s.coinScoreFinish(0, "no AI model: "+err.Error())
		return
	}
	minVol := s.loadCoinScoreAuto().minVolume()
	symbols := s.coinUniverse(ctx, minVol, topN)
	if skipFreshMins > 0 {
		kept := make([]string, 0, len(symbols))
		for _, sym := range symbols {
			if m := s.loadScoreMemory(uid, sym); m != nil {
				if time.Since(time.UnixMilli(m.LastAt)) < time.Duration(skipFreshMins)*time.Minute {
					continue
				}
			}
			kept = append(kept, sym)
		}
		symbols = kept
	}
	s.coinScoreSetTotal(len(symbols))
	conc := s.loadCoinScoreAuto().Concurrency
	if conc < 1 {
		conc = 4
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	lastErr := ""
	for _, sym := range symbols {
		select {
		case <-ctx.Done():
		case sem <- struct{}{}:
			wg.Add(1)
			go func(sym string) {
				defer wg.Done()
				defer func() { <-sem }()
				cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
				defer cancel()
				if _, e := s.analyzeCoinCore(cctx, uid, model, sym, "1h", ""); e != nil {
					mu.Lock()
					lastErr = sym + ": " + e.Error()
					mu.Unlock()
					s.coinScoreMarkFail(sym)
				} else {
					s.coinScoreClearFail(sym)
					mu.Lock()
					ok++
					mu.Unlock()
				}
				s.coinScoreInc()
			}(sym)
		}
	}
	wg.Wait()
	s.coinScoreFinish(ok, lastErr)
	if cfg := s.loadCoinScoreAuto(); cfg.PoolAuto && ok > 0 {
		if _, err := s.buildCoinPool(uid); err != nil {
			logger.Warnf("copy-trade auto pool: %v", err)
		}
	}
}

// StartCoinScoreAuto starts the scheduled analyze-all loop. Runs on app start.
func (s *Server) StartCoinScoreAuto(ctx context.Context) {
	logger.Info("copy-trade: coin-score rolling scheduler started")
	go s.coinScoreAutoLoop(ctx)
}

// coinScoreAutoLoop runs a rolling scheduler: it wakes often and refreshes the
// coins that are due (last analyzed >= interval ago), oldest first, up to the
// configured concurrency. Each coin is refreshed on its own clock, so a coin is
// not stuck behind a long full-batch pass.
func (s *Server) coinScoreAutoLoop(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.tickCoinScoreAuto(ctx)
	}
}

func (s *Server) tickCoinScoreAuto(ctx context.Context) {
	cfg := s.loadCoinScoreAuto()
	if !cfg.Enabled {
		return
	}
	s.coinScore.mu.Lock()
	if s.coinScore.manualRunning {
		s.coinScore.mu.Unlock()
		return // a manual full run is in progress; don't compete
	}
	s.coinScore.mu.Unlock()

	interval := cfg.IntervalMinutes
	if interval < 1 {
		interval = 60
	}
	conc := cfg.Concurrency
	if conc < 1 {
		conc = 4
	}
	model, uid, err := s.resolveScoreModelAny("")
	if err != nil {
		logger.Warnf("coin-score: resolve model failed: %v", err)
		return
	}

	minVol := cfg.minVolume()
	snaps := s.copyTrade.Snapshots()
	universe := s.coinUniverse(ctx, minVol, cfg.TopN)

	now := time.Now().UnixMilli()
	intervalMs := int64(interval) * 60 * 1000

	s.coinScore.mu.Lock()
	if s.coinScore.inflight == nil {
		s.coinScore.inflight = map[string]bool{}
	}
	inflightN := len(s.coinScore.inflight)
	s.coinScore.mu.Unlock()

	type dueCoin struct {
		sym  string
		last int64
	}
	fresh := 0
	var due []dueCoin
	for _, sym := range universe {
		var last int64
		if m := s.loadScoreMemory(uid, sym); m != nil {
			last = m.LastAt
		}
		if now-last < intervalMs {
			fresh++
			continue
		}
		s.coinScore.mu.Lock()
		busy := s.coinScore.inflight[sym]
		retryAt := s.coinScore.retryAfter[sym]
		s.coinScore.mu.Unlock()
		if busy {
			continue
		}
		if retryAt > now {
			continue // still in failure backoff window
		}
		due = append(due, dueCoin{sym, last})
	}
	sort.Slice(due, func(i, j int) bool { return due[i].last < due[j].last })

	slots := conc - inflightN
	if slots < 0 {
		slots = 0
	}
	launched := 0
	for _, d := range due {
		if launched >= slots {
			break
		}
		s.coinScore.mu.Lock()
		s.coinScore.inflight[d.sym] = true
		s.coinScore.mu.Unlock()
		launched++
		go func(sym string) {
			cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			if _, e := s.analyzeCoinCore(cctx, uid, model, sym, "1h", ""); e != nil {
				s.coinScoreSetErr(sym + ": " + e.Error())
				s.coinScoreMarkFail(sym)
			} else {
				s.coinScoreClearFail(sym)
			}
			s.coinScore.mu.Lock()
			delete(s.coinScore.inflight, sym)
			s.coinScore.mu.Unlock()
		}(d.sym)
	}

	// lastDiagMs is shared state: read and written under the lock like the
	// rest of the status.
	s.coinScore.mu.Lock()
	diagDue := now-s.coinScore.lastDiagMs > 60000
	if diagDue {
		s.coinScore.lastDiagMs = now
	}
	s.coinScore.total = len(universe)
	s.coinScore.done = fresh
	s.coinScore.lastRun = now
	if launched > 0 {
		s.coinScore.lastCount = launched
	}
	// A manual full run owns the running flag until it finishes; the scheduler
	// must not clear it, or a second manual run can start beside the first.
	if !s.coinScore.manualRunning {
		s.coinScore.running = len(s.coinScore.inflight) > 0
	}
	s.coinScore.mu.Unlock()

	switch {
	case len(universe) == 0:
		if diagDue {
			logger.Warnf("coin-score: universe empty (snapshots=%d, minVol=%.0f)", len(snaps), minVol)
		}
	case launched > 0:
		logger.Infof("coin-score tick: universe=%d due=%d fresh=%d launched=%d conc=%d interval=%dm", len(universe), len(due), fresh, launched, conc, interval)
	case diagDue:
		logger.Infof("coin-score tick idle: universe=%d due=%d fresh=%d conc=%d interval=%dm", len(universe), len(due), fresh, conc, interval)
	}
}

func (s *Server) handleCoinScoreSettingsGet(c *gin.Context) {
	cfg := s.loadCoinScoreAuto()
	s.coinScore.mu.Lock()
	running, done, total, lastRun, lastCount, lastErr := s.coinScore.running, s.coinScore.done, s.coinScore.total,
		s.coinScore.lastRun, s.coinScore.lastCount, s.coinScore.lastErr
	s.coinScore.mu.Unlock()
	modelName := ""
	if m, _, err := s.resolveScoreModelAny(c.GetString("user_id")); err == nil {
		modelName = m.Name
	}
	coins := 0
	if s.copyTrade != nil {
		coins = len(copytrade.Symbols(s.copyTrade.Snapshots()))
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled": cfg.Enabled, "interval_minutes": cfg.IntervalMinutes, "pool_auto": cfg.PoolAuto, "top_n": cfg.TopN, "concurrency": cfg.Concurrency, "min_quote_volume": cfg.MinQuoteVolume, "effective_min_quote_volume": cfg.minVolume(), "pool_refresh_minutes": cfg.PoolRefreshMinutes, "pool_ttl_minutes": cfg.PoolTTLMinutes, "pool_rank_mode": cfg.rankMode(), "min_interval": coinScoreMinInterval,
		"running": running, "done": done, "total": total, "last_run": lastRun, "last_count": lastCount,
		"last_error": lastErr, "model": modelName, "coin_count": coins,
	})
}

func (s *Server) handleCoinScoreSettingsPut(c *gin.Context) {
	// Merge-style: only fields present in the body are changed, so a partial
	// update never wipes the others (e.g. min volume when toggling pool_auto).
	var req struct {
		Enabled            *bool    `json:"enabled"`
		IntervalMinutes    *int     `json:"interval_minutes"`
		PoolAuto           *bool    `json:"pool_auto"`
		TopN               *int     `json:"top_n"`
		Concurrency        *int     `json:"concurrency"`
		MinQuoteVolume     *float64 `json:"min_quote_volume"`
		PoolRefreshMinutes *int     `json:"pool_refresh_minutes"`
		PoolTTLMinutes     *int     `json:"pool_ttl_minutes"`
		PoolRankMode       *string  `json:"pool_rank_mode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "invalid settings body")
		return
	}
	cfg := s.loadCoinScoreAuto()
	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}
	if req.IntervalMinutes != nil {
		cfg.IntervalMinutes = *req.IntervalMinutes
	}
	if req.PoolAuto != nil {
		cfg.PoolAuto = *req.PoolAuto
	}
	if req.TopN != nil {
		cfg.TopN = *req.TopN
	}
	if req.Concurrency != nil {
		cfg.Concurrency = *req.Concurrency
	}
	if req.MinQuoteVolume != nil {
		cfg.MinQuoteVolume = *req.MinQuoteVolume
	}
	if req.PoolRefreshMinutes != nil {
		cfg.PoolRefreshMinutes = *req.PoolRefreshMinutes
	}
	if req.PoolTTLMinutes != nil {
		cfg.PoolTTLMinutes = *req.PoolTTLMinutes
	}
	if req.PoolRankMode != nil {
		switch *req.PoolRankMode {
		case poolRankMeasured, poolRankConviction:
			cfg.PoolRankMode = *req.PoolRankMode
		default:
			SafeBadRequest(c, "pool_rank_mode must be measured or conviction")
			return
		}
	}
	if cfg.IntervalMinutes < coinScoreMinInterval {
		cfg.IntervalMinutes = coinScoreMinInterval
	}
	if cfg.IntervalMinutes > 1440 {
		cfg.IntervalMinutes = 1440
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	if cfg.MinQuoteVolume < 0 {
		cfg.MinQuoteVolume = 0
	}
	if cfg.PoolRefreshMinutes < 1 {
		cfg.PoolRefreshMinutes = 5
	}
	if cfg.PoolTTLMinutes < 1 {
		cfg.PoolTTLMinutes = 12
	}
	if cfg.PoolTTLMinutes < cfg.PoolRefreshMinutes {
		cfg.PoolTTLMinutes = cfg.PoolRefreshMinutes * 2
	}
	raw, _ := json.Marshal(cfg)
	if err := s.store.SetSystemConfig(coinScoreAutoKey, string(raw)); err != nil {
		SafeInternalError(c, "Save coin-score settings", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": cfg.Enabled, "interval_minutes": cfg.IntervalMinutes,
		"pool_auto": cfg.PoolAuto, "top_n": cfg.TopN, "concurrency": cfg.Concurrency, "min_quote_volume": cfg.MinQuoteVolume, "effective_min_quote_volume": cfg.minVolume(), "pool_refresh_minutes": cfg.PoolRefreshMinutes, "pool_ttl_minutes": cfg.PoolTTLMinutes, "pool_rank_mode": cfg.rankMode()})
}

func (s *Server) handleCoinScoreRunAll(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	// coinScoreBegin refuses a second run; say so instead of reporting started.
	if !s.coinScoreBegin() {
		c.JSON(http.StatusOK, gin.H{"started": false, "reason": "a scoring run is already in progress"})
		return
	}
	go s.runCoinScoreAll(context.Background(), c.GetString("user_id"), 0, 0)
	c.JSON(http.StatusOK, gin.H{"started": true})
}

func (s *Server) loadScoreMemory(userID, symbol string) *store.CopyTradeScoreMemory {
	m, err := s.store.CopyTrade().GetScoreMemory(userID, symbol)
	if err != nil {
		return nil
	}
	return m
}

func (s *Server) saveScoreMemory(userID, symbol string, res coinScoreResult) {
	if res.Analysis == nil {
		return
	}
	a := res.Analysis
	thesis := a.MarketContext
	if thesis == "" && len(a.Reasons) > 0 {
		thesis = a.Reasons[0]
	}
	m := &store.CopyTradeScoreMemory{UserID: userID, Symbol: symbol, Bias: a.Bias,
		Bull: a.BullScore, Bear: a.BearScore, Conviction: a.Conviction, SmoothedEdge: a.SmoothedEdge,
		Invalidation: a.Invalidates, Thesis: thesis, LastAt: res.AnalyzedAt, LastPrice: res.Price}
	if err := s.store.CopyTrade().SaveScoreMemory(m); err != nil {
		logger.Warnf("copy-trade score memory: save %s: %v", symbol, err)
	}
}

func parseCoinScore(raw string) (*coinScoreAnalysis, error) {
	text := raw
	if i := strings.LastIndex(text, "</think>"); i >= 0 {
		text = text[i+len("</think>"):]
	}
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object in AI reply")
	}
	var a coinScoreAnalysis
	if err := json.Unmarshal([]byte(text[start:end+1]), &a); err != nil {
		return nil, err
	}
	a.Bias = strings.ToLower(strings.TrimSpace(a.Bias))
	a.Horizon = strings.ToLower(strings.TrimSpace(a.Horizon))
	if a.Horizon != "short" && a.Horizon != "swing" {
		a.Horizon = ""
	}
	// v2 scores; fall back to legacy single score for old-style replies.
	if a.BullScore == 0 && a.BearScore == 0 && a.Score > 0 {
		switch a.Bias {
		case "short":
			a.BearScore = a.Score
		default:
			a.BullScore = a.Score
		}
	}
	a.BullScore = clampScore(a.BullScore)
	a.BearScore = clampScore(a.BearScore)
	a.Edge = a.BullScore - a.BearScore
	if a.Bias != "long" && a.Bias != "short" && a.Bias != "neutral" {
		switch {
		case a.Edge > 5:
			a.Bias = "long"
		case a.Edge < -5:
			a.Bias = "short"
		default:
			a.Bias = "neutral"
		}
	}
	a.Conviction = clampScore(absf(a.Edge))
	if a.Conviction == 0 {
		a.Conviction = clampScore(a.Confidence)
	}
	// Derive legacy display fields.
	a.Score = a.BullScore
	if a.BearScore > a.Score {
		a.Score = a.BearScore
	}
	a.Confidence = a.Conviction
	if a.Bias == "short" {
		a.Reasons, a.Risks = a.ShortCase, a.LongCase
	} else {
		a.Reasons, a.Risks = a.LongCase, a.ShortCase
	}
	return &a, nil
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func clampScore(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

type scoreTF struct {
	Interval string
	Klines   []binance.MarketKline
	Ind      indicatorSnapshot
}

func sideExtra(s copytrade.SideSummary) string {
	extra := ""
	if s.Openers > 0 {
		extra += fmt.Sprintf(", %d fresh openers", s.Openers)
	}
	if s.Added > 0 {
		extra += fmt.Sprintf(", %d added", s.Added)
	}
	if s.Trapped > 0 {
		extra += fmt.Sprintf(", %d trapped", s.Trapped)
	}
	return extra
}

// coinScoreBrief is the compact reading shown as a list column.
type coinScoreBrief struct {
	Symbol       string  `json:"symbol"`
	Bias         string  `json:"bias"`
	Horizon      string  `json:"horizon"`
	BullScore    float64 `json:"bull_score"`
	BearScore    float64 `json:"bear_score"`
	Edge         float64 `json:"edge"`
	SmoothedEdge float64 `json:"smoothed_edge"`
	Conviction   float64 `json:"conviction"`
	Score        float64 `json:"score"`
	Confidence   float64 `json:"confidence"`
	AnalyzedAt   int64   `json:"analyzed_at"`
	AgeMs        int64   `json:"age_ms"`
	Stale        bool    `json:"stale"`
	ModelID      string  `json:"model_id"`
}

func (s *Server) handleCoinScoresList(c *gin.Context) {
	rows, err := s.store.CopyTrade().ListCoinScores(c.GetString("user_id"))
	if err != nil {
		SafeInternalError(c, "Read coin scores", err)
		return
	}
	cfg := s.loadCoinScoreAuto()
	// One shared definition of "this reading is still current" (see
	// scoreFreshMs): the program, pool, rankings and status pages must agree,
	// or a coin is tradeable by one and expired by another.
	ttlMs := cfg.scoreFreshMs()
	now := time.Now().UnixMilli()
	out := make([]coinScoreBrief, 0, len(rows))
	for _, r := range rows {
		var res coinScoreResult
		if json.Unmarshal([]byte(r.Payload), &res) != nil || res.Analysis == nil {
			continue
		}
		age := now - r.AnalyzedAt
		out = append(out, coinScoreBrief{Symbol: r.Symbol, Bias: res.Analysis.Bias, Horizon: res.Analysis.Horizon,
			BullScore: res.Analysis.BullScore, BearScore: res.Analysis.BearScore, Edge: res.Analysis.Edge,
			SmoothedEdge: res.Analysis.SmoothedEdge, Conviction: res.Analysis.Conviction, Score: res.Analysis.Score, Confidence: res.Analysis.Confidence,
			AnalyzedAt: r.AnalyzedAt, AgeMs: age, Stale: r.AnalyzedAt > 0 && age > ttlMs, ModelID: r.ModelID})
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

func tfChg(tf scoreTF, lookback int) float64 {
	k := tf.Klines
	if len(k) < 2 {
		return 0
	}
	i := len(k) - 1 - lookback
	if i < 0 {
		i = 0
	}
	if k[i].Close == 0 {
		return 0
	}
	return (k[len(k)-1].Close/k[i].Close - 1) * 100
}

// scoreFeatures is the snapshot of inputs one reading was made from, stored
// with its outcome so rules and weights can be tested offline against what
// actually happened. Values are raw observations, not model output. Fields
// that could not be measured are omitted rather than written as zero.
type scoreFeatures struct {
	Rel1h        *float64 `json:"rel_1h,omitempty"` // coin minus BTC, pct points
	Rel4h        *float64 `json:"rel_4h,omitempty"`
	Rel24h       float64  `json:"rel_24h"`
	Coin4h       *float64 `json:"coin_4h,omitempty"` // coin's own 4h change, %
	Funding      *float64 `json:"funding,omitempty"` // %, per period
	OIChg1h      *float64 `json:"oi_chg_1h,omitempty"`
	LSRatio      *float64 `json:"ls_ratio,omitempty"`
	TakerBS      *float64 `json:"taker_bs,omitempty"`
	BasisPct     *float64 `json:"basis_pct,omitempty"`
	QuoteVol24h  *float64 `json:"quote_vol_24h,omitempty"`
	RSI14_1h     *float64 `json:"rsi14_1h,omitempty"`
	OpenersLong  int      `json:"openers_long"`
	OpenersShort int      `json:"openers_short"`
	TradersLong  int      `json:"traders_long"`
	TradersShort int      `json:"traders_short"`
}

func scoreFeaturesJSON(t *binance.MarketTicker, deriv binance.DerivativeInfo, sum *copytrade.SymbolSummary, rel relStrength, tfs []scoreTF) string {
	f := scoreFeatures{Rel24h: rel.Coin24h - rel.BTC24h}
	p := func(v float64) *float64 { return &v }
	if rel.Has1h {
		f.Rel1h = p(rel.Coin1h - rel.BTC1h)
	}
	if rel.Has4h {
		f.Rel4h, f.Coin4h = p(rel.Coin4h-rel.BTC4h), p(rel.Coin4h)
	}
	if t != nil {
		f.Funding = p(t.FundingRate * 100)
		if t.QuoteVolume24h > 0 {
			f.QuoteVol24h = p(t.QuoteVolume24h)
		}
	}
	if deriv.OpenInterest > 0 {
		f.OIChg1h = p(deriv.OIChangePct1h)
	}
	if deriv.LongShortRatio > 0 {
		f.LSRatio = p(deriv.LongShortRatio)
	}
	if deriv.TakerBuySellRatio > 0 {
		f.TakerBS = p(deriv.TakerBuySellRatio)
	}
	if deriv.OpenInterest > 0 || deriv.BasisPct != 0 {
		f.BasisPct = p(deriv.BasisPct)
	}
	for _, tf := range tfs {
		if tf.Interval == "1h" && tf.Ind.RSI14 > 0 {
			f.RSI14_1h = p(tf.Ind.RSI14)
		}
	}
	if sum != nil {
		f.OpenersLong, f.OpenersShort = sum.Long.Openers, sum.Short.Openers
		f.TradersLong, f.TradersShort = sum.Long.Traders, sum.Short.Traders
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return ""
	}
	return string(raw)
}

// errUnparseableReading reports a model reply that could not be read as a
// score. The result still carries the raw text for the manual analyze view.
var errUnparseableReading = errors.New("AI reply was not valid JSON")

// relStrength is the coin's return minus BTC's over several windows, in
// percentage points. Positive = stronger than BTC over that window.
type relStrength struct {
	Coin1h, Coin4h, Coin24h float64
	BTC1h, BTC4h, BTC24h    float64
	Has1h, Has4h            bool
}

// relativeStrength measures the coin against BTC over 1h, 4h and 24h. The 1h
// and 4h windows come from the coin's own already-fetched 1h candles and one
// extra BTC 1h request; 24h comes from the tickers. A window that cannot be
// measured on both legs is marked absent rather than reported as zero.
func relativeStrength(ctx context.Context, tfs []scoreTF, coin24, btc24 float64) relStrength {
	r := relStrength{Coin24h: coin24, BTC24h: btc24}
	var coin1h *scoreTF
	for i := range tfs {
		if tfs[i].Interval == "1h" {
			coin1h = &tfs[i]
		}
	}
	if coin1h == nil {
		return r
	}
	btc, err := binance.GetPublicKlines(ctx, "BTCUSDT", "1h", 6)
	if err != nil || len(btc) < 5 {
		return r
	}
	b := scoreTF{Interval: "1h", Klines: btc}
	if len(coin1h.Klines) >= 2 {
		r.Coin1h, r.BTC1h, r.Has1h = tfChg(*coin1h, 1), tfChg(b, 1), true
	}
	if len(coin1h.Klines) >= 5 {
		r.Coin4h, r.BTC4h, r.Has4h = tfChg(*coin1h, 4), tfChg(b, 4), true
	}
	return r
}

// smoothEdge blends the new edge with the previous smoothed edge. The newest
// reading dominates (0.7), and a previous reading older than one 4h horizon is
// dropped: it describes a market the 4h call no longer covers.
func smoothEdge(edge float64, prev *store.CopyTradeScoreMemory, nowMs int64) float64 {
	if prev == nil || prev.LastAt <= 0 || nowMs-prev.LastAt > 4*3600*1000 {
		return edge
	}
	return 0.7*edge + 0.3*prev.SmoothedEdge
}

// buildCoinScorePrompt builds the per-coin 4h reading.
//
// Changed from the previous version, based on the user's own stored outcomes:
//   - The copy-trade crowd's consensus confidence, rank score and per-holder
//     list are no longer shown. Over ~15k mature 4h signals they had no
//     directional edge (about 50% hit, ~0 excess over BTC), and the highest
//     confidence band did worse than average, so they were noise in the input.
//     Only the count of FRESH openers per side stays, labelled as weak, since
//     fresh calls were the one subset marginally above 50%.
//   - The previous reading is shown as context only, without the instruction to
//     keep it absent new evidence. Combined with heavy smoothing, that anchored
//     a 4h call to readings hours old.
//   - Relative strength vs BTC is given over 1h and 4h as well as 24h, because
//     the call is about the next 4h and a 24h comparison mostly reflects moves
//     that already happened.
//   - The task is framed as ranking against the rest of the market over 4h,
//     because absolute 4h direction in the stored history was mostly the
//     market's own move.
func buildCoinScorePrompt(lang, symbol string, tfs []scoreTF, t *binance.MarketTicker, deriv binance.DerivativeInfo, regime string, sum *copytrade.SymbolSummary, holders []copytrade.Holder, prev *store.CopyTradeScoreMemory, rel relStrength) (string, string) {
	language := "English"
	if lang == "zh" {
		language = "Simplified Chinese"
	}
	_ = holders // per-holder detail removed from the prompt; see doc comment
	system := `You are a disciplined crypto perpetual futures analyst. Judge ONE coin's likely move over the NEXT 4 HOURS RELATIVE TO THE REST OF THE MARKET, from BOTH sides with EQUAL rigor, using multi-timeframe price structure, derivatives positioning, and relative strength vs BTC.
Reply with ONE strict JSON object and nothing else:
{"bull_score":0-100,"bear_score":0-100,"bias":"long|short|neutral","conviction":0-100,"horizon":"short|swing","tf_alignment":{"15m":-1|0|1,"1h":-1|0|1,"4h":-1|0|1},"long_case":["..."],"short_case":["..."],"invalidates":"one sentence","market_context":"one sentence","changed":true|false,"change_reason":"..."}
What the scores mean:
- bull_score: strength of evidence that over the next 4h this coin does BETTER than the broad market (rises more, or falls less). bear_score: evidence it does WORSE. Judge the coin against the market, not in isolation: in a market where nearly everything falls, a coin falling much less is relatively strong, and its absolute price can still fall.
- Score both sides INDEPENDENTLY and with EQUAL weight. Neither side is the default.
- bias = the side with the clearly higher score. Use neutral when |bull_score - bear_score| <= 10, or when evidence on the 4h horizon is thin or conflicting. Neutral is an honest answer; do not force a side.
- conviction = |bull_score - bear_score|. Scores are an evidence ranking, NOT win probabilities or expected returns, and are checked later against measured outcomes.
Evidence to weigh (each is one input; none decides alone):
- Relative strength vs BTC over 1h and 4h matters most for a 4h call; 24h is background that has largely already happened.
- Price structure on 15m/1h/4h: trend vs MA/EMA, higher lows or lower highs, breakouts or breakdowns, rejection at levels, divergences. Prefer agreement across 1h and 4h.
- Derivatives: funding sign and size (crowded side), open interest change with price (new positions vs unwinds), taker buy/sell, basis.
- Lead-trader fresh openers (if shown) are WEAK evidence: historically close to a coin flip. Never let them decide the side.
Re-evaluation:
- A PREVIOUS READING, if shown, is context only. Re-judge from the current data; do not keep the old side just because it was the old side. Set changed=true and give change_reason when the side differs from the previous reading.
Rules:
- Missing or stale evidence lowers conviction.
- State a concrete, observable invalidation for the next 4h.
- NEVER give entry/stop/take-profit and never instruct an order. Conditional wording only.
- Write long_case, short_case, invalidates and market_context in ` + language + `.`

	var b strings.Builder
	fmt.Fprintf(&b, "Coin: %s (Binance USDT-M perpetual)\n", symbol)
	if t != nil {
		fmt.Fprintf(&b, "Last %s  mark %s  24h %.2f%%  24h high/low %s/%s  24h vol %s USDT  funding %.4f%%\n",
			fmtNum(t.LastPrice), fmtNum(t.MarkPrice), t.PriceChangePct, fmtNum(t.High24h), fmtNum(t.Low24h), fmtNum(t.QuoteVolume24h), t.FundingRate*100)
	}
	if deriv.OpenInterest > 0 || deriv.LongShortRatio > 0 || deriv.TakerBuySellRatio > 0 {
		fmt.Fprintf(&b, "Derivatives: OI %s (1h %+.2f%%), long/short ratio %.2f (long %.0f%%/short %.0f%%), taker buy/sell %.2f, basis %+.4f%%\n", fmtNum(deriv.OpenInterest), deriv.OIChangePct1h, deriv.LongShortRatio, deriv.LongAccountPct, deriv.ShortAccountPct, deriv.TakerBuySellRatio, deriv.BasisPct)
	}
	if regime != "" {
		fmt.Fprintf(&b, "BTC context: %s\n", regime)
	}
	b.WriteString("Relative strength vs BTC (coin change minus BTC change, percentage points; positive = stronger than BTC):\n")
	if rel.Has1h {
		fmt.Fprintf(&b, "- 1h: coin %+.2f%% vs BTC %+.2f%% => %+.2f pts\n", rel.Coin1h, rel.BTC1h, rel.Coin1h-rel.BTC1h)
	} else {
		b.WriteString("- 1h: unavailable\n")
	}
	if rel.Has4h {
		fmt.Fprintf(&b, "- 4h: coin %+.2f%% vs BTC %+.2f%% => %+.2f pts\n", rel.Coin4h, rel.BTC4h, rel.Coin4h-rel.BTC4h)
	} else {
		b.WriteString("- 4h: unavailable\n")
	}
	fmt.Fprintf(&b, "- 24h (background): coin %+.2f%% vs BTC %+.2f%% => %+.2f pts\n", rel.Coin24h, rel.BTC24h, rel.Coin24h-rel.BTC24h)
	for _, tf := range tfs {
		fmt.Fprintf(&b, "\n[%s] MA7 %s MA25 %s MA99 %s EMA12 %s EMA26 %s MACD %s/%s/%s RSI6 %.1f RSI14 %.1f KDJ %.1f/%.1f/%.1f BOLL %s/%s/%s ATR %s\n",
			tf.Interval, fmtNum(tf.Ind.MA7), fmtNum(tf.Ind.MA25), fmtNum(tf.Ind.MA99), fmtNum(tf.Ind.EMA12), fmtNum(tf.Ind.EMA26),
			fmtNum(tf.Ind.MACD), fmtNum(tf.Ind.MACDSignal), fmtNum(tf.Ind.MACDH), tf.Ind.RSI6, tf.Ind.RSI14, tf.Ind.K, tf.Ind.D, tf.Ind.J,
			fmtNum(tf.Ind.BollUp), fmtNum(tf.Ind.BollMid), fmtNum(tf.Ind.BollDn), fmtNum(tf.Ind.ATR14))
		recent := tf.Klines
		if len(recent) > 40 {
			recent = recent[len(recent)-40:]
		}
		b.WriteString("candles(time,o,h,l,c,v):\n")
		for _, c := range recent {
			fmt.Fprintf(&b, "%s,%s,%s,%s,%s,%s\n", time.Unix(c.Time, 0).UTC().Format("01-02 15:04"),
				fmtNum(c.Open), fmtNum(c.High), fmtNum(c.Low), fmtNum(c.Close), fmtNum(c.Volume))
		}
	}

	// Only fresh openers survive from the copy-trade block: they were the one
	// subset marginally above a coin flip, and are labelled as weak evidence.
	b.WriteString("\nLead-trader fresh openers (WEAK evidence, historically close to a coin flip):\n")
	if sum == nil || (sum.Long.Openers == 0 && sum.Short.Openers == 0) {
		b.WriteString("none recently.\n")
	} else {
		fmt.Fprintf(&b, "fresh long openers %d, fresh short openers %d\n", sum.Long.Openers, sum.Short.Openers)
	}
	if prev != nil {
		fmt.Fprintf(&b, "\nPREVIOUS READING (context only, %s, price %s): bias=%s bull=%.0f bear=%.0f\n",
			time.UnixMilli(prev.LastAt).UTC().Format("01-02 15:04"), fmtNum(prev.LastPrice), prev.Bias, prev.Bull, prev.Bear)
	}
	return system, b.String()
}

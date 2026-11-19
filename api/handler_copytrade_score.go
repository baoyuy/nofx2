package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"nofx/copytrade"
	"nofx/logger"
	"nofx/store"
	"nofx/trader/binance"
)

// Per-coin AI reading for the copy-trade "by coin" page. Read-only guidance:
// direction + 0-100 score + reasons/risks, no entry/stop, never feeds an order.

const copytradeScoreModelKey = "copytrade_score_model"

// coinScoreAuto is the in-memory status of the analyze-all run.
type coinScoreAuto struct {
	mu        sync.Mutex
	running   bool
	lastRun   int64
	lastCount int
	lastErr   string
	done      int
	total     int
}


type coinScoreAnalysis struct {
	Bias        string   `json:"bias"` // long | short | neutral
	Score       float64  `json:"score"`
	Horizon     string   `json:"horizon"` // short | swing
	Confidence  float64  `json:"confidence"`
	Reasons     []string `json:"reasons"`
	Risks       []string `json:"risks"`
	Invalidates string   `json:"invalidates"`
	Sources     []string `json:"sources,omitempty"`
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
	s.route(protected, "GET", "/copytrade/coin-score", "Saved AI reading for one coin (?symbol=)", s.handleCoinScoreGet)
	s.route(protected, "POST", "/copytrade/coin-score", "Run the AI reading for one coin now (body: symbol, interval?, model_id?, lang?)", s.handleCoinScoreAnalyze)
	s.route(protected, "GET", "/copytrade/coin-score/models", "Enabled models and the saved coin-score model", s.handleCoinScoreModels)
	s.route(protected, "PUT", "/copytrade/coin-score/model", "Set the coin-score model (body: model_id)", s.handleCoinScoreModelPut)
	s.route(protected, "GET", "/copytrade/coin-scores", "Saved AI readings for every coin (list column)", s.handleCoinScoresList)
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
	if err != nil {
		SafeError(c, http.StatusBadGateway, "AI call failed: "+SanitizeError(err, "check the AI connection settings"), err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// analyzeCoinCore builds the copy-trade-aware prompt, calls the model, parses
// the strict JSON and saves the reading. A parse failure is kept in the result
// (with the raw text); only a transport/AI error is returned as err.
func (s *Server) analyzeCoinCore(ctx context.Context, userID string, model *store.AIModel, symbol, interval, lang string) (coinScoreResult, error) {
	klines, err := binance.GetPublicKlines(ctx, symbol, interval, 200)
	if err != nil || len(klines) < 30 {
		return coinScoreResult{}, fmt.Errorf("not enough candles for %s: %v", symbol, err)
	}
	ticker, _ := binance.GetPublicTicker(ctx, symbol)
	ind := computeIndicators(klines)
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
	system, user := buildCoinScorePrompt(lang, symbol, interval, ticker, klines, ind, sum, holders)
	raw, err := s.runRealAITest(userID, model.ID, system, user)
	if err != nil {
		return coinScoreResult{}, err
	}
	res := coinScoreResult{Symbol: symbol, Interval: interval, ModelID: model.ID, ModelName: model.Name,
		AnalyzedAt: time.Now().UnixMilli(), Price: klines[len(klines)-1].Close, Raw: raw}
	if a, perr := parseCoinScore(raw); perr != nil {
		res.ParseError = "The AI reply was not valid JSON; showing the raw text."
	} else {
		res.Analysis = a
	}
	if data, merr := json.Marshal(res); merr == nil {
		row := store.CopyTradeCoinScore{UserID: userID, Symbol: symbol, ModelID: model.ID, Interval: interval,
			Payload: string(data), AnalyzedAt: res.AnalyzedAt}
		if serr := s.store.CopyTrade().SaveCoinScore(&row); serr != nil {
			logger.Warnf("copy-trade coin score: save %s: %v", symbol, serr)
			res.ParseError = strings.TrimSpace(res.ParseError + " (result not saved)")
		}
	}
	return res, nil
}

// ---- scheduled / manual analyze-all ----

const (
	coinScoreAutoKey       = "copytrade_coin_score_auto"
	coinScoreMinInterval   = 5
	coinScoreDefaultMins   = 60
)

type coinScoreAutoConfig struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"interval_minutes"`
}

func (s *Server) loadCoinScoreAuto() coinScoreAutoConfig {
	cfg := coinScoreAutoConfig{IntervalMinutes: coinScoreDefaultMins}
	if raw, err := s.store.GetSystemConfig(coinScoreAutoKey); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	if cfg.IntervalMinutes < coinScoreMinInterval {
		cfg.IntervalMinutes = coinScoreDefaultMins
	}
	return cfg
}

// resolveScoreModelAny picks the model and owning user for a batch run. A
// request-scoped run passes the user; the scheduler (no request) uses any
// enabled model and its owner.
func (s *Server) resolveScoreModelAny(userID string) (*store.AIModel, string, error) {
	if userID != "" {
		m, err := s.scoreModel(userID, "")
		if err != nil {
			return nil, "", err
		}
		return m, userID, nil
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
	s.coinScore.done, s.coinScore.total = 0, 0
	s.coinScore.lastErr = ""
	return true
}
func (s *Server) coinScoreSetTotal(n int) { s.coinScore.mu.Lock(); s.coinScore.total = n; s.coinScore.mu.Unlock() }
func (s *Server) coinScoreInc()         { s.coinScore.mu.Lock(); s.coinScore.done++; s.coinScore.mu.Unlock() }
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
	s.coinScore.lastRun = time.Now().UnixMilli()
	s.coinScore.lastCount = ok
	if lastErr != "" {
		s.coinScore.lastErr = lastErr
	}
	s.coinScore.mu.Unlock()
}

// runCoinScoreAll analyzes every coin currently on the by-coin page. Runs at
// most one at a time; the scheduler and the manual button share this.
func (s *Server) runCoinScoreAll(ctx context.Context, userID string) {
	if !s.coinScoreBegin() {
		return
	}
	model, uid, err := s.resolveScoreModelAny(userID)
	if err != nil {
		s.coinScoreFinish(0, "no AI model: "+err.Error())
		return
	}
	var symbols []string
	for _, x := range copytrade.Symbols(s.copyTrade.Snapshots()) {
		symbols = append(symbols, x.Symbol)
	}
	s.coinScoreSetTotal(len(symbols))
	sem := make(chan struct{}, 4)
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
				} else {
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
}

// StartCoinScoreAuto starts the scheduled analyze-all loop. Runs on app start.
func (s *Server) StartCoinScoreAuto(ctx context.Context) {
	go s.coinScoreAutoLoop(ctx)
}

func (s *Server) coinScoreAutoLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	var next time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cfg := s.loadCoinScoreAuto()
		if !cfg.Enabled {
			next = time.Time{}
			continue
		}
		now := time.Now()
		if next.IsZero() {
			next = now // run soon after enabling, then every interval
		}
		if now.Before(next) {
			continue
		}
		next = now.Add(time.Duration(cfg.IntervalMinutes) * time.Minute)
		go s.runCoinScoreAll(ctx, "")
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
		"enabled": cfg.Enabled, "interval_minutes": cfg.IntervalMinutes, "min_interval": coinScoreMinInterval,
		"running": running, "done": done, "total": total, "last_run": lastRun, "last_count": lastCount,
		"last_error": lastErr, "model": modelName, "coin_count": coins,
	})
}

func (s *Server) handleCoinScoreSettingsPut(c *gin.Context) {
	var req coinScoreAutoConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "enabled and interval_minutes are required")
		return
	}
	if req.IntervalMinutes < coinScoreMinInterval {
		req.IntervalMinutes = coinScoreMinInterval
	}
	if req.IntervalMinutes > 1440 {
		req.IntervalMinutes = 1440
	}
	raw, _ := json.Marshal(req)
	if err := s.store.SetSystemConfig(coinScoreAutoKey, string(raw)); err != nil {
		SafeInternalError(c, "Save coin-score settings", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"enabled": req.Enabled, "interval_minutes": req.IntervalMinutes})
}

func (s *Server) handleCoinScoreRunAll(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	go s.runCoinScoreAll(context.Background(), c.GetString("user_id"))
	c.JSON(http.StatusOK, gin.H{"started": true})
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
	switch a.Bias {
	case "long", "short", "neutral":
	default:
		a.Bias = "neutral"
	}
	a.Horizon = strings.ToLower(strings.TrimSpace(a.Horizon))
	switch a.Horizon {
	case "short", "swing":
	default:
		a.Horizon = ""
	}
	a.Score = clampScore(a.Score)
	a.Confidence = clampScore(a.Confidence)
	return &a, nil
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

func buildCoinScorePrompt(lang, symbol, interval string, t *binance.MarketTicker, k []binance.MarketKline, ind indicatorSnapshot, sum *copytrade.SymbolSummary, holders []copytrade.Holder) (string, string) {
	language := "English"
	if lang == "zh" {
		language = "Simplified Chinese"
	}
	system := `You are a disciplined crypto perpetual futures analyst. You judge ONE coin using the copy-trade consensus and the technicals provided. ` +
		`Reply with ONE strict JSON object and nothing else:
{"bias":"long|short|neutral","score":0-100,"horizon":"short|swing","confidence":0-100,"reasons":["..."],"risks":["..."],"invalidates":"one sentence","sources":["copytrade","technicals"]}
Rules: score is the strength/credibility of the bias, 0-100 (>=70 strong, 40-70 mixed, <40 weak). ` +
		`Use "neutral" when unclear. Do NOT give entry, stop-loss or take-profit prices, and do not tell the user to place an order. ` +
		`State conditionally ("if you are trading X, then ..."), never as certainty. When copy-trade consensus and technicals disagree, say so and say which you weight more. ` +
		`Write reasons, risks and invalidates in ` + language + `.`

	var b strings.Builder
	fmt.Fprintf(&b, "Coin: %s (Binance USDT-M perpetual)\nTimeframe: %s\n", symbol, interval)
	if t != nil {
		fmt.Fprintf(&b, "Last price: %s  Mark: %s  24h change: %.2f%%  24h high/low: %s / %s  24h quote volume: %s USDT  Funding rate: %.4f%%\n",
			fmtNum(t.LastPrice), fmtNum(t.MarkPrice), t.PriceChangePct, fmtNum(t.High24h), fmtNum(t.Low24h), fmtNum(t.QuoteVolume24h), t.FundingRate*100)
	}
	fmt.Fprintf(&b, "\nIndicators (last bar):\nMA7 %s  MA25 %s  MA99 %s\nEMA12 %s  EMA26 %s\nMACD DIF %s  DEA %s  HIST %s\nRSI6 %.2f  RSI14 %.2f\nKDJ K %.2f  D %.2f  J %.2f\nBOLL up %s  mid %s  low %s\nATR14 %s\n",
		fmtNum(ind.MA7), fmtNum(ind.MA25), fmtNum(ind.MA99), fmtNum(ind.EMA12), fmtNum(ind.EMA26),
		fmtNum(ind.MACD), fmtNum(ind.MACDSignal), fmtNum(ind.MACDH), ind.RSI6, ind.RSI14, ind.K, ind.D, ind.J,
		fmtNum(ind.BollUp), fmtNum(ind.BollMid), fmtNum(ind.BollDn), fmtNum(ind.ATR14))

	recent := k
	if len(recent) > 40 {
		recent = recent[len(recent)-40:]
	}
	b.WriteString("\nLast candles (UTC time, open, high, low, close, volume):\n")
	for _, c := range recent {
		fmt.Fprintf(&b, "%s,%s,%s,%s,%s,%s\n", time.Unix(c.Time, 0).UTC().Format("01-02 15:04"),
			fmtNum(c.Open), fmtNum(c.High), fmtNum(c.Low), fmtNum(c.Close), fmtNum(c.Volume))
	}

	b.WriteString("\nFollowed copy-trade consensus (this is the lead-trader crowd):\n")
	if sum == nil {
		b.WriteString("no followed trader currently holds or traded this coin.\n")
	} else {
		fmt.Fprintf(&b, "Long: %d traders, %s USDT%s\n", sum.Long.Traders, fmtNum(sum.Long.Value), sideExtra(sum.Long))
		fmt.Fprintf(&b, "Short: %d traders, %s USDT%s\n", sum.Short.Traders, fmtNum(sum.Short.Value), sideExtra(sum.Short))
		fmt.Fprintf(&b, "Confidence: %s %.0f/100 (agreement %.2f, quality %.2f, health %.2f, long share %.2f)\n",
			sum.Confidence.Direction, sum.Confidence.Score, sum.Confidence.Agreement, sum.Confidence.Quality, sum.Confidence.Health, sum.Confidence.LongShare)
		fmt.Fprintf(&b, "Rank score: %.0f dir=%s strength=%.0f independence=%.2f crowding=%.2f liquidity=%.2f entry=%.2f\n",
			sum.Rank.Score, sum.Rank.Direction, sum.Rank.Strength, sum.Rank.Independence, sum.Rank.Crowding, sum.Rank.Liquidity, sum.Rank.Entry)
		fmt.Fprintf(&b, "Fresh-call (opens/adds): dir=%s score=%.0f longCalls=%d shortCalls=%d burst=%d entryGapPct=%.2f\n",
			sum.Shadow.Direction, sum.Shadow.Score, sum.Shadow.LongCalls, sum.Shadow.ShortCalls, sum.Shadow.Burst, sum.Shadow.EntryGapPct)
		if sum.HighFreq > 0 {
			fmt.Fprintf(&b, "Note: %d high-frequency holders (weighted down).\n", sum.HighFreq)
		}
		fmt.Fprintf(&b, "24h quote volume: %s USDT\n", fmtNum(sum.Volume24h))
	}
	if len(holders) > 0 {
		b.WriteString("Top holders:\n")
		n := len(holders)
		if n > 5 {
			n = 5
		}
		for _, h := range holders[:n] {
			fmt.Fprintf(&b, "- %s %s %s USDT entry=%s return=%.1f%% style=%s score=%.0f\n",
				h.Nickname, h.Side, fmtNum(h.EntryValue), fmtNum(h.EntryPrice), h.ReturnPct, h.Style, h.Score)
		}
	}
	return system, b.String()
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
	Symbol     string  `json:"symbol"`
	Bias       string  `json:"bias"`
	Horizon    string  `json:"horizon"`
	Score      float64 `json:"score"`
	Confidence float64 `json:"confidence"`
	AnalyzedAt int64   `json:"analyzed_at"`
	ModelID    string  `json:"model_id"`
}

func (s *Server) handleCoinScoresList(c *gin.Context) {
	rows, err := s.store.CopyTrade().ListCoinScores(c.GetString("user_id"))
	if err != nil {
		SafeInternalError(c, "Read coin scores", err)
		return
	}
	out := make([]coinScoreBrief, 0, len(rows))
	for _, r := range rows {
		var res coinScoreResult
		if json.Unmarshal([]byte(r.Payload), &res) != nil || res.Analysis == nil {
			continue
		}
		out = append(out, coinScoreBrief{Symbol: r.Symbol, Bias: res.Analysis.Bias, Horizon: res.Analysis.Horizon,
			Score: res.Analysis.Score, Confidence: res.Analysis.Confidence, AnalyzedAt: r.AnalyzedAt, ModelID: r.ModelID})
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

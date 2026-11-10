package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nofx/logger"
	"nofx/trader"
	"nofx/trader/binance"

	"github.com/gin-gonic/gin"
)

// Market page: public Binance data, AI analysis, and manual orders on a
// Binance (live) or Binance Paper account.

func (s *Server) registerMarketRoutes(api, protected *gin.RouterGroup) {
	s.route(api, "GET", "/market/klines", "Binance USDT-M candles (?symbol=&interval=&limit=)", s.handleMarketKlines)
	s.route(api, "GET", "/market/depth", "Binance USDT-M L2 order book (?symbol=&limit=)", s.handleMarketDepth)
	s.route(api, "GET", "/market/ticker", "Binance USDT-M 24h ticker, mark price and funding (?symbol=)", s.handleMarketTicker)
	s.route(api, "GET", "/market/symbols", "Tradable Binance USDT perpetual symbols", s.handleMarketSymbols)
	s.route(api, "GET", "/market/tickers", "Price and 24h change of all Binance USDT perpetuals", s.handleMarketTickers)
	s.route(protected, "POST", "/market/analyze", "AI analysis of a symbol's chart (body: symbol, interval, model_id, lang)", s.handleMarketAnalyze)
	s.route(protected, "GET", "/market/analyses", "Saved latest AI analysis per symbol for the current user", s.handleMarketAnalyses)
	s.route(protected, "GET", "/market/account", "Balance and positions of a Binance or Binance Paper account (?exchange_id=)", s.handleMarketAccount)
	s.route(protected, "POST", "/market/order", "Open a manual position (live accounts require confirm_live=true)", s.handleMarketOrder)
	s.route(protected, "POST", "/market/close", "Close a manual position (body: exchange_id, symbol, side)", s.handleMarketClose)
}

func marketSymbol(c *gin.Context, raw string) (string, bool) {
	symbol := binance.NormalizeUSDTSymbol(raw)
	if symbol == "" || len(symbol) > 20 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid symbol"})
		return "", false
	}
	for _, r := range symbol {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid symbol"})
			return "", false
		}
	}
	return symbol, true
}

func (s *Server) handleMarketKlines(c *gin.Context) {
	symbol, ok := marketSymbol(c, c.DefaultQuery("symbol", "BTCUSDT"))
	if !ok {
		return
	}
	interval := c.DefaultQuery("interval", "15m")
	if !binance.ValidMarketIntervals[interval] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported interval"})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "500"))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	klines, err := binance.GetPublicKlines(ctx, symbol, interval, limit)
	if err != nil {
		SafeError(c, http.StatusBadGateway, "Could not load Binance candles. Check the network or proxy.", err)
		return
	}
	c.JSON(http.StatusOK, klines)
}

func (s *Server) handleMarketDepth(c *gin.Context) {
	symbol, ok := marketSymbol(c, c.DefaultQuery("symbol", "BTCUSDT"))
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	depth, err := binance.GetPublicDepth(ctx, symbol, limit)
	if err != nil {
		// Binance answers 400 (code -1121) for symbols it doesn't list; pass
		// that through as a 4xx so the client can fall back to another venue.
		if strings.Contains(err.Error(), `"code":-1121`) {
			SafeError(c, http.StatusNotFound, "Symbol is not a Binance USDT perpetual.", err)
			return
		}
		SafeError(c, http.StatusBadGateway, "Could not load Binance depth.", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"symbol": symbol, "bids": depth.Bids, "asks": depth.Asks})
}

func (s *Server) handleMarketTicker(c *gin.Context) {
	symbol, ok := marketSymbol(c, c.DefaultQuery("symbol", "BTCUSDT"))
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	ticker, err := binance.GetPublicTicker(ctx, symbol)
	if err != nil {
		SafeError(c, http.StatusBadGateway, "Could not load the Binance ticker. Check the network or proxy.", err)
		return
	}
	c.JSON(http.StatusOK, ticker)
}

func (s *Server) handleMarketSymbols(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	symbols, err := binance.GetPublicSymbols(ctx)
	if err != nil {
		SafeError(c, http.StatusBadGateway, "Could not load Binance symbols.", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"symbols": symbols})
}

func (s *Server) handleMarketTickers(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	tickers, err := binance.GetPublicTickers(ctx)
	if err != nil {
		SafeError(c, http.StatusBadGateway, "Could not load Binance tickers. Check the network or proxy.", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tickers": tickers})
}

// marketTrader returns a trader for an owned Binance / Binance Paper account.
// forTrading builds the full order client; otherwise a read-only one.
func (s *Server) marketTrader(c *gin.Context, exchangeID string, forTrading bool) (trader.Trader, string, bool) {
	userID := c.GetString("user_id")
	ex, err := s.store.Exchange().GetByID(userID, exchangeID)
	if err != nil || ex == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Exchange account not found"})
		return nil, "", false
	}
	switch ex.ExchangeType {
	case binance.PaperExchangeType:
		t, err := binance.GetPaperTrader(ex.ID)
		if err != nil {
			SafeInternalError(c, "Open paper account", err)
			return nil, "", false
		}
		return t, ex.ExchangeType, true
	case "binance":
		if forTrading {
			return binance.NewFuturesTrader(string(ex.APIKey), string(ex.SecretKey), userID), ex.ExchangeType, true
		}
		t, err := binance.NewAccountTrader(c.Request.Context(), string(ex.APIKey), string(ex.SecretKey), userID)
		if err != nil {
			SafeError(c, http.StatusBadGateway, "Could not connect to Binance. Check the API key, IP allowlist and proxy.", err)
			return nil, "", false
		}
		return t, ex.ExchangeType, true
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "The market page supports Binance and Binance Paper accounts only"})
		return nil, "", false
	}
}

func (s *Server) handleMarketAccount(c *gin.Context) {
	t, exchangeType, ok := s.marketTrader(c, c.Query("exchange_id"), false)
	if !ok {
		return
	}
	balance, err := t.GetBalance()
	if err != nil {
		SafeError(c, http.StatusBadGateway, "Could not read the account balance.", err)
		return
	}
	positions, err := t.GetPositions()
	if err != nil {
		SafeError(c, http.StatusBadGateway, "Could not read positions.", err)
		return
	}
	if positions == nil {
		// An account with nothing open must still send a list, not null.
		positions = []map[string]interface{}{}
	}
	c.JSON(http.StatusOK, gin.H{
		"exchange_type": exchangeType,
		"paper":         exchangeType == binance.PaperExchangeType,
		"balance":       balance,
		"positions":     positions,
	})
}

type marketOrderRequest struct {
	ExchangeID  string  `json:"exchange_id"`
	Symbol      string  `json:"symbol"`
	Side        string  `json:"side"` // long | short
	MarginUSDT  float64 `json:"margin_usdt"`
	Leverage    int     `json:"leverage"`
	StopLoss    float64 `json:"stop_loss"`
	TakeProfit  float64 `json:"take_profit"`
	ConfirmLive bool    `json:"confirm_live"`
}

// requireLiveConfirmation blocks real-money actions unless the client
// explicitly confirmed. Checked before any Binance client is created.
func (s *Server) requireLiveConfirmation(c *gin.Context, exchangeID string, confirmed bool) bool {
	ex, err := s.store.Exchange().GetByID(c.GetString("user_id"), exchangeID)
	if err != nil || ex == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Exchange account not found"})
		return false
	}
	if ex.ExchangeType == "binance" && !confirmed {
		c.JSON(http.StatusBadRequest, gin.H{"error": "This is a live Binance account. Confirm before placing real orders.", "code": "confirm_live_required"})
		return false
	}
	return true
}

func (s *Server) handleMarketOrder(c *gin.Context) {
	var req marketOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	symbol, ok := marketSymbol(c, req.Symbol)
	if !ok {
		return
	}
	if req.Side != "long" && req.Side != "short" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "side must be long or short"})
		return
	}
	if req.Leverage < 1 || req.Leverage > 20 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "leverage must be between 1 and 20"})
		return
	}
	if req.MarginUSDT < 1 || req.MarginUSDT > 1_000_000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "margin must be at least 1 USDT"})
		return
	}
	if !s.requireLiveConfirmation(c, req.ExchangeID, req.ConfirmLive) {
		return
	}
	t, exchangeType, ok := s.marketTrader(c, req.ExchangeID, true)
	if !ok {
		return
	}
	price, err := t.GetMarketPrice(symbol)
	if err != nil || price <= 0 {
		SafeError(c, http.StatusBadGateway, "Could not get the current price.", err)
		return
	}
	// Protective prices must sit on the correct side of the current price.
	long := req.Side == "long"
	if req.StopLoss > 0 && (long && req.StopLoss >= price || !long && req.StopLoss <= price) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("stop-loss %.6g is on the wrong side of the current price %.6g", req.StopLoss, price)})
		return
	}
	if req.TakeProfit > 0 && (long && req.TakeProfit <= price || !long && req.TakeProfit >= price) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("take-profit %.6g is on the wrong side of the current price %.6g", req.TakeProfit, price)})
		return
	}

	quantity := req.MarginUSDT * float64(req.Leverage) / price
	_ = t.SetMarginMode(symbol, true) // cross margin; "no need to change" is fine
	var result map[string]interface{}
	if long {
		result, err = t.OpenLong(symbol, quantity, req.Leverage)
	} else {
		result, err = t.OpenShort(symbol, quantity, req.Leverage)
	}
	if err != nil {
		SafeError(c, http.StatusBadRequest, "Order failed: "+SanitizeError(err, "exchange rejected the order"), err)
		return
	}
	logger.Infof("🧾 Market page order: user=%s exchange=%s(%s) %s %s qty=%.6f lev=%dx",
		c.GetString("user_id"), req.ExchangeID, exchangeType, req.Side, symbol, quantity, req.Leverage)

	positionSide := "LONG"
	if !long {
		positionSide = "SHORT"
	}
	var warnings []string
	if req.StopLoss > 0 {
		if err := t.SetStopLoss(symbol, positionSide, quantity, req.StopLoss); err != nil {
			warnings = append(warnings, "Stop-loss was not set: "+SanitizeError(err, "rejected"))
		}
	}
	if req.TakeProfit > 0 {
		if err := t.SetTakeProfit(symbol, positionSide, quantity, req.TakeProfit); err != nil {
			warnings = append(warnings, "Take-profit was not set: "+SanitizeError(err, "rejected"))
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"result": result, "symbol": symbol, "quantity": quantity, "price": price,
		"paper": exchangeType == binance.PaperExchangeType, "warnings": warnings,
	})
}

func (s *Server) handleMarketClose(c *gin.Context) {
	var req struct {
		ExchangeID  string `json:"exchange_id"`
		Symbol      string `json:"symbol"`
		Side        string `json:"side"`
		ConfirmLive bool   `json:"confirm_live"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	symbol, ok := marketSymbol(c, req.Symbol)
	if !ok {
		return
	}
	side := strings.ToLower(req.Side)
	if side != "long" && side != "short" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "side must be long or short"})
		return
	}
	if !s.requireLiveConfirmation(c, req.ExchangeID, req.ConfirmLive) {
		return
	}
	t, _, ok := s.marketTrader(c, req.ExchangeID, true)
	if !ok {
		return
	}
	var result map[string]interface{}
	var err error
	if side == "long" {
		result, err = t.CloseLong(symbol, 0)
	} else {
		result, err = t.CloseShort(symbol, 0)
	}
	if err != nil {
		SafeError(c, http.StatusBadRequest, "Close failed: "+SanitizeError(err, "exchange rejected the order"), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": result})
}

type marketAnalysis struct {
	Signal     string   `json:"signal"` // long | short | wait
	Confidence float64  `json:"confidence"`
	Entry      float64  `json:"entry"`
	StopLoss   float64  `json:"stop_loss"`
	TakeProfit float64  `json:"take_profit"`
	Leverage   float64  `json:"leverage"`
	Summary    string   `json:"summary"`
	Reasons    []string `json:"reasons"`
	Risks      []string `json:"risks"`
}

// parseMarketAnalysis extracts the JSON object from a model reply that may
// include <think> blocks or ```json fences, and clamps values to sane ranges.
func parseMarketAnalysis(raw string) (*marketAnalysis, error) {
	text := raw
	if i := strings.LastIndex(text, "</think>"); i >= 0 {
		text = text[i+len("</think>"):]
	}
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object in AI reply")
	}
	var a marketAnalysis
	if err := json.Unmarshal([]byte(text[start:end+1]), &a); err != nil {
		return nil, err
	}
	a.Signal = strings.ToLower(strings.TrimSpace(a.Signal))
	if a.Signal != "long" && a.Signal != "short" {
		a.Signal = "wait"
	}
	if a.Confidence < 0 {
		a.Confidence = 0
	} else if a.Confidence > 100 {
		a.Confidence = 100
	}
	if a.Leverage < 1 {
		a.Leverage = 1
	} else if a.Leverage > 20 {
		a.Leverage = 20
	}
	a.Leverage = float64(int(a.Leverage))
	return &a, nil
}

func fmtNum(v float64) string {
	switch {
	case v == 0:
		return "0"
	case v >= 1000 || v <= -1000:
		return strconv.FormatFloat(v, 'f', 2, 64)
	case v >= 1 || v <= -1:
		return strconv.FormatFloat(v, 'f', 4, 64)
	default:
		return strconv.FormatFloat(v, 'g', 6, 64)
	}
}

func buildMarketPrompt(symbol, interval, lang string, t *binance.MarketTicker, k []binance.MarketKline, ind indicatorSnapshot) (string, string) {
	language := "English"
	if lang == "zh" {
		language = "Simplified Chinese"
	}
	system := `You are a disciplined crypto perpetual futures analyst. Analyze only the data provided. ` +
		`Reply with ONE strict JSON object and nothing else:
{"signal":"long|short|wait","confidence":0-100,"entry":number,"stop_loss":number,"take_profit":number,` +
		`"leverage":1-20,"summary":"one sentence","reasons":["..."],"risks":["..."]}
Rules: for long, stop_loss < entry < take_profit; for short, take_profit < entry < stop_loss. ` +
		`Use "wait" with confidence below 50 when the setup is unclear, and set prices to 0 then. ` +
		`Keep reward/risk at least 1.5. Prefer leverage 3 or lower unless the setup is very strong. ` +
		`Write summary, reasons and risks in ` + language + `.`

	var b strings.Builder
	fmt.Fprintf(&b, "Symbol: %s (Binance USDT-M perpetual)\nTimeframe: %s\n", symbol, interval)
	if t != nil {
		fmt.Fprintf(&b, "Last price: %s  Mark: %s  24h change: %.2f%%  24h high/low: %s / %s  24h quote volume: %s USDT  Funding rate: %.4f%%\n",
			fmtNum(t.LastPrice), fmtNum(t.MarkPrice), t.PriceChangePct, fmtNum(t.High24h), fmtNum(t.Low24h), fmtNum(t.QuoteVolume24h), t.FundingRate*100)
	}
	fmt.Fprintf(&b, "\nIndicators (last bar):\nMA7 %s  MA25 %s  MA99 %s\nEMA12 %s  EMA26 %s\nMACD DIF %s  DEA %s  HIST %s\nRSI6 %.2f  RSI14 %.2f\nKDJ K %.2f  D %.2f  J %.2f\nBOLL up %s  mid %s  low %s\nATR14 %s\n",
		fmtNum(ind.MA7), fmtNum(ind.MA25), fmtNum(ind.MA99), fmtNum(ind.EMA12), fmtNum(ind.EMA26),
		fmtNum(ind.MACD), fmtNum(ind.MACDSignal), fmtNum(ind.MACDH), ind.RSI6, ind.RSI14, ind.K, ind.D, ind.J,
		fmtNum(ind.BollUp), fmtNum(ind.BollMid), fmtNum(ind.BollDn), fmtNum(ind.ATR14))

	recent := k
	if len(recent) > 60 {
		recent = recent[len(recent)-60:]
	}
	b.WriteString("\nLast candles (UTC time, open, high, low, close, volume):\n")
	for _, c := range recent {
		fmt.Fprintf(&b, "%s,%s,%s,%s,%s,%s\n", time.Unix(c.Time, 0).UTC().Format("01-02 15:04"),
			fmtNum(c.Open), fmtNum(c.High), fmtNum(c.Low), fmtNum(c.Close), fmtNum(c.Volume))
	}
	return system, b.String()
}

func (s *Server) handleMarketAnalyze(c *gin.Context) {
	var req struct {
		Symbol   string `json:"symbol"`
		Interval string `json:"interval"`
		ModelID  string `json:"model_id"`
		Lang     string `json:"lang"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ModelID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "symbol, interval and model_id are required"})
		return
	}
	symbol, ok := marketSymbol(c, req.Symbol)
	if !ok {
		return
	}
	if !binance.ValidMarketIntervals[req.Interval] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported interval"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	klines, err := binance.GetPublicKlines(ctx, symbol, req.Interval, 200)
	if err != nil || len(klines) < 30 {
		SafeError(c, http.StatusBadGateway, "Could not load enough Binance candles for analysis.", err)
		return
	}
	ticker, _ := binance.GetPublicTicker(ctx, symbol)
	ind := computeIndicators(klines)
	system, user := buildMarketPrompt(symbol, req.Interval, req.Lang, ticker, klines, ind)

	raw, err := s.runRealAITest(c.GetString("user_id"), req.ModelID, system, user)
	if err != nil {
		SafeError(c, http.StatusBadGateway, "AI call failed: "+SanitizeError(err, "check the AI connection settings"), err)
		return
	}
	analysis, parseErr := parseMarketAnalysis(raw)
	analyzedAt := time.Now().Unix()
	resp := gin.H{"symbol": symbol, "interval": req.Interval, "raw": raw, "price": klines[len(klines)-1].Close, "analyzed_at": analyzedAt}
	if parseErr != nil {
		resp["parse_error"] = "The AI reply was not valid JSON; showing the raw text."
	} else {
		resp["analysis"] = analysis
	}
	// Keep the latest analysis per symbol so it survives reloads and other
	// devices. A failed save must not hide a result the user already paid for.
	if data, err := json.Marshal(resp); err == nil {
		if err := s.store.MarketAnalysis().Save(c.GetString("user_id"), symbol, string(data), analyzedAt); err != nil {
			logger.Warnf("⚠️ Failed to save market analysis for %s: %v", symbol, err)
			resp["save_error"] = "The analysis could not be saved and will disappear after a reload."
		}
	}
	c.JSON(http.StatusOK, resp)
}

// handleMarketAnalyses returns the user's saved analyses keyed by symbol.
func (s *Server) handleMarketAnalyses(c *gin.Context) {
	rows, err := s.store.MarketAnalysis().List(c.GetString("user_id"))
	if err != nil {
		SafeInternalError(c, "List saved analyses", err)
		return
	}
	out := make(map[string]json.RawMessage, len(rows))
	for _, r := range rows {
		if json.Valid([]byte(r.Result)) {
			out[r.Symbol] = json.RawMessage(r.Result)
		}
	}
	c.JSON(http.StatusOK, gin.H{"analyses": out})
}

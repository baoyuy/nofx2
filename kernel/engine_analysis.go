package kernel

import (
	"encoding/json"
	"fmt"
	"nofx/logger"
	"nofx/market"
	"nofx/mcp"
	"nofx/store"
	"regexp"
	"strings"
	"time"
)

// ============================================================================
// Pre-compiled regular expressions (performance optimization)
// ============================================================================

var (
	// Safe regex: precisely match ```json code blocks
	reJSONFence      = regexp.MustCompile(`(?is)` + "```json\\s*(\\[\\s*\\{.*?\\}\\s*\\])\\s*```")
	reJSONArray      = regexp.MustCompile(`(?is)\[\s*\{.*?\}\s*\]`)
	reArrayHead      = regexp.MustCompile(`^\[\s*\{`)
	reArrayOpenSpace = regexp.MustCompile(`^\[\s+\{`)
	reInvisibleRunes = regexp.MustCompile("[\u200B\u200C\u200D\uFEFF]")

	// XML tag extraction (supports any characters in reasoning chain)
	reReasoningTag = regexp.MustCompile(`(?s)<reasoning>(.*?)</reasoning>`)
	reDecisionTag  = regexp.MustCompile(`(?s)<decision>(.*?)</decision>`)
)

// ============================================================================
// Entry Functions - Main API
// ============================================================================

// GetFullDecision gets AI's complete trading decision (batch analysis of all coins and positions)
// Uses default strategy configuration - for production use GetFullDecisionWithStrategy with explicit config
func GetFullDecision(ctx *Context, mcpClient mcp.AIClient) (*FullDecision, error) {
	defaultConfig := store.GetDefaultStrategyConfig("en")
	engine := NewStrategyEngine(&defaultConfig)
	return GetFullDecisionWithStrategy(ctx, mcpClient, engine, "")
}

// GetFullDecisionWithStrategy uses StrategyEngine to get AI decision (unified prompt generation)
func GetFullDecisionWithStrategy(ctx *Context, mcpClient mcp.AIClient, engine *StrategyEngine, variant string, traderRules ...TraderPromptRules) (*FullDecision, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is nil")
	}
	if engine == nil {
		defaultConfig := store.GetDefaultStrategyConfig("en")
		engine = NewStrategyEngine(&defaultConfig)
	}

	// Clamp a private config snapshot, never the caller's shared strategy.
	configJSON, err := json.Marshal(engine.GetConfig())
	if err != nil {
		return nil, fmt.Errorf("copy strategy config: %w", err)
	}
	var engineConfig store.StrategyConfig
	if err := json.Unmarshal(configJSON, &engineConfig); err != nil {
		return nil, fmt.Errorf("copy strategy config: %w", err)
	}
	engineConfig.ClampLimits()
	localEngine := *engine
	localEngine.config = &engineConfig
	engine = &localEngine

	// Token estimation check — block if exceeding the specific model's context limit
	estimate := engineConfig.EstimateTokens()
	if len(traderRules) > 0 {
		// Conservative allowance for arbitrary-language trader rules, including
		// the same 15% headroom used by the strategy estimator.
		estimate.Total += (len(traderRules[0].CustomPrompt) + 1) / 2 * 115 / 100
	}

	// Determine context limit for the specific model being used
	contextLimit := 131072 // safe default (strictest common limit)
	var providerName string
	if embedder, ok := mcpClient.(mcp.ClientEmbedder); ok {
		base := embedder.BaseClient()
		providerName = base.Provider
		contextLimit = store.GetContextLimitForClient(base.Provider, base.Model)
	}

	if estimate.Total > contextLimit {
		logger.Errorf("🚫 Token estimate %d exceeds %s context limit %d — blocking analysis",
			estimate.Total, providerName, contextLimit)
		return nil, fmt.Errorf("estimated %d tokens exceeds model context limit of %d; reduce coins, timeframes, or K-line count",
			estimate.Total, contextLimit)
	}
	if estimate.Total*100/contextLimit >= 80 {
		logger.Infof("⚠️  Token estimate %d — approaching %s context limit %d",
			estimate.Total, providerName, contextLimit)
	}

	// 1. Fetch market data using strategy config
	if len(ctx.MarketDataMap) == 0 {
		if err := fetchMarketDataWithStrategy(ctx, engine); err != nil {
			return nil, fmt.Errorf("failed to fetch market data: %w", err)
		}
	}
	alignPositionQuotes(ctx, engine.Exchange())
	pruneCandidateCoinsWithoutMarketData(ctx)
	enrichVergexDataWithStrategy(ctx, engine)
	enrichCopyTradeData(ctx, engine)

	// Ensure OITopDataMap is initialized. OI Top is a paid NofxOS dataset, so it
	// is only fetched when the strategy explicitly enables OI data.
	if ctx.OITopDataMap == nil {
		ctx.OITopDataMap = make(map[string]*OITopData)
	}
	if len(ctx.OITopDataMap) == 0 && engine.wantsOITopData() {
		oiPositions, err := engine.nofxosClient.GetOITopPositions()
		if err == nil {
			for _, pos := range oiPositions {
				ctx.OITopDataMap[pos.Symbol] = &OITopData{
					Rank:              pos.Rank,
					OIDeltaPercent:    pos.OIDeltaPercent,
					OIDeltaValue:      pos.OIDeltaValue,
					PriceDeltaPercent: pos.PriceDeltaPercent,
				}
			}
		}
	}

	// 2. Build System Prompt using strategy engine
	riskConfig := engine.GetRiskControlConfig()
	systemPrompt := engine.BuildSystemPrompt(ctx.Account.TotalEquity, variant, traderRules...)

	// 3. Build User Prompt using strategy engine
	userPrompt := engine.BuildUserPrompt(ctx)

	// 4. Call AI API
	aiCallStart := time.Now()
	aiResponse, err := mcpClient.CallWithMessages(systemPrompt, userPrompt)
	aiCallDuration := time.Since(aiCallStart)
	if err != nil {
		return nil, fmt.Errorf("AI API call failed: %w", err)
	}

	// 5. Parse AI response
	decision, err := parseFullDecisionResponse(
		aiResponse,
		ctx.Account.TotalEquity,
		riskConfig.BTCETHMaxLeverage,
		riskConfig.AltcoinMaxLeverage,
		riskConfig.BTCETHMaxPositionValueRatio,
		riskConfig.AltcoinMaxPositionValueRatio,
		engine.usesVergexSignalPrompt(),
	)

	if decision != nil {
		blockUnsafeOpenings(decision.Decisions, ctx, engine.Exchange(), time.Now())
	}
	if decision != nil {
		decision.Timestamp = time.Now()
		decision.SystemPrompt = systemPrompt
		decision.UserPrompt = userPrompt
		decision.AIRequestDurationMs = aiCallDuration.Milliseconds()
		decision.RawResponse = aiResponse
	}

	if err != nil {
		return decision, fmt.Errorf("failed to parse AI response: %w", err)
	}

	return decision, nil
}

func enrichVergexDataWithStrategy(ctx *Context, engine *StrategyEngine) {
	if ctx == nil || engine == nil || ctx.VergexDataMap != nil {
		return
	}
	if engine.GetConfig().CoinSource.SourceType != "vergex_signal" {
		return
	}
	symbolSet := make(map[string]bool)
	symbols := make([]string, 0, len(ctx.CandidateCoins)+len(ctx.Positions))
	for _, coin := range ctx.CandidateCoins {
		if !symbolSet[coin.Symbol] {
			symbolSet[coin.Symbol] = true
			symbols = append(symbols, coin.Symbol)
		}
	}
	for _, pos := range ctx.Positions {
		if !symbolSet[pos.Symbol] {
			symbolSet[pos.Symbol] = true
			symbols = append(symbols, pos.Symbol)
		}
	}
	ctx.VergexDataMap = engine.FetchVergexDataBatch(nil, symbols)
}

// ============================================================================
// Market Data Fetching
// ============================================================================

// fetchMarketDataWithStrategy fetches market data using strategy config (multiple timeframes)
func fetchMarketDataWithStrategy(ctx *Context, engine *StrategyEngine) error {
	config := engine.GetConfig()
	ctx.MarketDataMap = make(map[string]*market.Data)

	timeframes := config.Indicators.Klines.SelectedTimeframes
	primaryTimeframe := config.Indicators.Klines.PrimaryTimeframe
	klineCount := config.Indicators.Klines.PrimaryCount

	// Compatible with old configuration
	if len(timeframes) == 0 {
		if primaryTimeframe != "" {
			timeframes = append(timeframes, primaryTimeframe)
		} else {
			timeframes = append(timeframes, "3m")
		}
		if config.Indicators.Klines.LongerTimeframe != "" {
			timeframes = append(timeframes, config.Indicators.Klines.LongerTimeframe)
		}
	}
	if primaryTimeframe == "" {
		primaryTimeframe = timeframes[0]
	}
	if klineCount <= 0 {
		klineCount = 30
	}

	logger.Infof("📊 Strategy timeframes: %v, Primary: %s, Kline count: %d", timeframes, primaryTimeframe, klineCount)

	// 1. First fetch data for position coins (must fetch)
	for _, pos := range ctx.Positions {
		data, err := fetchStrategyCandles(pos.Symbol, engine.Exchange(), timeframes, primaryTimeframe, klineCount)
		if err != nil {
			logger.Infof("⚠️  Failed to fetch market data for position %s: %v", pos.Symbol, err)
			continue
		}
		ctx.MarketDataMap[pos.Symbol] = data
	}
	alignPositionQuotes(ctx, engine.Exchange())

	// 2. Fetch data for all candidate coins
	positionSymbols := make(map[string]bool)
	for _, pos := range ctx.Positions {
		positionSymbols[pos.Symbol] = true
	}

	for _, coin := range ctx.CandidateCoins {
		if _, exists := ctx.MarketDataMap[coin.Symbol]; exists {
			continue
		}

		data, err := fetchStrategyCandles(coin.Symbol, engine.Exchange(), timeframes, primaryTimeframe, klineCount)
		if err != nil {
			logger.Infof("⚠️  Failed to fetch market data for %s: %v", coin.Symbol, err)
			continue
		}

		if skipLowOpenInterest(coin.Symbol, data, positionSymbols[coin.Symbol], config.RiskControl.MinOIValueMillionsOrDefault()) {
			continue
		}

		if !positionSymbols[coin.Symbol] {
			quote, quoteErr := fetchStrategyQuote(coin.Symbol, engine.Exchange())
			if quoteErr != nil {
				logger.Infof("⚠️ Reference quote unavailable for %s: %v (opening disabled)", coin.Symbol, quoteErr)
			}
			data.SetQuote(quote)
		}
		ctx.MarketDataMap[coin.Symbol] = data
	}

	logger.Infof("📊 Successfully fetched multi-timeframe market data for %d coins", len(ctx.MarketDataMap))
	return nil
}

// Missing OI must not empty the candidate list; a measured zero remains low OI.
// Existing positions and xyz assets retain their existing exemptions. The
// threshold comes from the strategy (risk_control.min_oi_value_millions);
// 0 disables the filter.
func skipLowOpenInterest(symbol string, data *market.Data, existingPosition bool, minOIThresholdMillions float64) bool {
	if existingPosition || market.IsXyzDexAsset(symbol) || minOIThresholdMillions <= 0 {
		return false
	}
	if data.OpenInterest == nil {
		logger.Infof("%s open interest unavailable; OI liquidity filter not evaluated", symbol)
		return false
	}
	price := data.CurrentPrice
	if price <= 0 {
		price = data.IndicatorPrice
	} // valuation estimate only, not a live quote
	if price > 0 {
		oiValueInMillions := data.OpenInterest.Latest * price / 1_000_000
		if oiValueInMillions < minOIThresholdMillions {
			logger.Infof("%s OI value too low (%.2fM USD < %.1fM), skipping coin",
				symbol, oiValueInMillions, minOIThresholdMillions)
			return true
		}
	}
	return false
}

func pruneCandidateCoinsWithoutMarketData(ctx *Context) {
	if ctx == nil || len(ctx.CandidateCoins) == 0 || len(ctx.MarketDataMap) == 0 {
		return
	}
	kept := make([]CandidateCoin, 0, len(ctx.CandidateCoins))
	for _, coin := range ctx.CandidateCoins {
		if _, ok := ctx.MarketDataMap[coin.Symbol]; ok {
			kept = append(kept, coin)
			continue
		}
		logger.Infof("⚠️  Skipping candidate %s in AI prompt: no valid market/K-line data", coin.Symbol)
	}
	ctx.CandidateCoins = kept
}

var fetchStrategyCandles = market.GetWithTimeframesFor
var fetchStrategyQuote = market.GetReferenceQuote

// alignPositionQuotes shares the account snapshot's price/PnL basis with the
// market section. Never refetch/replace a held symbol with a different quote.
// Shallow-copy Data so shared candle/indicator snapshots remain unchanged.
func alignPositionQuotes(ctx *Context, exchange string) {
	if ctx.MarketDataMap == nil {
		ctx.MarketDataMap = make(map[string]*market.Data)
	}
	for _, pos := range ctx.Positions {
		key := pos.Symbol
		data := ctx.MarketDataMap[key]
		if data == nil {
			data = ctx.MarketDataMap[market.NormalizeFor(key, exchange)]
		}
		if data == nil {
			data = &market.Data{Symbol: key, IndicatorIssue: "candles unavailable"}
		}
		copyData := *data
		quote := &market.Quote{Price: pos.MarkPrice, Source: "account_position_mark (same snapshot as PnL; not a fill guarantee)"}
		if pos.MarkObservedAt > 0 {
			quote.ObservedAt = time.UnixMilli(pos.MarkObservedAt)
		}
		if pos.MarkPriceTime > 0 {
			quote.AsOf = time.UnixMilli(pos.MarkPriceTime)
		}
		copyData.SetQuote(quote)
		ctx.MarketDataMap[key] = &copyData
		if normalized := market.NormalizeFor(key, exchange); normalized != key {
			ctx.MarketDataMap[normalized] = &copyData
		}
	}
}

// Independent of prompt compliance: stale/unknown quote or indicator data may
// never create exposure. Hold/close actions are intentionally untouched.
func blockUnsafeOpenings(decisions []Decision, ctx *Context, exchange string, now time.Time) {
	for i := range decisions {
		d := &decisions[i]
		if d.Action != "open_long" && d.Action != "open_short" {
			continue
		}
		data := ctx.MarketDataMap[d.Symbol]
		if data == nil {
			data = ctx.MarketDataMap[market.NormalizeFor(d.Symbol, exchange)]
		}
		if !data.OpeningAllowed(now) {
			logger.Infof("🚫 Opening %s blocked: stale/unavailable quote or indicator timestamps", d.Symbol)
			d.Action = "wait"
			d.Reasoning = "Opening blocked: stale/unavailable quote or indicator timestamps. " + d.Reasoning
		}
	}
}

// ============================================================================
// AI Response Parsing
// ============================================================================

func parseFullDecisionResponse(aiResponse string, accountEquity float64, btcEthLeverage, altcoinLeverage int, btcEthPosRatio, altcoinPosRatio float64, signalManagedExit bool) (*FullDecision, error) {
	cotTrace := extractCoTTrace(aiResponse)

	decisions, err := extractDecisions(aiResponse)
	if err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: []Decision{},
		}, fmt.Errorf("failed to extract decisions: %w", err)
	}

	if err := validateDecisions(decisions, accountEquity, btcEthLeverage, altcoinLeverage, btcEthPosRatio, altcoinPosRatio, signalManagedExit); err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: decisions,
		}, fmt.Errorf("decision validation failed: %w", err)
	}

	return &FullDecision{
		CoTTrace:  cotTrace,
		Decisions: decisions,
	}, nil
}

func extractCoTTrace(response string) string {
	if match := reReasoningTag.FindStringSubmatch(response); match != nil && len(match) > 1 {
		logger.Infof("✓ Extracted reasoning chain using <reasoning> tag")
		return strings.TrimSpace(match[1])
	}

	if decisionIdx := strings.Index(response, "<decision>"); decisionIdx > 0 {
		logger.Infof("✓ Extracted content before <decision> tag as reasoning chain")
		return strings.TrimSpace(response[:decisionIdx])
	}

	jsonStart := strings.Index(response, "[")
	if jsonStart > 0 {
		logger.Infof("⚠️  Extracted reasoning chain using old format ([ character separator)")
		return strings.TrimSpace(response[:jsonStart])
	}

	return strings.TrimSpace(response)
}

func extractDecisions(response string) ([]Decision, error) {
	s := removeInvisibleRunes(response)
	s = strings.TrimSpace(s)
	s = fixMissingQuotes(s)

	var jsonPart string
	if match := reDecisionTag.FindStringSubmatch(s); match != nil && len(match) > 1 {
		jsonPart = strings.TrimSpace(match[1])
		logger.Infof("✓ Extracted JSON using <decision> tag")
	} else {
		jsonPart = s
		logger.Infof("⚠️  <decision> tag not found, searching JSON in full text")
	}

	jsonPart = fixMissingQuotes(jsonPart)

	if m := reJSONFence.FindStringSubmatch(jsonPart); m != nil && len(m) > 1 {
		jsonContent := strings.TrimSpace(m[1])
		jsonContent = compactArrayOpen(jsonContent)
		jsonContent = fixMissingQuotes(jsonContent)
		if err := validateJSONFormat(jsonContent); err != nil {
			return nil, fmt.Errorf("JSON format validation failed: %w\nJSON content: %s\nFull response:\n%s", err, jsonContent, response)
		}
		var decisions []Decision
		if err := json.Unmarshal([]byte(jsonContent), &decisions); err != nil {
			return nil, fmt.Errorf("JSON parsing failed: %w\nJSON content: %s", err, jsonContent)
		}
		return decisions, nil
	}

	jsonContent := strings.TrimSpace(reJSONArray.FindString(jsonPart))
	if jsonContent == "" {
		logger.Infof("⚠️  [SafeFallback] AI didn't output JSON decision, entering safe wait mode")

		cotSummary := jsonPart
		if len(cotSummary) > 240 {
			cotSummary = cotSummary[:240] + "..."
		}

		fallbackDecision := Decision{
			Symbol:    "ALL",
			Action:    "wait",
			Reasoning: fmt.Sprintf("Model didn't output structured JSON decision, entering safe wait; summary: %s", cotSummary),
		}

		return []Decision{fallbackDecision}, nil
	}

	jsonContent = compactArrayOpen(jsonContent)
	jsonContent = fixMissingQuotes(jsonContent)

	if err := validateJSONFormat(jsonContent); err != nil {
		return nil, fmt.Errorf("JSON format validation failed: %w\nJSON content: %s\nFull response:\n%s", err, jsonContent, response)
	}

	var decisions []Decision
	if err := json.Unmarshal([]byte(jsonContent), &decisions); err != nil {
		return nil, fmt.Errorf("JSON parsing failed: %w\nJSON content: %s", err, jsonContent)
	}

	return decisions, nil
}

func fixMissingQuotes(jsonStr string) string {
	jsonStr = strings.ReplaceAll(jsonStr, "\u201c", "\"")
	jsonStr = strings.ReplaceAll(jsonStr, "\u201d", "\"")
	jsonStr = strings.ReplaceAll(jsonStr, "\u2018", "'")
	jsonStr = strings.ReplaceAll(jsonStr, "\u2019", "'")

	jsonStr = strings.ReplaceAll(jsonStr, "［", "[")
	jsonStr = strings.ReplaceAll(jsonStr, "］", "]")
	jsonStr = strings.ReplaceAll(jsonStr, "｛", "{")
	jsonStr = strings.ReplaceAll(jsonStr, "｝", "}")
	jsonStr = strings.ReplaceAll(jsonStr, "：", ":")
	jsonStr = strings.ReplaceAll(jsonStr, "，", ",")

	jsonStr = strings.ReplaceAll(jsonStr, "【", "[")
	jsonStr = strings.ReplaceAll(jsonStr, "】", "]")
	jsonStr = strings.ReplaceAll(jsonStr, "〔", "[")
	jsonStr = strings.ReplaceAll(jsonStr, "〕", "]")
	jsonStr = strings.ReplaceAll(jsonStr, "、", ",")

	jsonStr = strings.ReplaceAll(jsonStr, "　", " ")

	return jsonStr
}

func validateJSONFormat(jsonStr string) error {
	trimmed := strings.TrimSpace(jsonStr)

	if !reArrayHead.MatchString(trimmed) {
		if strings.HasPrefix(trimmed, "[") && !strings.Contains(trimmed[:min(20, len(trimmed))], "{") {
			return fmt.Errorf("not a valid decision array (must contain objects {}), actual content: %s", trimmed[:min(50, len(trimmed))])
		}
		return fmt.Errorf("JSON must start with [{ (whitespace allowed), actual: %s", trimmed[:min(20, len(trimmed))])
	}

	if strings.Contains(jsonStr, "~") {
		return fmt.Errorf("JSON cannot contain range symbol ~, all numbers must be precise single values")
	}

	for i := 0; i < len(jsonStr)-4; i++ {
		if jsonStr[i] >= '0' && jsonStr[i] <= '9' &&
			jsonStr[i+1] == ',' &&
			jsonStr[i+2] >= '0' && jsonStr[i+2] <= '9' &&
			jsonStr[i+3] >= '0' && jsonStr[i+3] <= '9' &&
			jsonStr[i+4] >= '0' && jsonStr[i+4] <= '9' {
			return fmt.Errorf("JSON numbers cannot contain thousand separator comma, found: %s", jsonStr[i:min(i+10, len(jsonStr))])
		}
	}

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func removeInvisibleRunes(s string) string {
	return reInvisibleRunes.ReplaceAllString(s, "")
}

func compactArrayOpen(s string) string {
	return reArrayOpenSpace.ReplaceAllString(strings.TrimSpace(s), "[{")
}

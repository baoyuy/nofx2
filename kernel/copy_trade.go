package kernel

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"nofx/logger"
	"nofx/market"
	"nofx/store"
)

// CopyTradeSide is one side of a coin among followed Binance lead traders.
type CopyTradeSide struct {
	Traders  int
	Value    float64 // USDT at entry
	AvgEntry float64
	Trapped  int // deep in loss
	Fresh    int // opened or added within 24h
	HighFreq int // bots / grid traders, weighted down
	// Openers: fresh holders whose call counts (not trapped, not high
	// frequency, not averaging down).
	Openers int
}

// CopyTradeHitRate is how often past signals of one confidence band and
// direction moved the called way 24h later.
type CopyTradeHitRate struct {
	Band      string // high | mid | low
	Direction string // long | short
	Samples   int
	Rate      float64 // 0-1
}

// copyTradeBand is the hit-rate band of a confidence (copytrade.ConfidenceBand).
func copyTradeBand(confidence float64) string {
	switch {
	case confidence >= 70:
		return "high"
	case confidence >= 40:
		return "mid"
	default:
		return "low"
	}
}

// CopyTradeAction is one recent open, add, reduce or close.
type CopyTradeAction struct {
	Time     int64 // ms
	Trader   string
	Score    float64 // trader credibility 0-100
	Side     string  // long | short
	Action   string  // open | add | reduce | close
	Value    float64
	Price    float64
	Realized float64
}

// CopyTradeHolder is one followed lead trader's position in a coin.
type CopyTradeHolder struct {
	Trader                 string
	Score                  float64 // credibility 0-100
	Side                   string  // long | short
	Entry                  float64
	Value                  float64 // USDT at entry
	Leverage               float64 // 0 = unknown (position inferred from trades)
	ReturnPct              float64 // unrealized return on the explicitly supplied basis
	PctOfMargin            bool
	Priced                 bool
	Source                 string
	HistoricalEntryWarning bool
	FirstEntry             float64
	FirstEntryReturnPct    float64
	FirstEntryPctOfMargin  bool
	FirstEntryThresholdPct float64
	PositionsUpdatedAt     time.Time
	FillsUpdatedAt         time.Time
	ProfileUpdatedAt       time.Time
	OpenedAt               int64 // ms; 0 = before monitoring
	Trapped                bool
	Fresh                  bool
	HighFreq               bool
	// Style is how they trade: scalp, swing, long or martingale. Empty when
	// there are too few trades to tell.
	Style string
	// Calls and Accuracy: how many of their own opens and adds were scored,
	// and the share that moved their way within 24h (0-1).
	Calls    int
	Accuracy float64
}

// CopyTradeCoin is what followed lead traders do on one coin.
type CopyTradeCoin struct {
	Symbol     string
	Direction  string  // long | short | neutral
	Confidence float64 // 0-100
	Long       CopyTradeSide
	Short      CopyTradeSide
	Liquidated int
	// Recent actions, newest first, high-frequency traders left out.
	Recent []CopyTradeAction
	// Holders, most credible first, at most a few.
	Holders []CopyTradeHolder
}

// CopyTradeView is what followed lead traders currently do, with how much of
// the picture is fresh. A rate-limited or partly stale monitor must not read
// as "nobody holds this coin".
type CopyTradeView struct {
	Coins []CopyTradeCoin
	// Fresh is how many followed traders have data newer than the freshness
	// window, Total how many can have any (public portfolios).
	Fresh int
	Total int
	// Component coverage is available for monitor-backed views, not test mocks.
	QualityKnown   bool
	PositionsFresh int
	FillsFresh     int
	ProfilesFresh  int
	// Reason explains missing data, e.g. "Binance is rate limiting requests".
	Reason string
	// HitRates are the measured 24h hit rates per band and direction.
	HitRates []CopyTradeHitRate
}

// hitRate is the measured record of signals like a coin's current one.
func (v *CopyTradeView) hitRate(confidence float64, direction string) (CopyTradeHitRate, bool) {
	if v == nil {
		return CopyTradeHitRate{}, false
	}
	band := copyTradeBand(confidence)
	for _, r := range v.HitRates {
		if r.Band == band && r.Direction == direction {
			return r, true
		}
	}
	return CopyTradeHitRate{Band: band, Direction: direction}, false
}

// complete tells whether every trader that can have data is fresh: only then
// does a missing coin mean the traders truly do not hold it.
func (v *CopyTradeView) complete() bool { return v == nil || (v.Total > 0 && v.Fresh >= v.Total) }

// usable tells whether any fresh data arrived at all.
func (v *CopyTradeView) usable() bool { return v == nil || v.Fresh > 0 }

// note warns that the coin data below is incomplete, or "" when it is whole.
func (v *CopyTradeView) note() string {
	if v.complete() {
		return ""
	}
	reason := v.Reason
	if reason == "" {
		reason = "data is stale"
	}
	if !v.usable() {
		return fmt.Sprintf("⚠️ Lead traders: copy-trade data is unavailable right now (%s); nothing below tells whether they hold or left a coin.\n", reason)
	}
	return fmt.Sprintf("⚠️ Lead traders: only %d of %d followed traders have fresh data (%s); the counts below are incomplete.\n", v.Fresh, v.Total, reason)
}

// CopyTradeResolver returns the followed lead traders' view. It is set at
// startup by the copy-trade monitor; nil means the monitor is not running.
var CopyTradeResolver func() CopyTradeView

// copyTradeCandidates picks the coins of the copy_trade source: the leading
// side matches the direction setting, enough traders, confidence high
// enough, highest confidence first.
// Symbols are Binance's; GetCandidateCoins puts them in the trader's
// exchange form.
func copyTradeCandidates(source store.CoinSourceConfig) []CandidateCoin {
	source.NormalizeCopyTrade()
	if CopyTradeResolver == nil {
		logger.Warnf("⚠️  Copy-trade monitor not running: no copy-trade coins")
		return nil
	}
	view := CopyTradeResolver()
	if !view.usable() {
		logger.Warnf("⚠️  Copy-trade data unavailable: %s", view.Reason)
		return nil
	}
	coins := view.Coins
	picked := make([]CopyTradeCoin, 0, len(coins))
	for _, c := range coins {
		if c.Direction == "neutral" || c.Confidence < source.CopyTradeMinConfidence {
			continue
		}
		if source.CopyTradeDirection != "both" && c.Direction != source.CopyTradeDirection {
			continue
		}
		if c.Long.Traders+c.Short.Traders < source.CopyTradeMinTraders {
			continue
		}
		// Holding is not a call: enough of them must have opened lately.
		if source.CopyTradeMinOpeners > 0 && c.leading().Openers < source.CopyTradeMinOpeners {
			continue
		}
		picked = append(picked, c)
	}
	sort.SliceStable(picked, func(a, b int) bool { return picked[a].Confidence > picked[b].Confidence })
	if len(picked) > source.CopyTradeLimit {
		picked = picked[:source.CopyTradeLimit]
	}
	out := make([]CandidateCoin, 0, len(picked))
	for _, c := range picked {
		out = append(out, CandidateCoin{Symbol: copyTradeKey(c.Symbol), Sources: []string{"copy_trade"}})
	}
	logger.Infof("👥 Copy-trade source: %d of %d followed coins pass (min confidence %.0f, %s, %d+ traders, %d+ fresh openers)",
		len(out), len(coins), source.CopyTradeMinConfidence, source.CopyTradeDirection, source.CopyTradeMinTraders, source.CopyTradeMinOpeners)
	return out
}

// leading is the side the traders lean to (long when neutral).
func (c *CopyTradeCoin) leading() CopyTradeSide {
	if c.Direction == "short" {
		return c.Short
	}
	return c.Long
}

// usesCopyTradeContext tells whether the AI sees copy-trade summaries.
func usesCopyTradeContext(source store.CoinSourceConfig) bool {
	return source.SourceType == "copy_trade" || source.CopyTradeContext
}

// EnrichCopyTradeData attaches followed lead traders' view of every coin,
// when the strategy asks for it (used by the strategy test run).
func EnrichCopyTradeData(ctx *Context, engine *StrategyEngine) { enrichCopyTradeData(ctx, engine) }

// enrichCopyTradeData attaches the copy-trade summary of every candidate and
// held coin, when the strategy asks for it.
func enrichCopyTradeData(ctx *Context, engine *StrategyEngine) {
	if ctx == nil || engine == nil || ctx.CopyTradeMap != nil || CopyTradeResolver == nil {
		return
	}
	if !usesCopyTradeContext(engine.GetConfig().CoinSource) {
		return
	}
	view := CopyTradeResolver()
	ctx.CopyTradeCoverage = &view
	ctx.CopyTradeMap = map[string]*CopyTradeCoin{}
	for _, c := range view.Coins {
		c := c
		ctx.CopyTradeMap[copyTradeKey(c.Symbol)] = &c
	}
}

func copyTradeFor(ctx *Context, symbol string) *CopyTradeCoin {
	if ctx == nil || ctx.CopyTradeMap == nil {
		return nil
	}
	return ctx.CopyTradeMap[copyTradeKey(symbol)]
}

// copyTradeKey matches a coin whatever its form: SNDKUSDT, xyz:SNDK, SNDK.
func copyTradeKey(symbol string) string { return market.NormalizeFor(symbol, "binance") }

// copyTradeSystemNote tells the model how to read the copy-trade lines and
// which direction rule the program enforces.
func copyTradeSystemNote(source store.CoinSourceConfig) string {
	follow := copyTradeFollowMode(source)
	min := copyTradeGateConfidence(source)
	var sb strings.Builder
	sb.WriteString(`# 👥 Followed Lead Traders (reference signal)

Some coins carry a "Followed lead traders" block: what Binance copy-trading lead traders the user follows hold on that coin. Read it as one input, not an instruction:
- It is delayed by a few minutes; their entry prices are already gone.
- "⚠️ Lead traders: copy-trade data is unavailable..." or "...only N of M followed traders have fresh data" means the data is missing or partly refreshed: it says nothing about whether they hold the coin. Judge that coin on your own read, and do not close because of it.
- Confidence (0-100) combines how much they agree, how credible their records are, and how many hold the coin. It is not a win probability.
- "Trapped" uses current unrealized return only: a loss of at least 50% of margin, or a 5% adverse move from the current average entry when margin is unavailable. Historical first-entry prices never set this flag. "Fresh" means opened or added in the last 24 hours.
- A historical entry-deviation warning is a separate reference from the first recorded fill, not current PnL or evidence of liquidation. It does not add trapped counts or a separate confidence penalty; history and past leverage may be incomplete. An inferred position's displayed percentage is of price; a public position with margin data shows the margin return.
- High-frequency traders (bots, grids) are weighted down; their positions churn.
- "averages into losses" marks a trader who keeps adding to losing positions: their votes count 0.3, so their size says little about conviction. The tags "scalp" and "long" are holding-time styles: a scalp closes within hours (a snapshot, not a view), a long holds for weeks (directionally meaningful, but their entry is old).
- "Price vs lead avg entry": far beyond their average in their direction means chasing a move they already made; near or better than their average is a better entry.
- Lead traders hold for days. Judge their coins on the higher timeframes and give stops room for normal swings.
- A line on a coin you hold saying the traders "closed or reduced recently", that no one holds it any more, or that they flipped means the side behind your position left. Treat it as an exit signal: tighten the stop or close unless your own read is clearly still valid.
- Agreement with your own technical read can raise conviction; disagreement or mostly trapped holders should lower it. Never open a trade only because lead traders hold it.
- "Fresh calls" counts traders who opened or added on that side in the last 24h and are not trapped, high-frequency or averaging down: a lean with no fresh calls is old positions, not a current view.
- "Track record" is how often past signals of the same confidence band and direction moved the called way 24h later. Near or under 50% means no edge: weigh the lean little. "own calls right X% of N" on a holder is that trader's own 24h record.
`)
	switch follow {
	case "follow":
		sb.WriteString(fmt.Sprintf("- RULE (enforced by the program): open only in the lead traders' leaning direction on each coin, and only when their confidence is at least %.0f; opens against it, or on coins they do not lean on that clearly, are rejected. Closing is always allowed.\n", min))
	case "fade":
		sb.WriteString(fmt.Sprintf("- RULE (enforced by the program): open only AGAINST the lead traders' leaning direction on each coin, and only when their confidence is at least %.0f; opens with it, or on coins they do not lean on that clearly, are rejected. Closing is always allowed.\n", min))
	}
	if follow == "follow" || follow == "fade" {
		source.NormalizeCopyTrade()
		if n := source.CopyTradeMinOpeners; n > 0 {
			sb.WriteString(fmt.Sprintf("- RULE (enforced): the leading side needs at least %d fresh calls.\n", n))
		}
		if r := source.CopyTradeMinHitRate; r > 0 {
			sb.WriteString(fmt.Sprintf("- RULE (enforced): the track record of the coin's band and direction must be at least %.0f%% over %d+ signals (for fade: wrong at least that often).\n", r, source.CopyTradeMinSamples))
		}
		if source.CopyTradeTrendFilter {
			sb.WriteString("- RULE (enforced): no long while the 4h EMA20 is below EMA50, no short while it is above.\n")
		}
	}
	if v := source.CopyTradeMinVolumeM; v > 0 {
		sb.WriteString(fmt.Sprintf("- RULE (enforced): no open on a coin that traded under %.0fM USDT in 24h.\n", v))
	}
	sb.WriteString("\n")
	return sb.String()
}

// formatCopyTrade writes one coin's copy-trade block for the user prompt.
// price is the current price (0 = unknown); held is the side the account
// holds ("" = none), for exit alerts. cov tells how much of the data is
// fresh: with stale data a missing coin says nothing about the traders.
func formatCopyTrade(c *CopyTradeCoin, cov *CopyTradeView, now time.Time, price float64, held string) string {
	if c == nil {
		if (cov != nil && cov.Total == 0) || (held == "" && cov.complete()) {
			// No follows, or complete data for an unheld/unfollowed coin.
			return ""
		}
		switch {
		case !cov.usable():
			reason := "data is stale"
			if cov != nil && cov.Reason != "" {
				reason = cov.Reason
			}
			return fmt.Sprintf("⚠️ Lead traders: copy-trade data is unavailable right now (%s). That is not a signal that they left this coin.\n", reason)
		case !cov.complete():
			return fmt.Sprintf("⚠️ Lead traders: no data on this coin from the %d of %d followed traders that refreshed; that is not evidence of an exit.\n", cov.Fresh, cov.Total)
		}
		return "⚠️ Lead traders: none of the followed lead traders holds or recently traded this coin.\n"
	}
	var sb strings.Builder
	// New entries need the same completeness warning as held positions.
	sb.WriteString(cov.note())
	if c.Long.Traders+c.Short.Traders == 0 {
		sb.WriteString("Followed lead traders: none hold it now\n")
	} else {
		sb.WriteString(fmt.Sprintf("Followed lead traders: leaning %s, confidence %.0f/100\n", strings.ToUpper(c.Direction), c.Confidence))
	}
	if line := copyTradeVsAvg(c, price); line != "" {
		sb.WriteString(line)
	}
	if line := copyTradeTrackRecord(c, cov); line != "" {
		sb.WriteString(line)
	}
	if cov.complete() {
		if line := copyTradeExitAlert(c, held); line != "" {
			sb.WriteString(line)
		}
	}
	side := func(name string, s CopyTradeSide) {
		if s.Traders == 0 {
			sb.WriteString(fmt.Sprintf("- %s: none\n", name))
			return
		}
		sb.WriteString(fmt.Sprintf("- %s: %d traders, %.0f USDT, avg entry %s", name, s.Traders, s.Value, formatCopyPrice(s.AvgEntry)))
		var notes []string
		if s.Trapped > 0 {
			notes = append(notes, fmt.Sprintf("%d trapped", s.Trapped))
		}
		if s.Fresh > 0 {
			notes = append(notes, fmt.Sprintf("%d fresh", s.Fresh))
		}
		if s.Openers > 0 {
			notes = append(notes, fmt.Sprintf("%d fresh calls", s.Openers))
		}
		if s.HighFreq > 0 {
			notes = append(notes, fmt.Sprintf("%d high-frequency", s.HighFreq))
		}
		if len(notes) > 0 {
			sb.WriteString(" (" + strings.Join(notes, ", ") + ")")
		}
		sb.WriteString("\n")
	}
	side("Long", c.Long)
	side("Short", c.Short)
	if c.Liquidated > 0 {
		sb.WriteString(fmt.Sprintf("- %d more likely liquidated (not counted)\n", c.Liquidated))
	}
	if len(c.Holders) > 0 {
		sb.WriteString("- Holders (most credible first):\n")
		for _, h := range c.Holders {
			line := fmt.Sprintf("  - %s (score %.0f) %s %.0f USDT @ %s", h.Trader, h.Score, h.Side, h.Value, formatCopyPrice(h.Entry))
			if h.Leverage > 0 {
				line += fmt.Sprintf(", %.0fx", h.Leverage)
			}
			if h.Priced {
				if h.PctOfMargin {
					line += fmt.Sprintf(", %+.1f%% of margin", h.ReturnPct)
				} else {
					line += fmt.Sprintf(", %+.1f%% price (margin unavailable)", h.ReturnPct)
				}
			}
			if h.OpenedAt > 0 {
				line += ", held " + formatCopyAgo(now.Sub(time.UnixMilli(h.OpenedAt)).Round(time.Minute))
			} else {
				line += ", held since before monitoring"
			}
			if h.Calls >= 10 {
				line += fmt.Sprintf(", own calls right %.0f%% of %d", h.Accuracy*100, h.Calls)
			}
			var tags []string
			if h.Trapped {
				tags = append(tags, "trapped")
			}
			if h.Fresh {
				tags = append(tags, "fresh")
			}
			if h.HighFreq {
				tags = append(tags, "high-frequency")
			}
			switch h.Style {
			case "martingale":
				// Their size is a symptom, not conviction: votes count 0.3.
				tags = append(tags, "averages into losses")
			case "scalp", "long":
				tags = append(tags, h.Style)
			}
			if len(tags) > 0 {
				line += " [" + strings.Join(tags, ", ") + "]"
			}
			if h.Source != "" {
				line += " (" + h.Source + " position)"
			}
			sb.WriteString(line + "\n")
			if !h.PositionsUpdatedAt.IsZero() || !h.FillsUpdatedAt.IsZero() || !h.ProfileUpdatedAt.IsZero() {
				sb.WriteString(fmt.Sprintf("    Data timestamps (UTC): positions %s; fills %s; profile %s\n",
					copyTradeDataTime(h.PositionsUpdatedAt), copyTradeDataTime(h.FillsUpdatedAt), copyTradeDataTime(h.ProfileUpdatedAt)))
			}
			if h.HistoricalEntryWarning {
				basis := "price"
				if h.FirstEntryPctOfMargin {
					basis = "reference scaled by current leverage"
				}
				sb.WriteString(fmt.Sprintf("    Historical entry deviation: first recorded entry %s, %+.1f%% (%s), adverse threshold %.1f%%. Not current PnL; not counted as trapped or a separate confidence penalty. History/past leverage may be incomplete.\n",
					formatCopyPrice(h.FirstEntry), h.FirstEntryReturnPct, basis, h.FirstEntryThresholdPct))
			}
		}
	}
	if len(c.Recent) > 0 {
		sb.WriteString("- Recent actions:\n")
		for _, a := range c.Recent {
			ago := now.Sub(time.UnixMilli(a.Time)).Round(time.Minute)
			line := fmt.Sprintf("  - %s ago: %s (score %.0f) %s %s %.0f USDT @ %s", formatCopyAgo(ago), a.Trader, a.Score, a.Action, a.Side, a.Value, formatCopyPrice(a.Price))
			if a.Realized != 0 {
				line += fmt.Sprintf(", realized %+.0f", a.Realized)
			}
			sb.WriteString(line + "\n")
		}
	}
	return sb.String()
}

// copyTradeTrackRecord is the measured 24h record of signals like this one.
func copyTradeTrackRecord(c *CopyTradeCoin, cov *CopyTradeView) string {
	if c.Direction != "long" && c.Direction != "short" {
		return ""
	}
	hr, ok := cov.hitRate(c.Confidence, c.Direction)
	if !ok || hr.Samples == 0 {
		return fmt.Sprintf("- Track record: no scored %s-confidence %s signals yet\n", copyTradeBand(c.Confidence), c.Direction)
	}
	note := ""
	if hr.Samples < 30 {
		note = ", too few to rely on"
	}
	return fmt.Sprintf("- Track record: past %s-confidence %s signals moved the called way %.0f%% of %d times after 24h%s\n",
		hr.Band, c.Direction, hr.Rate*100, hr.Samples, note)
}

func formatCopyPrice(p float64) string {
	switch {
	case p <= 0:
		return "-"
	case p >= 100:
		return fmt.Sprintf("%.2f", p)
	case p >= 1:
		return fmt.Sprintf("%.4f", p)
	default:
		return fmt.Sprintf("%.6g", p)
	}
}

func formatCopyAgo(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%.1fh", d.Hours())
}

// CopyTradeBriefs summarizes, for the decision record, what followed lead
// traders did on each coin the AI was shown, with the same text it read.
func CopyTradeBriefs(ctx *Context, now time.Time) []store.DecisionCopyTrade {
	if ctx == nil || ctx.CopyTradeMap == nil {
		return nil
	}
	var out []store.DecisionCopyTrade
	seen := map[string]bool{}
	add := func(symbol string) {
		c := copyTradeFor(ctx, symbol)
		if c == nil || seen[copyTradeKey(symbol)] {
			return
		}
		seen[copyTradeKey(symbol)] = true
		out = append(out, store.DecisionCopyTrade{Symbol: symbol, Direction: c.Direction, Confidence: c.Confidence,
			LongTraders: c.Long.Traders, ShortTraders: c.Short.Traders, LongAvgEntry: c.Long.AvgEntry, ShortAvgEntry: c.Short.AvgEntry,
			Trapped: c.Long.Trapped + c.Short.Trapped, Fresh: c.Long.Fresh + c.Short.Fresh, Holders: len(c.Holders)})
	}
	for _, p := range ctx.Positions {
		add(p.Symbol)
	}
	for _, c := range ctx.CandidateCoins {
		if _, ok := ctx.MarketDataMap[c.Symbol]; ok || ctx.MarketDataMap == nil {
			add(c.Symbol)
		}
	}
	return out
}

// CopyTradeText is the copy-trade block for one coin in ctx, as the AI
// reads it.
func CopyTradeText(ctx *Context, symbol string, now time.Time) string {
	if ctx == nil || ctx.CopyTradeMap == nil {
		return ""
	}
	price, held := 0.0, ""
	if d := ctx.MarketDataMap[symbol]; d != nil {
		price = d.CurrentPrice
	}
	for _, p := range ctx.Positions {
		if copyTradeKey(p.Symbol) == copyTradeKey(symbol) {
			held = strings.ToLower(p.Side)
			if price == 0 {
				price = p.MarkPrice
			}
		}
	}
	return formatCopyTrade(copyTradeFor(ctx, symbol), ctx.CopyTradeCoverage, now, price, held)
}

// copyTradeVsAvg compares the price with the leading side's average entry.
func copyTradeVsAvg(c *CopyTradeCoin, price float64) string {
	side, avg := "", 0.0
	switch c.Direction {
	case "long":
		side, avg = "long", c.Long.AvgEntry
	case "short":
		side, avg = "short", c.Short.AvgEntry
	}
	if price <= 0 || avg <= 0 {
		return ""
	}
	diff := (price/avg - 1) * 100
	ahead := diff
	if side == "short" {
		ahead = -diff
	}
	note := "near their entry"
	switch {
	case ahead > 3:
		note = "beyond their entry in their direction: chasing"
	case ahead < -3:
		note = "better than their entry, they are under water"
	}
	return fmt.Sprintf("- Price vs lead %s avg entry: %+.1f%% (%s)\n", side, diff, note)
}

// exitAlertShare is how much of one side must have left before the prompt
// warns: one trader reducing says little about the rest.
const exitAlertShare = 3

// copyTradeExitAlert warns when traders on the side the account holds left.
// One or two of many leaving is not a signal; a third of them is.
func copyTradeExitAlert(c *CopyTradeCoin, held string) string {
	if held != "long" && held != "short" {
		return ""
	}
	same, other := c.Long, c.Short
	opposite := "short"
	if held == "short" {
		same, other, opposite = c.Short, c.Long, "long"
	}
	leavers := map[string]bool{}
	exits := 0
	for i, a := range c.Recent {
		if a.Side != held {
			continue
		}
		switch a.Action {
		case "close", "reduce":
			key := a.Trader
			if key == "" {
				key = "#" + strconv.Itoa(i) // no nickname: count the action
			}
			leavers[key] = true
			if a.Action == "close" {
				exits++
			}
		}
	}
	left := len(leavers)
	switch {
	case same.Traders == 0 && other.Traders > 0:
		return fmt.Sprintf("⚠️ Lead traders: no one holds %s any more and %d now hold %s (flipped against your position)\n", held, other.Traders, opposite)
	case same.Traders == 0:
		return fmt.Sprintf("⚠️ Lead traders: no one holds %s any more (%d closed recently)\n", held, exits)
	case c.Direction == opposite:
		return fmt.Sprintf("⚠️ Lead traders: they now lean %s, against your %s position\n", opposite, held)
	case left > 0 && left*exitAlertShare >= same.Traders+left:
		return fmt.Sprintf("⚠️ Lead traders: %d of the %d traders on the %s side closed or reduced recently\n", left, same.Traders+left, held)
	}
	return ""
}

// copyTradeFollowMode is the program-enforced direction rule of a strategy.
func copyTradeFollowMode(source store.CoinSourceConfig) string {
	if !usesCopyTradeContext(source) {
		return "free"
	}
	source.NormalizeCopyTrade()
	return source.CopyTradeFollow
}

// copyTradeGateConfidence is the direction confidence an open needs under
// follow or fade: the source's own threshold when the coins come from the lead
// traders, a separate one when the copy-trade data is only context.
func copyTradeGateConfidence(source store.CoinSourceConfig) float64 {
	source.NormalizeCopyTrade()
	if source.SourceType == "copy_trade" {
		return source.CopyTradeMinConfidence
	}
	if source.CopyTradeContextMinConfidence > 0 && source.CopyTradeContextMinConfidence <= 100 {
		return source.CopyTradeContextMinConfidence
	}
	return store.DefaultCopyTradeContextMinConfidence
}

// OpenBlockReason returns why the program rejects an open decision, or "":
// the copy-trade direction rule, and a stop closer than MinStopATR × ATR14
// of the primary timeframe. Closes are never blocked.
func (e *StrategyEngine) OpenBlockReason(d Decision, ctx *Context) string {
	if e == nil || ctx == nil || (d.Action != "open_long" && d.Action != "open_short") {
		return ""
	}
	side := "long"
	if d.Action == "open_short" {
		side = "short"
	}
	switch mode := copyTradeFollowMode(e.config.CoinSource); mode {
	case "follow", "fade":
		c := copyTradeFor(ctx, d.Symbol)
		lean := ""
		confidence := 0.0
		if c != nil {
			lean, confidence = c.Direction, c.Confidence
		}
		if lean != "long" && lean != "short" {
			return fmt.Sprintf("copy-trade %s mode: lead traders do not lean either way on %s", mode, d.Symbol)
		}
		// One trader agreeing is not a direction: the lean must be clear
		// enough, whether the coins come from the lead traders or the
		// copy-trade data is only context.
		if min := copyTradeGateConfidence(e.config.CoinSource); confidence < min {
			return fmt.Sprintf("copy-trade %s mode: lead traders' confidence %.0f on %s is below the %.0f minimum",
				mode, confidence, d.Symbol, min)
		}
		if (mode == "follow") != (lean == side) {
			return fmt.Sprintf("copy-trade %s mode: lead traders lean %s on %s, open %s rejected", mode, lean, d.Symbol, side)
		}
		if reason := e.copyTradeSignalGate(c, ctx, mode, side, d.Symbol); reason != "" {
			return reason
		}
	}
	if usesCopyTradeContext(e.config.CoinSource) {
		if reason := e.copyTradeVolumeGate(ctx, d.Symbol); reason != "" {
			return reason
		}
	}
	if min := e.config.RiskControl.MinStopATR; min > 0 && d.StopLoss > 0 {
		atr, price := e.primaryATR(ctx, d.Symbol)
		if atr > 0 && price > 0 {
			if dist := math.Abs(price - d.StopLoss); dist < min*atr {
				return fmt.Sprintf("stop loss %.6g is %.2f ATR from price %.6g, below the %.1f ATR minimum", d.StopLoss, dist/atr, price, min)
			}
		}
	}
	return ""
}

// primaryATR is ATR14 and the price of symbol on the primary timeframe.
func (e *StrategyEngine) primaryATR(ctx *Context, symbol string) (float64, float64) {
	data := e.marketData(ctx, symbol)
	if data == nil {
		return 0, 0
	}
	tf := e.config.Indicators.Klines.PrimaryTimeframe
	if s := data.TimeframeData[tf]; s != nil && s.ATR14 > 0 {
		return s.ATR14, data.CurrentPrice
	}
	for _, s := range data.TimeframeData {
		if s != nil && s.ATR14 > 0 {
			return s.ATR14, data.CurrentPrice
		}
	}
	return 0, data.CurrentPrice
}

func copyTradeDataTime(at time.Time) string {
	if at.IsZero() {
		return "unknown"
	}
	return at.UTC().Format(time.RFC3339)
}

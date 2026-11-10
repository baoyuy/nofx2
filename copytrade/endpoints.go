package copytrade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// LeaderQuery selects one leaderboard page.
type LeaderQuery struct {
	TimeRange string // 7D | 30D | 90D
	SortBy    string // ROI | PNL | COPIER_PNL
	Page      int
	Nickname  string
}

// ChartPoint is one point of a lead trader's return curve.
type ChartPoint struct {
	Value    Num   `json:"value"`
	DateTime int64 `json:"dateTime"`
}

// Leader is one leaderboard row.
type Leader struct {
	PortfolioID  string       `json:"leadPortfolioId"`
	Nickname     string       `json:"nickname"`
	AvatarURL    string       `json:"avatarUrl"`
	ROI          Num          `json:"roi"`
	PnL          Num          `json:"pnl"`
	MDD          Num          `json:"mdd"`
	WinRate      Num          `json:"winRate"`
	Sharpe       Num          `json:"sharpRatio"`
	AUM          Num          `json:"aum"`
	CopierPnL    Num          `json:"copierPnl"`
	CopyCount    int          `json:"currentCopyCount"`
	MaxCopyCount int          `json:"maxCopyCount"`
	APIKeyTag    string       `json:"apiKeyTag"`
	TradFiTag    string       `json:"tradFiTag"`
	StartTime    int64        `json:"startTime"`
	Chart        []ChartPoint `json:"chartItems"`
}

type LeaderPage struct {
	Total int      `json:"total"`
	List  []Leader `json:"list"`
}

// Leaderboard pages through Binance's lead traders; Binance returns at most
// 30 rows per page.
func (c *Client) Leaderboard(ctx context.Context, q LeaderQuery) (LeaderPage, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	var out LeaderPage
	err := c.do(ctx, "/v1/friendly/future/copy-trade/home-page/query-list", map[string]any{
		"pageNumber": q.Page, "pageSize": 30, "timeRange": q.TimeRange, "dataType": q.SortBy,
		"favoriteOnly": false, "hideFull": false, "nickname": q.Nickname, "order": "DESC",
		"userAsset": 0, "portfolioType": "ALL",
	}, &out)
	if err != nil {
		return out, err
	}
	bad := 0
	for _, r := range out.List {
		if r.PortfolioID == "" {
			bad++
		}
	}
	return out, c.checkFormat("leaderboard", mostlyOK(bad, len(out.List)) && !(out.Total > 0 && len(out.List) == 0 && q.Page == 1),
		fmt.Sprintf("%d of %d rows without an ID", bad, len(out.List)))
}

// mostlyOK tolerates the odd bad row; more than a fifth means the fields moved.
func mostlyOK(bad, n int) bool { return n == 0 || bad*5 <= n }

// Detail is a lead portfolio's profile.
type Detail struct {
	PortfolioID   string `json:"leadPortfolioId"`
	Nickname      string `json:"nickname"`
	AvatarURL     string `json:"avatarUrl"`
	Status        string `json:"status"` // ACTIVE | CLOSED ...
	PositionShow  bool   `json:"positionShow"`
	MarginBalance Num    `json:"marginBalance"`
	AUM           Num    `json:"aumAmount"`
	CopyCount     int    `json:"currentCopyCount"`
	MaxCopyCount  int    `json:"maxCopyCount"`
	StartTime     int64  `json:"startTime"`
	LastTradeTime int64  `json:"lastTradeTime"`
	// PortfolioType is PUBLIC or PRIVATE. Private portfolios hide their trade
	// history; PublicID names the same trader's public portfolio, if any.
	PortfolioType string `json:"portfolioType"`
	PublicID      string `json:"publicLeadPortfolioId"`
}

// Private tells a portfolio whose trades Binance does not show.
func (d Detail) Private() bool { return strings.EqualFold(d.PortfolioType, "PRIVATE") }

func (c *Client) Detail(ctx context.Context, portfolioID string) (Detail, error) {
	var out Detail
	var raw map[string]json.RawMessage
	err := c.do(ctx, "/v1/friendly/future/copy-trade/lead-portfolio/detail?portfolioId="+url.QueryEscape(portfolioID), nil, &raw)
	if err != nil || len(raw) == 0 {
		return out, err // unknown ID: Binance answers with no data
	}
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &out); err != nil {
		return out, c.checkFormat("detail", false, err.Error())
	}
	return out, c.checkFormat("detail", out.PortfolioID != "" || out.Nickname != "", "profile has no ID or nickname")
}

// Performance is a portfolio's results over one time range.
type Performance struct {
	ROI        Num `json:"roi"`
	PnL        Num `json:"pnl"`
	MDD        Num `json:"mdd"`
	WinRate    Num `json:"winRate"`
	WinOrders  int `json:"winOrders"`
	TotalOrder int `json:"totalOrder"`
}

func (c *Client) Performance(ctx context.Context, portfolioID, timeRange string) (Performance, error) {
	var out Performance
	err := c.do(ctx, "/v1/public/future/copy-trade/lead-portfolio/performance?portfolioId="+
		url.QueryEscape(portfolioID)+"&timeRange="+url.QueryEscape(timeRange), nil, &out)
	return out, err
}

// LivePosition is one row of a portfolio's public positions. Binance lists
// every symbol; rows with zero amount are not held.
type LivePosition struct {
	Symbol           string `json:"symbol"`
	PositionSide     string `json:"positionSide"` // LONG | SHORT | BOTH
	PositionAmount   Num    `json:"positionAmount"`
	EntryPrice       Num    `json:"entryPrice"`
	MarkPrice        Num    `json:"markPrice"`
	Leverage         Num    `json:"leverage"`
	UnrealizedProfit Num    `json:"unrealizedProfit"`
	NotionalValue    Num    `json:"notionalValue"`
}

// Positions returns held positions. Binance returns nothing for portfolios
// that hide positions.
func (c *Client) Positions(ctx context.Context, portfolioID string) ([]LivePosition, error) {
	var rows []LivePosition
	if err := c.do(ctx, "/v1/friendly/future/copy-trade/lead-data/positions?portfolioId="+url.QueryEscape(portfolioID), nil, &rows); err != nil {
		return nil, err
	}
	held := make([]LivePosition, 0, len(rows))
	bad := 0
	for _, r := range rows {
		if r.PositionAmount != 0 {
			held = append(held, r)
			if r.Symbol == "" || r.EntryPrice <= 0 {
				bad++
			}
		}
	}
	return held, c.checkFormat("positions", mostlyOK(bad, len(held)),
		fmt.Sprintf("%d of %d positions without a symbol or entry price", bad, len(held)))
}

// Trade is one fill. A large order shows as many fills with the same time.
type Trade struct {
	Time         int64  `json:"time"`
	Symbol       string `json:"symbol"`
	Side         string `json:"side"`         // BUY | SELL
	PositionSide string `json:"positionSide"` // LONG | SHORT | BOTH (one-way mode)
	Qty          Num    `json:"qty"`
	Price        Num    `json:"price"`
	Fee          Num    `json:"fee"`
	RealizedPnL  Num    `json:"realizedProfit"`
}

type TradePage struct {
	Total int     `json:"total"`
	List  []Trade `json:"list"`
}

// TradePageSize is the page size used for trade history; Binance accepts 50
// and 200 (100 was refused once during testing).
const TradePageSize = 200

// Trades returns one page of fills, newest first.
func (c *Client) Trades(ctx context.Context, portfolioID string, page, size int) (TradePage, error) {
	var out TradePage
	err := c.do(ctx, "/v1/friendly/future/copy-trade/lead-portfolio/trade-history", map[string]any{
		"pageNumber": page, "pageSize": size, "portfolioId": portfolioID,
	}, &out)
	if err != nil {
		return out, err
	}
	bad := 0
	for _, t := range out.List {
		if t.Time <= 0 || t.Symbol == "" || t.Price <= 0 || t.Qty <= 0 {
			bad++
		}
	}
	return out, c.checkFormat("trade-history", mostlyOK(bad, len(out.List)),
		fmt.Sprintf("%d of %d fills without time, symbol, price or quantity", bad, len(out.List)))
}

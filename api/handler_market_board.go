package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nofx/marketboard"
)

// marketBoard is shared by every user: the panels read public data only.
var marketBoard = marketboard.New()

// marketBoardSymbol accepts BTC, BTCUSDT, 1000PEPEUSDT and Binance's
// non-Latin listings such as 哈基米USDT: letters and digits of any script.
var marketBoardSymbol = regexp.MustCompile(`^[\p{L}\p{N}]{1,24}$`)

func (s *Server) registerMarketBoardRoutes(protected *gin.RouterGroup) {
	s.route(protected, "GET", "/market-board/flow", "Binance taker net flow of the 30 most traded perpetuals (?window=1h|4h|24h&limit=25)", s.handleMarketBoardFlow)
	s.route(protected, "GET", "/market-board/direction", "Binance bull/bear direction board of the 30 most traded perpetuals, or of the given markets (?symbols=BTC,ETH)", s.handleMarketBoardDirection)
	s.route(protected, "GET", "/market-board/heatmap", "Estimated cost/liquidation map from Binance candles and open interest (?symbol=BTC)", s.handleMarketBoardHeatmap)
}

func marketBoardContext(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 50*time.Second)
}

func (s *Server) handleMarketBoardFlow(c *gin.Context) {
	ctx, cancel := marketBoardContext(c)
	defer cancel()
	limit := parsePositiveInt(c.Query("limit"), 25)
	if limit > 30 {
		limit = 30
	}
	resp, err := marketBoard.Flow(ctx, c.DefaultQuery("window", "1h"), limit)
	if err != nil {
		SafeError(c, http.StatusBadGateway, "Could not read Binance net flow. Check the network and try again.", err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) handleMarketBoardDirection(c *gin.Context) {
	ctx, cancel := marketBoardContext(c)
	defer cancel()
	// ?symbols=POWR,VVV scores just those markets (the dashboard passes the
	// trader's AI candidates and open positions so the panels follow the AI).
	if raw := strings.TrimSpace(c.Query("symbols")); raw != "" {
		var symbols []string
		for _, sym := range strings.Split(raw, ",") {
			sym = strings.TrimSpace(sym)
			if sym == "" {
				continue
			}
			// Skip anything that is not a market symbol rather than failing
			// the whole board over one odd entry.
			if marketBoardSymbol.MatchString(sym) {
				symbols = append(symbols, sym)
			}
		}
		if len(symbols) > marketboard.MaxDirectionSymbols {
			symbols = symbols[:marketboard.MaxDirectionSymbols]
		}
		c.JSON(http.StatusOK, marketBoard.DirectionFor(ctx, symbols))
		return
	}
	board, err := marketBoard.Direction(ctx)
	if err != nil {
		SafeError(c, http.StatusBadGateway, "Could not build the direction board from Binance. Check the network and try again.", err)
		return
	}
	c.JSON(http.StatusOK, board)
}

func (s *Server) handleMarketBoardHeatmap(c *gin.Context) {
	symbol := strings.TrimSpace(c.Query("symbol"))
	if symbol == "" || len(symbol) > 30 {
		SafeBadRequest(c, "symbol is required")
		return
	}
	ctx, cancel := marketBoardContext(c)
	defer cancel()
	resp, err := marketBoard.Heatmap(ctx, symbol)
	if err != nil {
		SafeError(c, http.StatusBadGateway, "Could not build the cost/liquidation map from Binance. Check the network and try again.", err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

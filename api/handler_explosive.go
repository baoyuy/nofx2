package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nofx/explosive"
)

// explosiveScreener is shared by every user: it reads public market data only.
var explosiveScreener = explosive.Shared

func (s *Server) registerExplosiveRoutes(protected *gin.RouterGroup) {
	s.route(protected, "GET", "/market/explosive", "Binance USDT-M futures top gainers scored for fresh breakouts (?refresh=1)", s.handleExplosiveBoard)
}

func (s *Server) handleExplosiveBoard(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 50*time.Second)
	defer cancel()
	board, err := explosiveScreener.Board(ctx, c.Query("refresh") == "1")
	if err != nil {
		SafeError(c, http.StatusBadGateway, "Could not screen Binance futures. Check the network and try again.", err)
		return
	}
	c.JSON(http.StatusOK, board)
}

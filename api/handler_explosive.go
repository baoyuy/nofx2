package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"nofx/explosive"
)

// explosiveScreener is shared by every user: it reads public market data only.
var explosiveScreener = explosive.Shared

func (s *Server) registerExplosiveRoutes(protected *gin.RouterGroup) {
	s.route(protected, "GET", "/market/explosive", "Binance USDT-M futures top gainers scored for fresh breakouts (?refresh=1)", s.handleExplosiveBoard)
	s.route(protected, "GET", "/market/explosive/accuracy", "Measured record of the explosive board (?days=30)", s.handleExplosiveAccuracy)
	s.route(protected, "GET", "/market/explosive/paper", "Paper-trading record of one board account (?account_id=)", s.handleExplosivePaper)
	s.route(protected, "GET", "/market/explosive/paper/accounts", "Paper accounts of the board's rule engine", s.handleExplosivePaperAccounts)
	s.route(protected, "POST", "/market/explosive/paper/accounts", "Add a paper account from a configured exchange", s.handleExplosivePaperAddAccount)
	s.route(protected, "DELETE", "/market/explosive/paper/accounts/:id", "Remove a paper account", s.handleExplosivePaperRemoveAccount)
}

// StartExplosiveRecorder records the board every 15 minutes and fills in what
// happened next. It does not change the ranking.
func (s *Server) StartExplosiveRecorder(ctx context.Context) {
	rec := explosive.NewRecorder(s.store.Explosive())
	s.explosiveRec = rec
	go rec.Run(ctx)
}

// StartExplosivePaper runs the board's simulated rule engine (no real money).
func (s *Server) StartExplosivePaper(ctx context.Context) {
	s.explosivePaper = explosive.StartPaper(ctx, s.store.Paper())
}

func (s *Server) handleExplosivePaper(c *gin.Context) {
	if s.explosivePaper == nil {
		c.JSON(http.StatusOK, gin.H{"account_id": 0, "label": "默认模拟盘", "starting_equity": 1000, "equity": 1000, "trades": 0, "open": []any{}, "recent": []any{}, "equity_series": []any{}})
		return
	}
	accID, _ := strconv.ParseInt(c.DefaultQuery("account_id", "0"), 10, 64)
	c.JSON(http.StatusOK, s.explosivePaper.Snapshot(accID))
}

func (s *Server) handleExplosivePaperAccounts(c *gin.Context) {
	if s.explosivePaper == nil {
		c.JSON(http.StatusOK, gin.H{"accounts": []any{}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"accounts": s.explosivePaper.Accounts()})
}

func (s *Server) handleExplosivePaperAddAccount(c *gin.Context) {
	if s.explosivePaper == nil {
		SafeError(c, http.StatusServiceUnavailable, "Paper engine not running", nil)
		return
	}
	var req struct {
		ExchangeID     string  `json:"exchange_id"`
		Name           string  `json:"name"`
		StartingEquity float64 `json:"starting_equity"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}
	if req.Name == "" {
		req.Name = "模拟账户"
	}
	id, err := s.explosivePaper.AddAccount(req.ExchangeID, req.Name, req.StartingEquity)
	if err != nil {
		SafeInternalError(c, "Add paper account", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "message": "Paper account added"})
}

func (s *Server) handleExplosivePaperRemoveAccount(c *gin.Context) {
	if s.explosivePaper == nil {
		SafeError(c, http.StatusServiceUnavailable, "Paper engine not running", nil)
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := s.explosivePaper.RemoveAccount(id); err != nil {
		SafeInternalError(c, "Remove paper account", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Paper account removed"})
}

func (s *Server) recorder() *explosive.Recorder {
	if s.explosiveRec == nil {
		s.explosiveRec = explosive.NewRecorder(s.store.Explosive())
	}
	return s.explosiveRec
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

func (s *Server) handleExplosiveAccuracy(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
	acc, err := s.recorder().Accuracy(days, time.Now())
	if err != nil {
		SafeInternalError(c, "Load explosive accuracy", err)
		return
	}
	c.JSON(http.StatusOK, acc)
}

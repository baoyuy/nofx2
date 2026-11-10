package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"nofx/coinpool"
	"nofx/store"
)

// coinPoolScreener is shared by every user and by live horn strategies:
// screens read public data only.
var coinPoolScreener = coinpool.Shared

func (s *Server) registerCoinPoolRoutes(protected *gin.RouterGroup) {
	s.route(protected, "GET", "/coin-pool/screen/bollinger", "Bollinger horn screen of the Binance top 50 gainers (?interval=15m|1h|4h&refresh=1)", s.handleCoinPoolBollinger)
	s.route(protected, "GET", "/coin-pools", "Saved coin pools of the current user", s.handleListCoinPools)
	s.route(protected, "POST", "/coin-pools", "Save a coin pool (body: name, screener, interval, coins)", s.handleCreateCoinPool)
	s.route(protected, "PUT", "/coin-pools/:id", "Replace a coin pool's name and coins", s.handleUpdateCoinPool)
	s.route(protected, "DELETE", "/coin-pools/:id", "Delete a coin pool", s.handleDeleteCoinPool)
}

func (s *Server) handleCoinPoolBollinger(c *gin.Context) {
	interval := c.DefaultQuery("interval", "1h")
	if !coinpool.Intervals[interval] {
		SafeBadRequest(c, "Unsupported interval; use 15m, 1h or 4h")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 50*time.Second)
	defer cancel()
	board, err := coinPoolScreener.HornBoard(ctx, interval, c.Query("refresh") == "1")
	if err != nil {
		SafeError(c, http.StatusBadGateway, "Could not screen Binance markets. Check the network and try again.", err)
		return
	}
	c.JSON(http.StatusOK, board)
}

type coinPoolRequest struct {
	Name     string               `json:"name"`
	Screener string               `json:"screener"`
	Interval string               `json:"interval"`
	Coins    []store.CoinPoolCoin `json:"coins"`
}

func (r coinPoolRequest) pool() *store.CoinPool {
	return &store.CoinPool{Name: r.Name, Screener: r.Screener, Interval: r.Interval, Coins: r.Coins}
}

func (s *Server) handleListCoinPools(c *gin.Context) {
	pools, err := s.store.CoinPool().List(c.GetString("user_id"))
	if err != nil {
		SafeInternalError(c, "Load coin pools", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"pools": pools})
}

func (s *Server) handleCreateCoinPool(c *gin.Context) {
	var req coinPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid coin pool")
		return
	}
	pool := req.pool()
	if err := s.store.CoinPool().Create(c.GetString("user_id"), pool); err != nil {
		SafeBadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, pool)
}

func (s *Server) handleUpdateCoinPool(c *gin.Context) {
	var req coinPoolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid coin pool")
		return
	}
	pool := req.pool()
	pool.ID = c.Param("id")
	if err := s.store.CoinPool().Update(c.GetString("user_id"), pool); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Coin pool not found"})
			return
		}
		SafeBadRequest(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, pool)
}

func (s *Server) handleDeleteCoinPool(c *gin.Context) {
	if err := s.store.CoinPool().Delete(c.GetString("user_id"), c.Param("id")); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Coin pool not found"})
			return
		}
		SafeInternalError(c, "Delete coin pool", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// checkStrategyCoinPool makes sure a strategy only references the user's own
// pool and refreshes its snapshot. Strategies run without a user context.
func (s *Server) checkStrategyCoinPool(userID string, config *store.StrategyConfig) error {
	source := &config.CoinSource
	if source.SourceType != "coin_pool" {
		source.CoinPoolID = ""
		return nil
	}
	pool, err := s.store.CoinPool().Get(userID, source.CoinPoolID)
	if err != nil {
		return errors.New("Selected coin pool was not found")
	}
	snapshot := make([]string, 0, len(pool.Coins))
	for _, coin := range pool.Coins {
		snapshot = append(snapshot, coin.Symbol)
	}
	if len(snapshot) > store.MaxCandidateCoins {
		snapshot = snapshot[:store.MaxCandidateCoins]
	}
	source.StaticCoins = snapshot
	return nil
}

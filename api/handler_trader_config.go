package api

import (
	"net/http"

	"nofx/logger"
	"nofx/trader"

	"github.com/gin-gonic/gin"
)

// handleUpdateTraderPrompt Update trader custom prompt
func (s *Server) handleUpdateTraderPrompt(c *gin.Context) {
	traderID := c.Param("id")
	userID := c.GetString("user_id")

	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	var req struct {
		CustomPrompt       string `json:"custom_prompt"`
		OverrideBasePrompt bool   `json:"override_base_prompt"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}
	unlockStrategy := trader.LockStrategyLifecycle()
	defer unlockStrategy()

	// Authorize and validate capability before persisting anything. The grid
	// decision path does not consume trader-level prompt overlays.
	cfg, err := s.store.Trader().GetByID(traderID)
	if err != nil || cfg.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "Trader not found"})
		return
	}
	if cfg.StrategyID != "" {
		strategy, err := s.store.Strategy().Get(userID, cfg.StrategyID)
		if err != nil {
			SafeBadRequest(c, "Trader strategy not found")
			return
		}
		config, err := strategy.ParseConfig()
		if err != nil {
			SafeBadRequest(c, "Invalid trader strategy configuration")
			return
		}
		if err := trader.ValidateTraderPrompt(config.StrategyType, req.CustomPrompt, req.OverrideBasePrompt); err != nil {
			SafeBadRequest(c, err.Error())
			return
		}
	}
	var runtime *trader.AutoTrader
	if s.traderManager != nil {
		runtime, _ = s.traderManager.GetTrader(traderID)
		if runtime != nil {
			if config := runtime.GetStrategyConfig(); config != nil {
				if err := trader.ValidateTraderPrompt(config.StrategyType, req.CustomPrompt, req.OverrideBasePrompt); err != nil {
					SafeBadRequest(c, err.Error())
					return
				}
			}
		}
	}
	// Update database
	err = s.store.Trader().UpdateCustomPrompt(userID, traderID, req.CustomPrompt, req.OverrideBasePrompt)
	if err != nil {
		SafeInternalError(c, "Failed to update custom prompt", err)
		return
	}

	// If trader is in memory, update its custom prompt and override settings
	if runtime != nil {
		if err := runtime.SetPromptSettings(req.CustomPrompt, req.OverrideBasePrompt); err != nil {
			SafeBadRequest(c, err.Error())
			return
		}
		logger.Infof("✓ Updated trader %s custom prompt (override base=%v)", runtime.GetName(), req.OverrideBasePrompt)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Custom prompt updated"})
}

// handleToggleCompetition Toggle trader competition visibility
func (s *Server) handleToggleCompetition(c *gin.Context) {
	traderID := c.Param("id")
	userID := c.GetString("user_id")

	var req struct {
		ShowInCompetition bool `json:"show_in_competition"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}

	// Update database
	err := s.store.Trader().UpdateShowInCompetition(userID, traderID, req.ShowInCompetition)
	if err != nil {
		SafeInternalError(c, "Update competition visibility", err)
		return
	}

	// Update in-memory trader if it exists
	if trader, err := s.traderManager.GetTrader(traderID); err == nil {
		trader.SetShowInCompetition(req.ShowInCompetition)
	}

	status := "shown"
	if !req.ShowInCompetition {
		status = "hidden"
	}
	logger.Infof("✓ Trader %s competition visibility updated: %s", traderID, status)
	c.JSON(http.StatusOK, gin.H{
		"message":             "Competition visibility updated",
		"show_in_competition": req.ShowInCompetition,
	})
}

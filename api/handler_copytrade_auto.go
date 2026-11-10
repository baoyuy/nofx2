package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nofx/copytrade"
	"nofx/logger"
	"nofx/store"
)

// copyTradeAutoKey stores the auto-follow setting (JSON) in system_config.
const copyTradeAutoKey = "copytrade_auto_follow"

// loadCopyTradeAuto reads the saved setting; off with the default target when
// nothing is saved.
func (s *Server) loadCopyTradeAuto() copytrade.AutoConfig {
	cfg := copytrade.AutoConfig{Target: copytrade.DefaultAutoTarget}
	raw, err := s.store.GetSystemConfig(copyTradeAutoKey)
	if err != nil {
		logger.Warnf("copy-trade: read auto-follow setting: %v", err)
		return cfg
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			logger.Warnf("copy-trade: bad auto-follow setting ignored: %v", err)
			return copytrade.AutoConfig{Target: copytrade.DefaultAutoTarget}
		}
	}
	return cfg
}

func (s *Server) registerCopyTradeAutoRoutes(protected *gin.RouterGroup) {
	s.route(protected, "GET", "/copytrade/auto", "Auto-follow setting, latest run, recent actions and excluded traders", s.handleCopyTradeAutoGet)
	s.route(protected, "PUT", "/copytrade/auto", "Set auto-follow (body: enabled bool, target 5-100 traders); runs soon when enabled", s.handleCopyTradeAutoPut)
	s.route(protected, "DELETE", "/copytrade/auto/exclusions/:id", "Let auto-follow pick an excluded lead trader again", s.handleCopyTradeAutoUnexclude)
}

func (s *Server) handleCopyTradeAutoGet(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	now := time.Now()
	st := s.store.CopyTrade()
	logs, err := st.AutoLogs(now.Add(-30*24*time.Hour).UnixMilli(), 100)
	if err != nil {
		SafeInternalError(c, "Read auto-follow log", err)
		return
	}
	ex, err := st.Exclusions(now.UnixMilli())
	if err != nil {
		SafeInternalError(c, "Read auto-follow exclusions", err)
		return
	}
	auto, manual := 0, 0
	for _, sn := range s.copyTrade.Snapshots() {
		if sn.Follow.Source == "manual" {
			manual++
		} else {
			auto++
		}
	}
	c.JSON(http.StatusOK, gin.H{"state": s.copyTrade.AutoState(), "log": logs, "exclusions": ex,
		"auto_follows": auto, "manual_follows": manual,
		"min_target": copytrade.MinAutoTarget, "max_target": copytrade.MaxFollows})
}

func (s *Server) handleCopyTradeAutoPut(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	var req copytrade.AutoConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "enabled and target are required")
		return
	}
	if req.Target < copytrade.MinAutoTarget || req.Target > copytrade.MaxFollows {
		SafeBadRequest(c, "target must be 5-100")
		return
	}
	raw, _ := json.Marshal(req)
	if err := s.store.SetSystemConfig(copyTradeAutoKey, string(raw)); err != nil {
		SafeInternalError(c, "Save auto-follow setting", err)
		return
	}
	s.copyTrade.SetAuto(req)
	c.JSON(http.StatusOK, gin.H{"state": s.copyTrade.AutoState()})
}

func (s *Server) handleCopyTradeAutoUnexclude(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	if err := s.store.CopyTrade().Unexclude(c.Param("id")); err != nil {
		SafeInternalError(c, "Remove auto-follow exclusion", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// excludeUnfollowed keeps a trader the user unfollowed out of auto-follow, so
// it does not come straight back.
func (s *Server) excludeUnfollowed(snap copytrade.Snapshot) {
	err := s.store.CopyTrade().Exclude(store.CopyTradeExclusion{PortfolioID: snap.Follow.PortfolioID,
		Nickname: snap.Follow.Nickname, Reason: "manual", CreatedAt: time.Now().UnixMilli()})
	if err != nil {
		logger.Warnf("copy-trade: exclude unfollowed %s: %v", snap.Follow.PortfolioID, err)
	}
}

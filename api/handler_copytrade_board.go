package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nofx/copytrade"
	"nofx/logger"
)

// copyTradeBoardKey stores the board-sync setting (JSON) in system_config.
const copyTradeBoardKey = "copytrade_board_sync"

// loadCopyTradeBoard reads the saved board-sync setting; on by default so a
// fresh deploy starts following the board.
func (s *Server) loadCopyTradeBoard() copytrade.BoardSyncConfig {
	cfg := copytrade.BoardSyncConfig{Enabled: true}
	raw, err := s.store.GetSystemConfig(copyTradeBoardKey)
	if err != nil {
		logger.Warnf("copy-trade: read board-sync setting: %v", err)
		return cfg
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			logger.Warnf("copy-trade: bad board-sync setting ignored: %v", err)
			return copytrade.BoardSyncConfig{Enabled: true}
		}
	}
	return cfg
}

func (s *Server) registerCopyTradeBoardRoutes(protected *gin.RouterGroup) {
	s.route(protected, "GET", "/copytrade/board", "Board sync: latest run, followed ranks and observe-only private traders", s.handleCopyTradeBoardGet)
	s.route(protected, "PUT", "/copytrade/board", "Enable or disable the daily board sync (body: enabled bool)", s.handleCopyTradeBoardPut)
	s.route(protected, "POST", "/copytrade/board/run", "Run the board sync now", s.handleCopyTradeBoardRun)
	s.route(protected, "GET", "/copytrade/paper", "Simulated copy-trade record: fixed roster, fills and the per-dimension report", s.handleCopyTradePaperGet)
	s.route(protected, "POST", "/copytrade/paper/start", "Start a new simulated-copy roster from the current board follows (body: ids [] optional)", s.handleCopyTradePaperStart)
}

func (s *Server) handleCopyTradeBoardGet(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	members, err := s.store.CopyTrade().ListBoardMembers()
	if err != nil {
		SafeInternalError(c, "Read board members", err)
		return
	}
	following, observe := 0, 0
	for _, m := range members {
		if m.Observe {
			observe++
		} else if m.Followed {
			following++
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"state": s.copyTrade.BoardState(), "members": members,
		"following": following, "observe": observe,
		"board_size": copytrade.BoardSize, "max_follows": copytrade.MaxFollows,
	})
}

func (s *Server) handleCopyTradeBoardPut(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	var req copytrade.BoardSyncConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "enabled is required")
		return
	}
	raw, _ := json.Marshal(req)
	if err := s.store.SetSystemConfig(copyTradeBoardKey, string(raw)); err != nil {
		SafeInternalError(c, "Save board-sync setting", err)
		return
	}
	s.copyTrade.SetBoardSync(req)
	c.JSON(http.StatusOK, gin.H{"state": s.copyTrade.BoardState()})
}

func (s *Server) handleCopyTradeBoardRun(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	go s.copyTrade.RunBoardSync(c.Request.Context(), time.Now())
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleCopyTradePaperGet(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	st := s.store.CopyTrade()
	roster, err := st.ActivePaperRoster()
	if err != nil {
		SafeInternalError(c, "Read paper roster", err)
		return
	}
	rosters, _ := st.ListPaperRosters()
	resp := gin.H{"roster": roster, "rosters": rosters, "fills": 0, "rows": []copytrade.PaperRow{}}
	if roster != nil {
		fills, err := st.PaperFills(roster.ID)
		if err != nil {
			SafeInternalError(c, "Read paper fills", err)
			return
		}
		resp["fills"] = len(fills)
		resp["rows"] = copytrade.PaperReport(fills)
		resp["members"] = len(splitCSV(roster.Members))
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) handleCopyTradePaperStart(c *gin.Context) {
	if !s.copyTradeReady(c) {
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	_ = c.ShouldBindJSON(&req)
	ids := req.IDs
	note := "manual"
	if len(ids) == 0 {
		// Default to the current board follows: fixed from now on.
		for _, sn := range s.copyTrade.Snapshots() {
			if sn.Follow.Source == "board" && !sn.Follow.Private {
				ids = append(ids, sn.Follow.PortfolioID)
			}
		}
		note = "board follows at start"
	}
	roster, err := s.copyTrade.StartPaperRoster(ids, note, time.Now())
	if err != nil {
		SafeInternalError(c, "Start paper roster", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"roster": roster})
}

// splitCSV splits a comma-separated list, dropping blanks.
func splitCSV(sv string) []string {
	var out []string
	for _, p := range strings.Split(sv, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

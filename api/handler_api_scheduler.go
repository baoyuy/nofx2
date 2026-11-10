package api

import (
	"net/http"
	"nofx/internal/apischeduler"

	"github.com/gin-gonic/gin"
)

// Counters are process-wide, not exchange quotas or account balances.
func (s *Server) handleAPISchedulerStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"scope":     "process",
		"providers": apischeduler.Snapshot(),
	})
}

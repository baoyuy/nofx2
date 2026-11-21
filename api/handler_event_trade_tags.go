package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"nofx/store"
)

// handleEventTradeTags returns the latest two-model event stance per coin for
// the current user's traders. Read-only; the Events page renders it so tagging
// is visible without opening the trading dashboard.
func (s *Server) handleEventTradeTags(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	tags, err := s.store.EventTradeTags().ListByUser(userID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Event trade tags unavailable"})
		return
	}
	if tags == nil {
		tags = []store.EventTradeTag{}
	}
	c.JSON(http.StatusOK, gin.H{"tags": tags})
}

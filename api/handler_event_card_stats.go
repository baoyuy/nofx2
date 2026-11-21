package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// handleEventCardStats returns hit/miss/pending counts for the user's event cards.
func (s *Server) handleEventCardStats(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	stats, err := s.store.EventCards().CardStats(userID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Event card stats unavailable"})
		return
	}
	c.JSON(http.StatusOK, stats)
}

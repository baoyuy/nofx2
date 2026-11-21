package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"nofx/logger"
	"nofx/store"
)

// handleEventCards returns the latest tagged information-event cards (catalysts)
// for the current user's traders. Read-only intelligence for display on the
// Events page; never an order.
func (s *Server) handleEventCards(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	cards, err := s.store.EventCards().ListByUser(userID)
	if err != nil {
		logger.Warnf("event cards list failed: user=%s err=%v", userID, err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Event cards unavailable", "detail": err.Error()})
		return
	}
	if tid := c.Query("trader_id"); tid != "" {
		filtered := make([]store.EventCard, 0, len(cards))
		for _, card := range cards {
			if card.TraderID == tid {
				filtered = append(filtered, card)
			}
		}
		cards = filtered
	}
	if cards == nil {
		cards = []store.EventCard{}
	}
	c.JSON(http.StatusOK, gin.H{"cards": cards})
}

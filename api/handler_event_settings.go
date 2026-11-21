package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type eventLabelSettingsRequest struct {
	ModelID   *string `json:"model_id"`
	AutoLabel *bool   `json:"auto_label"`
}

func decodeEventLabelSettingsRequest(r io.Reader) (eventLabelSettingsRequest, error) {
	var req eventLabelSettingsRequest
	dec := json.NewDecoder(io.LimitReader(r, 2049))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, err
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return req, fmt.Errorf("invalid JSON")
	}
	if req.ModelID == nil || req.AutoLabel == nil || len(*req.ModelID) > 128 {
		return req, fmt.Errorf("model_id and auto_label are required")
	}
	return req, nil
}

func (s *Server) handleEventLabelSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	if c.Request.Method == http.MethodGet {
		settings, err := s.store.EventLabelSettings().Get(userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Event settings unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"model_id": settings.ModelID, "auto_label": settings.AutoLabel})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
	req, err := decodeEventLabelSettingsRequest(c.Request.Body)
	if err != nil {
		SafeBadRequest(c, "Expected model_id and auto_label only")
		return
	}
	modelID := strings.TrimSpace(*req.ModelID)
	autoLabel := *req.AutoLabel
	if modelID == "" && autoLabel {
		SafeBadRequest(c, "auto_label requires an enabled model")
		return
	}
	if modelID != "" {
		model, modelErr := s.store.AIModel().Get(userID, modelID)
		if modelErr != nil || model == nil || model.UserID != userID || !model.Enabled {
			SafeBadRequest(c, "Enabled model not found")
			return
		}
	}
	if err := s.store.EventLabelSettings().Set(userID, modelID, autoLabel); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Event settings unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"model_id": modelID, "auto_label": autoLabel})
}

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"nofx/events"
	"nofx/mcp"
	"nofx/store"
)

type eventLabelRequest struct {
	EventID string `json:"event_id"`
	ModelID string `json:"model_id"`
}

func decodeEventLabelRequest(r io.Reader) (eventLabelRequest, error) {
	var req eventLabelRequest
	d := json.NewDecoder(io.LimitReader(r, 2049))
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		return req, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return req, fmt.Errorf("invalid JSON")
	}
	if strings.TrimSpace(req.EventID) == "" || len(req.EventID) > 256 || strings.TrimSpace(req.ModelID) == "" || len(req.ModelID) > 128 {
		return req, fmt.Errorf("missing event or model")
	}
	return req, nil
}
func (s *Server) eventLabelModel(userID, id string) (*store.AIModel, error) {
	model, err := s.store.AIModel().Get(userID, id)
	if err != nil || model == nil || model.UserID != userID || !model.Enabled || model.APIKey == "" {
		return nil, fmt.Errorf("enabled model unavailable")
	}
	return model, nil
}
func (s *Server) handleEventLabel(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
	req, err := decodeEventLabelRequest(c.Request.Body)
	if err != nil {
		SafeBadRequest(c, "Expected event_id and model_id only")
		return
	}
	model, err := s.eventLabelModel(userID, req.ModelID)
	if err != nil {
		SafeBadRequest(c, "Enabled model not found")
		return
	}
	opts, err := eventHistoryOptions(c)
	if err != nil {
		SafeBadRequest(c, err.Error())
		return
	}
	board, err := s.buildEventBoard(c.Request.Context(), userID, opts)
	if err != nil {
		c.JSON(503, gin.H{"error": "Event data unavailable"})
		return
	}
	var selected *events.Event
	for i := range board.Events {
		if board.Events[i].ID == req.EventID {
			if selected != nil {
				SafeBadRequest(c, "Ambiguous event ID")
				return
			}
			selected = &board.Events[i]
		}
	}
	if selected == nil {
		c.JSON(404, gin.H{"error": "Event not found in current running strategy scope"})
		return
	}
	if selected.ReceivedAt.IsZero() || time.Since(selected.ReceivedAt) > 15*time.Minute || selected.ReceivedAt.After(time.Now().Add(time.Minute)) || (selected.EndAt != nil && !selected.EndAt.After(time.Now())) {
		c.JSON(409, gin.H{"error": "Event snapshot is stale; refresh before labeling"})
		return
	}
	fp := events.LabelModelFingerprint(model)
	in, hash, err := events.BuildLabelInput(*selected, fp)
	if err != nil {
		SafeBadRequest(c, "Event cannot be labeled")
		return
	}
	key := store.EventLabel{UserID: userID, EventID: req.EventID, ModelID: model.ID, Fingerprint: fp, InputHash: hash}
	labels := s.store.EventLabels()
	row, token, err := labels.Reserve(key, time.Now().UTC())
	if err != nil {
		var limit *store.EventLabelLimit
		if errors.As(err, &limit) {
			status := 429
			if limit.Busy {
				status = 409
			}
			c.Header("Retry-After", strconv.Itoa(limit.RetryAfter))
			c.JSON(status, gin.H{"error": "Event labeling temporarily limited", "retry_after": limit.RetryAfter})
			return
		}
		c.JSON(503, gin.H{"error": "Event labels unavailable"})
		return
	}
	if row != nil {
		analysis, err := events.ParseLabel(row.AnalysisJSON, in)
		if err != nil {
			c.JSON(503, gin.H{"error": "Event label unavailable"})
			return
		}
		c.JSON(200, events.Label{ModelID: model.ID, ModelName: model.Name, InputHash: hash, AnalyzedAt: row.AnalyzedAt, Cached: true, Analysis: analysis})
		return
	}
	finished := false
	defer func() {
		if !finished {
			_ = labels.Finish(key, token, "", time.Now().UTC())
		}
	}()
	if c.Request.Context().Err() != nil {
		return
	}
	// CallWithMessages is text-only: no tools, order execution, or planner. One
	// attempt, bounded response and HTTP timeout; never retry malformed output.
	user, _ := json.Marshal(in)
	raw, err := s.runRealAITest(userID, model.ID, events.LabelSystemPrompt, string(user), mcp.WithMaxRetries(1), mcp.WithTimeout(75*time.Second), mcp.WithMaxTokens(2400))
	if err != nil {
		c.JSON(502, gin.H{"error": "Event analysis failed; please try later"})
		return
	}
	analysis, err := events.ParseLabel(raw, in)
	if err != nil {
		c.JSON(502, gin.H{"error": "Event analysis failed; please try later"})
		return
	}
	// Recheck configuration before committing, avoiding labels attributed to a
	// configuration changed while the request was in flight.
	current, err := s.eventLabelModel(userID, model.ID)
	if err != nil || events.LabelModelFingerprint(current) != fp {
		c.JSON(409, gin.H{"error": "Model configuration changed"})
		return
	}
	parsed, _ := json.Marshal(analysis)
	at := time.Now().UTC()
	if err = labels.Finish(key, token, string(parsed), at); err != nil {
		c.JSON(503, gin.H{"error": "Event label could not be saved"})
		return
	}
	finished = true
	c.JSON(200, events.Label{ModelID: model.ID, ModelName: model.Name, InputHash: hash, AnalyzedAt: at, Analysis: analysis})
}

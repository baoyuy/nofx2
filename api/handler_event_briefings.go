package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"nofx/events"
	"nofx/mcp"
	"nofx/store"
	"sort"
	"strconv"
	"strings"
	"time"
)

type eventBriefingRequest struct {
	ModelID string `json:"model_id"`
}

func decodeEventBriefingRequest(r io.Reader) (eventBriefingRequest, error) {
	var req eventBriefingRequest
	data, err := io.ReadAll(io.LimitReader(r, 2049))
	if err != nil || len(data) > 2048 {
		return req, fmt.Errorf("invalid request")
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil {
		return req, fmt.Errorf("invalid request")
	}
	var extra any
	if d.Decode(&extra) != io.EOF || strings.TrimSpace(req.ModelID) == "" || len(req.ModelID) > 128 {
		return req, fmt.Errorf("invalid request")
	}
	return req, nil
}

// Stored envelopes contain original coverage, never source text or prior input.
func cachedEventBriefing(row *store.EventLabel, model *store.AIModel, hash string) (events.Briefing, error) {
	var result events.Briefing
	if err := json.Unmarshal([]byte(row.AnalysisJSON), &result); err != nil || result.InputHash != row.InputHash || result.ModelID != model.ID || result.Briefings == nil {
		return result, fmt.Errorf("invalid cache")
	}
	result.ModelName = model.Name
	result.AnalyzedAt = row.AnalyzedAt
	result.Cached = true
	result.Stale = row.InputHash != hash
	if result.Stale {
		result.Warnings = append(result.Warnings, "当前资料或范围已变化；以下是旧版本简报及其原始覆盖范围，不代表当前资料")
	}
	return result, nil
}
func briefingLimitResponse(c *gin.Context, err error) {
	var limit *store.EventLabelLimit
	if errors.As(err, &limit) {
		status := 429
		if limit.Busy {
			status = 409
		}
		c.Header("Retry-After", strconv.Itoa(limit.RetryAfter))
		c.JSON(status, gin.H{"error": "Event briefing temporarily limited", "retry_after": limit.RetryAfter})
		return
	}
	c.JSON(503, gin.H{"error": "Event briefing unavailable"})
}

func (s *Server) handleEventBriefing(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(401, gin.H{"error": "Authentication required"})
		return
	}
	id := c.Query("model_id")
	if c.Request.Method == http.MethodPost {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
		req, err := decodeEventBriefingRequest(c.Request.Body)
		if err != nil {
			SafeBadRequest(c, "Expected model_id only")
			return
		}
		id = req.ModelID
	}
	if strings.TrimSpace(id) == "" || len(id) > 128 {
		SafeBadRequest(c, "Enabled model not found")
		return
	}
	model, err := s.eventLabelModel(userID, id)
	if err != nil {
		SafeBadRequest(c, "Enabled model not found")
		return
	}
	// Deliberately independent of UI query/history parameters.
	board, err := s.buildEventBoard(c.Request.Context(), userID, events.HistoryOptions{Window: time.Hour, ThresholdPP: 5, EndingWithin: 24 * time.Hour})
	if err != nil {
		c.JSON(503, gin.H{"error": "Event data unavailable"})
		return
	}
	fp := events.LabelModelFingerprint(model)
	in, coverage, warnings, hash, err := events.BuildBriefingInput(board, fp, time.Now().UTC())
	if err != nil {
		c.JSON(503, gin.H{"error": "Event data unavailable"})
		return
	}
	labels := s.store.EventLabels()
	if c.Request.Method == http.MethodGet {
		row, err := labels.Latest(userID, events.BriefingEventID, model.ID, fp)
		if err != nil {
			c.JSON(503, gin.H{"error": "Event briefing unavailable"})
			return
		}
		if row == nil {
			c.JSON(200, gin.H{"status": "empty"})
			return
		}
		result, err := cachedEventBriefing(row, model, hash)
		if err != nil {
			c.JSON(503, gin.H{"error": "Event briefing unavailable"})
			return
		}
		c.JSON(200, result)
		return
	}
	if len(in.Evidence) == 0 {
		c.JSON(200, gin.H{"status": "empty", "coverage": coverage, "warnings": warnings})
		return
	}
	key := store.EventLabel{UserID: userID, EventID: events.BriefingEventID, ModelID: model.ID, Fingerprint: fp, InputHash: hash}
	row, token, err := labels.Reserve(key, time.Now().UTC())
	if err != nil {
		briefingLimitResponse(c, err)
		return
	}
	if row != nil {
		result, err := cachedEventBriefing(row, model, hash)
		if err != nil {
			c.JSON(503, gin.H{"error": "Event briefing unavailable"})
			return
		}
		c.JSON(200, result)
		return
	}
	finished := false
	defer func() {
		if !finished {
			_ = labels.Finish(key, token, "", time.Now().UTC())
		}
	}()
	// Reserve once per click; each coin receives one bounded, independent model call.
	briefings := make([]events.CoinBriefing, 0, len(in.Coins))
	coinLimit := len(in.Coins)
	if coinLimit > 10 {
		coinLimit = 10
		warnings = append(warnings, "币种范围超过10，超出部分未进行AI简报")
	}
	for _, coin := range in.Coins[:coinLimit] {
		if c.Request.Context().Err() != nil {
			warnings = append(warnings, "部分币种因请求取消未完成简报")
			break
		}
		coinInput, inputErr := events.BuildCoinBriefingInput(in, coin)
		if inputErr != nil {
			warnings = append(warnings, "部分币种因证据不足未完成简报")
			continue
		}
		coinJSON, _ := json.Marshal(coinInput)
		raw, callErr := s.runRealAITest(userID, model.ID, events.BriefingSystemPrompt, string(coinJSON), mcp.WithMaxRetries(0), mcp.WithTimeout(30*time.Second), mcp.WithMaxTokens(1800))
		if callErr != nil {
			warnings = append(warnings, "部分币种简报生成失败")
			continue
		}
		analysis, parseErr := events.ParseCoinBriefing(raw, coinInput)
		if parseErr != nil {
			warnings = append(warnings, "部分币种简报结果无效")
			continue
		}
		briefings = append(briefings, analysis)
	}
	sort.Slice(briefings, func(i, j int) bool { return briefings[i].Coin < briefings[j].Coin })
	if len(briefings) == 0 {
		_ = labels.Finish(key, token, "", time.Now().UTC())
		finished = true
		c.JSON(502, gin.H{"error": "Event briefing failed; please try later"})
		return
	}
	current, err := s.eventLabelModel(userID, model.ID)
	if err != nil || events.LabelModelFingerprint(current) != fp {
		c.JSON(409, gin.H{"error": "Model configuration changed"})
		return
	}
	_, _, _, currentHash, err := events.BuildBriefingInput(board, fp, time.Now().UTC())
	if err != nil || currentHash != hash {
		c.JSON(409, gin.H{"error": "Event snapshot expired; refresh before analysis"})
		return
	}
	at := time.Now().UTC()
	result := events.Briefing{ModelID: model.ID, ModelName: model.Name, InputHash: hash, AnalyzedAt: at, Coverage: coverage, Briefings: briefings, Warnings: warnings}
	parsed, _ := json.Marshal(result)
	if err = labels.Finish(key, token, string(parsed), at); err != nil {
		c.JSON(503, gin.H{"error": "Event briefing could not be saved"})
		return
	}
	finished = true
	c.JSON(200, result)
}

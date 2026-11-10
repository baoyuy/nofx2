package provider

import (
	"strings"
	"testing"

	"nofx/mcp"
)

func TestClaudeParseSurfacesRefusal(t *testing.T) {
	c := NewClaudeClientWithOptions(mcp.WithLogger(mcp.NewNoopLogger())).(*ClaudeClient)

	_, err := c.ParseMCPResponse([]byte(`{"type":"message","role":"assistant","content":[],"stop_reason":"refusal","usage":{"input_tokens":10,"output_tokens":1}}`))
	if err == nil || !strings.Contains(err.Error(), "refusal") {
		t.Fatalf("refusal must be an error, got %v", err)
	}

	text, err := c.ParseMCPResponse([]byte(`{"type":"message","role":"assistant","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`))
	if err != nil || text != "OK" {
		t.Fatalf("normal reply = %q, %v", text, err)
	}
}

package provider

import (
	"testing"

	"nofx/mcp"
)

func TestDeepSeekReasoningBudgetUsesDocumentedParameters(t *testing.T) {
	for _, effort := range []string{"", "none", "low"} {
		client := NewDeepSeekClientWithOptions(mcp.WithModel("deepseek-v4.1-flash"), mcp.WithReasoningEffort(effort)).(*DeepSeekClient)
		for _, body := range []map[string]any{client.BuildMCPRequestBody("system", "user"), client.BuildRequestBodyFromRequest(&mcp.Request{})} {
			thinking, _ := body["thinking"].(map[string]any)
			switch effort {
			case "":
				if thinking != nil {
					t.Fatal("changed default model behavior")
				}
			case "none":
				if thinking["type"] != "disabled" || body["reasoning_effort"] != nil {
					t.Fatal("non-thinking mode must use thinking.disabled, not unsupported effort none")
				}
			case "low":
				if thinking["type"] != "enabled" || body["reasoning_effort"] != "low" {
					t.Fatal("lost explicit low reasoning budget")
				}
			}
		}
	}
	legacy := NewDeepSeekClientWithOptions(mcp.WithModel("deepseek-chat"), mcp.WithReasoningEffort("none")).(*DeepSeekClient)
	if legacy.BuildMCPRequestBody("", "test")["thinking"] != nil {
		t.Fatal("sent V4 parameters to legacy model")
	}
}

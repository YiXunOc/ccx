package chat

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func TestChatCacheUsageRegression(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	upstream := `data: {"choices":[{"delta":{"content":"ok"},"finish_reason":null}]}

data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":70}}}

data: [DONE]

`
	got, err := streamPassthrough(c, &http.Response{Body: io.NopCloser(strings.NewReader(upstream))}, w, nil, false, nil,
		common.StreamPreflightTimeouts{InactivityTimeoutMs: 1000}, common.NewStreamProgressLogger("Test", time.Now(), false))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.CacheReadInputTokens != 70 || got.PromptTokensTotal != 100 {
		t.Fatalf("cache usage lost: %+v", got)
	}
	if w.Body.String() != upstream {
		t.Fatal("passthrough stream changed")
	}
}

func TestResponsesChatStreamCacheUsageRegression(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("requestBodyBytes", []byte(`{"stream_options":{"include_usage":true}}`))
	input := `data: {"type":"response.output_text.delta","delta":"ok"}

data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":100,"output_tokens":5,"input_tokens_details":{"cached_tokens":70}}}}

`
	got, err := streamResponsesToChat(c, &http.Response{Body: io.NopCloser(strings.NewReader(input))}, w, "test", nil, false, nil,
		common.StreamPreflightTimeouts{InactivityTimeoutMs: 1000}, common.NewStreamProgressLogger("Test", time.Now(), false))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.CacheReadInputTokens != 70 || got.PromptTokensTotal != 100 {
		t.Fatalf("cache usage lost: %+v", got)
	}
	found := false
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var chunk map[string]interface{}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk) != nil {
			continue
		}
		if usage, ok := chunk["usage"].(map[string]interface{}); ok {
			details, _ := usage["prompt_tokens_details"].(map[string]interface{})
			if details["cached_tokens"] == float64(70) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("client cache usage missing")
	}
}

func TestChatUsageFromClaudeAndGemini(t *testing.T) {
	claude := map[string]interface{}{"input_tokens": float64(30), "output_tokens": float64(5), "cache_read_input_tokens": float64(70), "cache_creation_input_tokens": float64(2)}
	u := chatUsageFromClaude(claude)
	if u.CacheReadInputTokens != 70 || u.InputTokens != 30 || u.CacheCreationInputTokens != 2 {
		t.Fatalf("claude usage: %+v", u)
	}
	gemini := map[string]interface{}{"promptTokenCount": float64(100), "candidatesTokenCount": float64(5), "cachedContentTokenCount": float64(70)}
	v := chatUsageFromGemini(gemini)
	if v.CacheReadInputTokens != 70 || v.PromptTokensTotal != 100 {
		t.Fatalf("gemini usage: %+v", v)
	}
}

func TestChatUsageAliasesAndAccounting(t *testing.T) {
	cases := []struct {
		name                         string
		raw                          map[string]interface{}
		expectedInput, expectedCache int
	}{
		{"openai", map[string]interface{}{"prompt_tokens": float64(100), "completion_tokens": float64(5), "prompt_tokens_details": map[string]interface{}{"cached_tokens": float64(70)}}, 30, 70},
		{"deepseek", map[string]interface{}{"prompt_tokens": float64(100), "prompt_cache_hit_tokens": float64(70)}, 30, 70},
		{"uncached", map[string]interface{}{"prompt_tokens": float64(100), "completion_tokens": float64(5)}, 100, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := chatUsageFromOpenAI(tc.raw)
			miss := u.PromptTokensTotal - u.CacheReadInputTokens
			if miss != tc.expectedInput || u.CacheReadInputTokens != tc.expectedCache {
				t.Fatalf("accounting %+v", u)
			}
		})
	}
}

func TestClaudeCacheSplitAcrossEvents(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var usage *types.Usage
	processClaudeChatStreamLine(c, w, "model", `data: {"type":"message_start","message":{"usage":{"input_tokens":30,"cache_read_input_tokens":70}}}`, &usage, new(bool))
	processClaudeChatStreamLine(c, w, "model", `data: {"type":"message_delta","usage":{"output_tokens":5}}`, &usage, new(bool))
	if usage == nil || usage.InputTokens != 30 || usage.CacheReadInputTokens != 70 || usage.OutputTokens != 5 {
		t.Fatalf("split usage: %+v", usage)
	}
	if !strings.Contains(w.Body.String(), `"cached_tokens":70`) {
		t.Fatal("Claude cache missing in Chat SSE")
	}
}

func TestClaudeToChatNonStreamCacheUsage(t *testing.T) {
	claude := map[string]interface{}{"content": []interface{}{map[string]interface{}{"type": "text", "text": "ok"}}, "usage": map[string]interface{}{"input_tokens": float64(30), "output_tokens": float64(5), "cache_read_input_tokens": float64(70)}}
	got := convertClaudeResponseToChat(claude, "model")
	u, ok := got["usage"].(map[string]interface{})
	if !ok {
		t.Fatal("missing usage")
	}
	if u["prompt_tokens"] != 100 || u["total_tokens"] != 105 {
		t.Fatalf("inclusive tokens: %v", u)
	}
	details, _ := u["prompt_tokens_details"].(map[string]interface{})
	if details["cached_tokens"] != 70 {
		t.Fatalf("cache: %v", u)
	}
}
func TestChatCacheUsageStoredDisjointly(t *testing.T) {
	u := chatUsageFromOpenAI(map[string]interface{}{"prompt_tokens": float64(100), "completion_tokens": float64(5), "prompt_tokens_details": map[string]interface{}{"cached_tokens": float64(70)}})
	// The persisted metrics routine is exercised in its own package; this asserts
	// that Chat passes the inclusive total and separate read bucket to it.
	if u.PromptTokensTotal-u.CacheReadInputTokens != 30 || u.InputTokens != 100 {
		t.Fatalf("unexpected accounting %+v", u)
	}
}

func TestResponsesChatStreamUsageOptIn(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	source := `data: {"type":"response.output_text.delta","delta":"ok"}` + "\n\n" + `data: {"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":5,"input_tokens_details":{"cached_tokens":70}}}}` + "\n\n"
	usage, err := streamResponsesToChat(c, &http.Response{Body: io.NopCloser(strings.NewReader(source))}, w, "model", nil, false, nil, common.StreamPreflightTimeouts{InactivityTimeoutMs: 1000}, common.NewStreamProgressLogger("Test", time.Now(), false))
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.CacheReadInputTokens != 70 {
		t.Fatalf("internal cache missing: %+v", usage)
	}
	if strings.Contains(w.Body.String(), `"cached_tokens"`) {
		t.Fatal("unsolicited Chat usage chunk")
	}
}

func TestResponsesChatIncompletePreservesCacheUsage(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("requestBodyBytes", []byte(`{"stream_options":{"include_usage":true}}`))
	source := `data: {"type":"response.output_text.delta","delta":"ok"}` + "\n\n" + `data: {"type":"response.incomplete","response":{"status":"incomplete","usage":{"input_tokens":100,"output_tokens":5,"input_tokens_details":{"cached_tokens":70}}}}` + "\n\n"
	got, err := streamResponsesToChat(c, &http.Response{Body: io.NopCloser(strings.NewReader(source))}, w, "model", nil, false, nil, common.StreamPreflightTimeouts{InactivityTimeoutMs: 1000}, common.NewStreamProgressLogger("Test", time.Now(), false))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.CacheReadInputTokens != 70 {
		t.Fatalf("incomplete usage: %+v", got)
	}
	if !strings.Contains(w.Body.String(), `"finish_reason":"length"`) || !strings.Contains(w.Body.String(), `"cached_tokens":70`) {
		t.Fatal("client incomplete/cache fields missing")
	}
}

func TestChatResponsesSplitCacheAccounting(t *testing.T) {
	u := chatUsageFromResponses(map[string]interface{}{"input_tokens": float64(30), "output_tokens": float64(5), "cache_read_input_tokens": float64(70)})
	if u.PromptTokensTotal != 0 || u.InputTokens != 30 || u.CacheReadInputTokens != 70 {
		t.Fatalf("split accounting: %+v", u)
	}
	wire := chatWireUsage(u)
	if wire["prompt_tokens"] != 100 || wire["total_tokens"] != 105 {
		t.Fatalf("split wire: %+v", wire)
	}
	v := chatUsageFromResponses(map[string]interface{}{"input_tokens": float64(100), "output_tokens": float64(5), "input_tokens_details": map[string]interface{}{"cached_tokens": float64(70)}})
	if v.PromptTokensTotal != 100 || v.CacheReadInputTokens != 70 {
		t.Fatalf("inclusive accounting: %+v", v)
	}
}

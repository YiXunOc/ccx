package providers

import (
	"encoding/json"
	"github.com/BenedictKing/ccx/internal/types"
	"io"
	"strings"
	"testing"
)

func TestGeminiToMessagesNonStreamCacheUsage(t *testing.T) {
	raw := `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":5,"cachedContentTokenCount":70}}`
	out, err := (&GeminiProvider{}).ConvertToClaudeResponse(&types.ProviderResponse{Body: []byte(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if out.Usage == nil || out.Usage.InputTokens != 30 || out.Usage.CacheReadInputTokens != 70 {
		t.Fatalf("cache usage: %+v", out.Usage)
	}
}
func TestGeminiToMessagesStreamCacheUsage(t *testing.T) {
	raw := `data: {"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}

data: {"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":5,"cachedContentTokenCount":70}}

`
	events, _, err := (&GeminiProvider{}).HandleStreamResponse(io.NopCloser(strings.NewReader(raw)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for e := range events {
		for _, line := range strings.Split(e, "\n") {
			if !strings.HasPrefix(line, "data: {") {
				continue
			}
			var v map[string]interface{}
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &v) != nil || v["type"] != "message_delta" {
				continue
			}
			u, _ := v["usage"].(map[string]interface{})
			if u["input_tokens"] == float64(30) && u["cache_read_input_tokens"] == float64(70) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("cache usage missing in message_delta")
	}
}

func TestGeminiCacheOnlyFollowupChunk(t *testing.T) {
	raw := `data: {"usageMetadata":{"promptTokenCount":100}}` + "\n\n" + `data: {"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}` + "\n\n" + `data: {"usageMetadata":{"cachedContentTokenCount":70,"candidatesTokenCount":5}}` + "\n\n"
	events, _, err := (&GeminiProvider{}).HandleStreamResponse(io.NopCloser(strings.NewReader(raw)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for e := range events {
		for _, line := range strings.Split(e, "\n") {
			if !strings.HasPrefix(line, "data: {") {
				continue
			}
			var v map[string]interface{}
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &v) != nil || v["type"] != "message_delta" {
				continue
			}
			u, _ := v["usage"].(map[string]interface{})
			found = u["input_tokens"] == float64(30) && u["cache_read_input_tokens"] == float64(70)
		}
	}
	if !found {
		t.Fatal("cache-only followup double-subtracted cached tokens")
	}
}

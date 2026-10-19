package converters

import (
	"github.com/tidwall/gjson"
	"strings"
	"testing"
)

func TestResponsesToChatCacheUsage(t *testing.T) {
	raw := []byte(`{"id":"resp","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":100,"output_tokens":5,"input_tokens_details":{"cached_tokens":70}}}`)
	out := ConvertResponsesResponseToChatResponse(raw, "model")
	if got := gjson.GetBytes(out, "usage.prompt_tokens_details.cached_tokens").Int(); got != 70 {
		t.Fatalf("cached=%d", got)
	}
	if got := gjson.GetBytes(out, "usage.prompt_tokens").Int(); got != 100 {
		t.Fatalf("input=%d", got)
	}
}
func TestResponsesToGeminiCacheDoesNotDoubleCount(t *testing.T) {
	raw := map[string]interface{}{"status": "completed", "output": []interface{}{map[string]interface{}{"type": "message", "content": []interface{}{map[string]interface{}{"type": "output_text", "text": "ok"}}}}, "usage": map[string]interface{}{"input_tokens": float64(100), "output_tokens": float64(5), "input_tokens_details": map[string]interface{}{"cached_tokens": float64(70)}}}
	out, err := ResponsesResponseToGemini(raw)
	if err != nil {
		t.Fatal(err)
	}
	if out.UsageMetadata == nil || out.UsageMetadata.PromptTokenCount != 100 || out.UsageMetadata.CachedContentTokenCount != 70 {
		t.Fatalf("usage: %+v", out.UsageMetadata)
	}
}

func TestGeminiCrossProtocolCacheMapping(t *testing.T) {
	claude := map[string]interface{}{"content": []interface{}{map[string]interface{}{"type": "text", "text": "ok"}}, "usage": map[string]interface{}{"input_tokens": float64(30), "output_tokens": float64(5), "cache_read_input_tokens": float64(70)}}
	cg, err := ClaudeResponseToGemini(claude)
	if err != nil {
		t.Fatal(err)
	}
	if cg.UsageMetadata == nil || cg.UsageMetadata.PromptTokenCount != 100 || cg.UsageMetadata.CachedContentTokenCount != 70 {
		t.Fatalf("Claude->Gemini: %+v", cg.UsageMetadata)
	}
	chat := map[string]interface{}{"choices": []interface{}{map[string]interface{}{"message": map[string]interface{}{"content": "ok"}}}, "usage": map[string]interface{}{"prompt_tokens": float64(100), "completion_tokens": float64(5), "prompt_tokens_details": map[string]interface{}{"cached_tokens": float64(70)}}}
	og, err := OpenAIResponseToGemini(chat)
	if err != nil {
		t.Fatal(err)
	}
	if og.UsageMetadata == nil || og.UsageMetadata.PromptTokenCount != 100 || og.UsageMetadata.CachedContentTokenCount != 70 {
		t.Fatalf("Chat->Gemini: %+v", og.UsageMetadata)
	}
}
func TestResponsesToGeminiStreamCacheUsage(t *testing.T) {
	var state any
	lines := []string{`event: response.completed`, `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":100,"output_tokens":5,"input_tokens_details":{"cached_tokens":70}}}}`}
	var out []string
	for _, line := range lines {
		out = append(out, ConvertResponsesToGeminiStream(nil, "model", []byte(line), &state)...)
	}
	found := false
	for _, e := range out {
		root := gjson.Get(e, "usageMetadata.cachedContentTokenCount")
		if !root.Exists() {
			root = gjson.Get(e[6:], "usageMetadata.cachedContentTokenCount")
		}
		if root.Int() == 70 {
			found = true
			if total := gjson.Get(e[6:], "usageMetadata.promptTokenCount").Int(); total != 100 {
				t.Fatalf("double counted: %d", total)
			}
		}
	}
	if !found {
		t.Fatalf("missing cache field in %d chunks", len(out))
	}
}

func TestResponsesToGeminiClaudeSplitCache(t *testing.T) {
	raw := map[string]interface{}{"status": "completed", "output": []interface{}{map[string]interface{}{"type": "message", "content": []interface{}{map[string]interface{}{"type": "output_text", "text": "ok"}}}}, "usage": map[string]interface{}{"input_tokens": float64(30), "output_tokens": float64(5), "cache_read_input_tokens": float64(70)}}
	got, err := ResponsesResponseToGemini(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.UsageMetadata == nil || got.UsageMetadata.PromptTokenCount != 100 || got.UsageMetadata.CachedContentTokenCount != 70 {
		t.Fatalf("split usage: %+v", got.UsageMetadata)
	}
	var state any
	var emitted []string
	for _, line := range []string{`event: response.completed`, `data: {"type":"response.completed","response":{"status":"completed","output":[],"usage":{"input_tokens":30,"output_tokens":5,"cache_read_input_tokens":70}}}`} {
		emitted = append(emitted, ConvertResponsesToGeminiStream(nil, "model", []byte(line), &state)...)
	}
	matched := false
	for _, event := range emitted {
		if !strings.HasPrefix(event, "data: ") {
			continue
		}
		if gjson.Get(event[6:], "usageMetadata.promptTokenCount").Int() == 100 && gjson.Get(event[6:], "usageMetadata.cachedContentTokenCount").Int() == 70 {
			matched = true
		}
	}
	if !matched {
		t.Fatal("stream split cache not mapped")
	}
}

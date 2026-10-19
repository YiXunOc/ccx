package gemini

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/gin-gonic/gin"
)

func TestGeminiNativeStreamCacheMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	input := `data: {"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":5,"cachedContentTokenCount":70}}` + "\n\n"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	chunks := make(chan []byte, 1)
	chunks <- []byte(input)
	close(chunks)
	errors := make(chan error)
	reader := newStreamLineReader(chunks, errors)
	usage, err := streamGeminiToGemini(c, reader, w, nil, false, common.StreamPreflightTimeouts{InactivityTimeoutMs: 1000}, common.NewStreamProgressLogger("Test", time.Now(), false))
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.InputTokens != 30 || usage.CacheReadInputTokens != 70 {
		t.Fatalf("metrics usage: %+v", usage)
	}
	if !strings.Contains(w.Body.String(), `"cachedContentTokenCount":70`) {
		t.Fatal("client cache field missing")
	}
}

func TestClaudeToGeminiSplitCacheUsage(t *testing.T) {
	input := `data: {"type":"message_start","message":{"usage":{"input_tokens":30,"cache_read_input_tokens":70,"cache_creation_input_tokens":2}}}` + "\n\n" + `data: {"type":"message_delta","usage":{"output_tokens":5}}` + "\n\n"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	chunks := make(chan []byte, 1)
	chunks <- []byte(input)
	close(chunks)
	reader := newStreamLineReader(chunks, make(chan error))
	usage, err := streamClaudeToGemini(c, reader, w, "model", nil, false, common.StreamPreflightTimeouts{InactivityTimeoutMs: 1000}, common.NewStreamProgressLogger("Test", time.Now(), false))
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.InputTokens != 30 || usage.CacheReadInputTokens != 70 || usage.CacheCreationInputTokens != 2 || usage.OutputTokens != 5 {
		t.Fatalf("usage: %+v", usage)
	}
	if !strings.Contains(w.Body.String(), `"cachedContentTokenCount":70`) || !strings.Contains(w.Body.String(), `"promptTokenCount":102`) {
		t.Fatal("split cache omitted from Gemini client")
	}
}

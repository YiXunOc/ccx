package chat

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/gin-gonic/gin"
)

func TestToolTracePrivacyAndToggle(t *testing.T) {
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	source := "event: response.output_item.added\r\ndata: " + `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"private-item","call_id":"private-call","name":"private-tool","arguments":"private-args"}}` + "\r\n\r\n"
	var baseline string
	for _, enabled := range []string{"", "1"} {
		t.Setenv("CCX_TOOL_TRACE", enabled)
		logs.Reset()
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		traceToolRoute(c, "responses", "private-model", "private-host")
		resp := &http.Response{Body: io.NopCloser(strings.NewReader(source))}
		_, err := streamResponsesToChat(c, resp, w, "private-model", nil, false, nil, common.StreamPreflightTimeouts{InactivityTimeoutMs: 1000}, common.NewStreamProgressLogger("test", time.Now(), false))
		if err != nil {
			t.Fatal(err)
		}
		// Instrumentation must not silently fix the existing CRLF defect.
		if strings.Contains(w.Body.String(), "tool_calls") {
			t.Fatal("trace changed conversion")
		}
		writeChatSSEChunk(c, w, map[string]interface{}{"choices": []map[string]interface{}{{"delta": map[string]interface{}{"tool_calls": []map[string]interface{}{{"index": 0, "id": "private-call", "function": map[string]interface{}{"name": "private-tool", "arguments": "private-args"}}}}}}})
		if enabled == "" {
			baseline = w.Body.String()
		} else if w.Body.String() != baseline {
			t.Fatal("trace changed output bytes")
		}
		got := logs.String()
		if enabled == "" {
			if strings.Contains(got, "[Chat-ToolTrace]") {
				t.Fatal("trace enabled by default")
			}
			continue
		}
		for _, want := range []string{"[Chat-ToolTrace]", "upstream", "downstream", "crlf", "name_nonempty"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %s", want)
			}
		}
		for _, secret := range []string{"private-item", "private-call", "private-tool", "private-args", "private-model", "private-host"} {
			if strings.Contains(got, secret) {
				t.Errorf("leaked %s", secret)
			}
		}
	}
}

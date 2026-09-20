package chat

import (
	"encoding/json"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Synthetic fixtures, not captured production responses.
func TestResponsesToChatDiagnosticToolName(t *testing.T) {
	for _, tc := range []struct {
		name, initialName string
		crlf              bool
	}{
		{name: "complete_name_at_start", initialName: "terminal"},
		{name: "name_only_at_done"},
		{name: "standard_crlf", initialName: "terminal", crlf: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := `{"command":"pwd"}`
			item := map[string]interface{}{"type": "function_call", "id": "fc_diag", "call_id": "call_diag", "name": tc.initialName, "arguments": ""}
			doneItem := map[string]interface{}{"type": "function_call", "id": "fc_diag", "call_id": "call_diag", "name": "terminal", "arguments": args}
			events := []map[string]interface{}{
				{"type": "response.output_item.added", "output_index": 0, "item": item},
				{"type": "response.function_call_arguments.delta", "output_index": 0, "item_id": "fc_diag", "delta": args},
				{"type": "response.function_call_arguments.done", "output_index": 0, "item_id": "fc_diag", "name": "terminal", "arguments": args},
				{"type": "response.output_item.done", "output_index": 0, "item": doneItem},
				{"type": "response.completed", "response": map[string]interface{}{"status": "completed", "output": []interface{}{doneItem}}},
			}
			var input strings.Builder
			for _, event := range events {
				b, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				input.WriteString("event: " + event["type"].(string) + "\ndata: " + string(b) + "\n\n")
			}
			source := input.String()
			if tc.crlf {
				source = strings.ReplaceAll(source, "\n", "\r\n")
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(source))}
			_, err := streamResponsesToChat(c, resp, w, "diagnostic", nil, false, nil,
				common.StreamPreflightTimeouts{InactivityTimeoutMs: 1000}, common.NewStreamProgressLogger("Diagnostic", time.Now(), false))
			if err != nil {
				t.Fatal(err)
			}
			var name, arguments string
			count := 0
			for _, line := range strings.Split(w.Body.String(), "\n") {
				if !strings.HasPrefix(line, "data: {") {
					continue
				}
				root := gjson.Parse(strings.TrimPrefix(line, "data: "))
				for _, call := range root.Get("choices.0.delta.tool_calls").Array() {
					count++
					name += call.Get("function.name").String()
					arguments += call.Get("function.arguments").String()
				}
			}
			if name != "terminal" || arguments != args {
				t.Fatalf("converted tool name=%q args=%q chunks=%d; want terminal and %s", name, arguments, count, args)
			}
		})
	}
}

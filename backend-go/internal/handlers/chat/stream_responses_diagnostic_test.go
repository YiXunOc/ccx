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

func TestResponsesToChatToolCompletion(t *testing.T) {
	for _, mode := range []string{"item_done", "completed_only", "missing_name", "interleaved"} {
		t.Run(mode, func(t *testing.T) {
			var source strings.Builder
			emit := func(event map[string]interface{}) {
				b, _ := json.Marshal(event)
				source.WriteString("data: " + string(b) + "\n\n")
			}
			item := func(id, name, args string) map[string]interface{} {
				return map[string]interface{}{"type": "function_call", "id": "fc_" + id, "call_id": "call_" + id, "name": name, "arguments": args}
			}
			emit(map[string]interface{}{"type": "response.output_item.added", "output_index": 2, "item": item("a", "", "")})
			if mode == "interleaved" {
				emit(map[string]interface{}{"type": "response.output_item.added", "output_index": 4, "item": item("b", "other", "")})
			}
			emit(map[string]interface{}{"type": "response.function_call_arguments.delta", "output_index": 2, "item_id": "fc_a", "delta": "{"})
			if mode == "interleaved" {
				emit(map[string]interface{}{"type": "response.function_call_arguments.delta", "output_index": 4, "item_id": "fc_b", "delta": "[]"})
			}
			emit(map[string]interface{}{"type": "response.function_call_arguments.delta", "output_index": 2, "item_id": "fc_a", "delta": "}"})
			name := "terminal"
			if mode == "missing_name" {
				name = ""
			}
			a := item("a", name, "{}")
			if mode != "completed_only" {
				emit(map[string]interface{}{"type": "response.output_item.done", "output_index": 2, "item": a})
			}
			output := []interface{}{map[string]interface{}{"type": "message"}, map[string]interface{}{"type": "reasoning"}, a}
			if mode == "interleaved" {
				output = append(output, map[string]interface{}{"type": "message"}, item("b", "other", "[]"))
			}
			emit(map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"output": output}})
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			_, err := streamResponsesToChat(c, &http.Response{Body: io.NopCloser(strings.NewReader(source.String()))}, w, "test", nil, false, nil, common.StreamPreflightTimeouts{InactivityTimeoutMs: 1000}, common.NewStreamProgressLogger("Test", time.Now(), false))
			if mode == "missing_name" {
				if err == nil || strings.Contains(w.Body.String(), "[DONE]") {
					t.Fatal("missing name must fail without successful termination")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			names, args, ids := map[int64]string{}, map[int64]string{}, map[int64]string{}
			for _, line := range strings.Split(w.Body.String(), "\n") {
				for _, call := range gjson.Get(strings.TrimPrefix(line, "data: "), "choices.0.delta.tool_calls").Array() {
					index := call.Get("index").Int()
					names[index] += call.Get("function.name").String()
					ids[index] += call.Get("id").String()
					if names[index] == "" || ids[index] == "" {
						t.Fatal("tool emitted before name and call ID are ready")
					}
					args[index] += call.Get("function.arguments").String()
				}
			}
			if names[0] != "terminal" || ids[0] != "call_a" || args[0] != "{}" {
				t.Fatalf("first tool: %v %v %v", names, ids, args)
			}
			if mode == "interleaved" && (names[1] != "other" || ids[1] != "call_b" || args[1] != "[]") {
				t.Fatalf("second tool: %v %v %v", names, ids, args)
			}
		})
	}
}

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

package converters

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestChatToolCompletionBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		valid      bool
	}{
		{"read", "", false}, {"read", "{}", false}, {"read", "{", false}, {"read", "not json", false},
		{"run", "", true}, {"run", "{}", true}, {"read", `{"path":"x"}`, true}, {"read", "null", true}, {"read", "[]", true},
	} {
		t.Run(tc.name+"/"+tc.args, func(t *testing.T) {
			var state any
			payload, _ := json.Marshal(map[string]interface{}{"id": "chat", "choices": []interface{}{map[string]interface{}{"index": 0, "delta": map[string]interface{}{"tool_calls": []interface{}{map[string]interface{}{"index": 0, "id": "call", "type": "function", "function": map[string]interface{}{"name": tc.name, "arguments": tc.args}}}}}}})
			events := ConvertOpenAIChatToResponses(context.Background(), "m", nil, nil, append([]byte("data: "), payload...), &state)
			events = append(events, ConvertOpenAIChatToResponses(context.Background(), "m", nil, nil, []byte(`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`), &state)...)
			events = append(events, ConvertOpenAIChatToResponses(context.Background(), "m", nil, nil, []byte("data: [DONE]"), &state)...)
			wire := strings.Join(events, "")
			if !tc.valid && (strings.Contains(wire, "response.function_call_arguments.done") || strings.Contains(wire, "response.output_item.done") || strings.Contains(wire, "response.completed")) {
				t.Fatalf("invalid tool emitted successful completion events: %s", wire)
			}
			if !tc.valid && !strings.Contains(wire, "response.incomplete") {
				t.Fatal("missing abnormal terminal")
			}
			if tc.valid && (!strings.Contains(wire, "response.function_call_arguments.done") || !strings.Contains(wire, "response.completed")) {
				t.Fatalf("valid tool rejected: %s", wire)
			}
		})
	}
}

package converters

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// Exercise the public stream converters without an HTTP server or network access.
func TestResponsesReasoningDeltaContract(t *testing.T) {
	type converter func(context.Context, string, []byte, []byte, []byte, *any) []string
	for _, source := range []struct {
		name    string
		convert converter
	}{
		{"chat", ConvertOpenAIChatToResponses},
		{"claude", ConvertClaudeMessagesToResponses},
		{"gemini", ConvertGeminiStreamToResponses},
	} {
		for _, follow := range []string{"reasoning_only", "text", "tool"} {
			t.Run(source.name+"/"+follow, func(t *testing.T) {
				var state any
				var events []string
				for _, line := range reasoningContractInput(source.name, follow) {
					events = append(events, source.convert(context.Background(), "m", []byte(`{"model":"m","input":"hi"}`), nil, []byte("data: "+line), &state)...)
				}
				assertReasoningContract(t, events, follow)
			})
		}
	}
}

func reasoningContractInput(source, follow string) []string {
	switch source {
	case "chat":
		lines := []string{
			`{"id":"contract","choices":[{"delta":{"reasoning_content":"first"}}]}`,
			`{"choices":[{"delta":{"reasoning_content":""}}]}`,
			`{"choices":[{"delta":{"reasoning_content":" second"}}]}`,
		}
		if follow == "text" {
			lines = append(lines, `{"choices":[{"delta":{"content":"answer"}}]}`)
		}
		if follow == "tool" {
			lines = append(lines, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_lookup","function":{"name":"lookup","arguments":"{}"}}]}}]}`)
		}
		return append(lines, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`, "[DONE]")
	case "claude":
		lines := []string{
			`{"type":"message_start","message":{"id":"contract"}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"first"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" second"}}`,
			`{"type":"content_block_stop","index":0}`,
		}
		if follow == "text" {
			lines = append(lines,
				`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":"answer"}}`,
				`{"type":"content_block_stop","index":1}`)
		}
		if follow == "tool" {
			lines = append(lines,
				`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_lookup","name":"lookup","input":{}}}`,
				`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
				`{"type":"content_block_stop","index":1}`)
		}
		return append(lines, `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, `{"type":"message_stop"}`)
	default:
		lines := []string{
			`{"candidates":[{"content":{"parts":[{"thought":true,"text":"first"}]}}]}`,
			`{"candidates":[{"content":{"parts":[{"thought":true,"text":""}]}}]}`,
			`{"candidates":[{"content":{"parts":[{"thought":true,"text":" second"}]}}]}`,
		}
		if follow == "text" {
			lines = append(lines, `{"candidates":[{"content":{"parts":[{"text":"answer"}]}}]}`)
		}
		if follow == "tool" {
			lines = append(lines, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{}}}]}}]}`)
		}
		return append(lines, `{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}]}`)
	}
}

func assertReasoningContract(t *testing.T, events []string, follow string) {
	t.Helper()
	const full = "first second"
	var lifecycle, fragments []string
	id, responseID, text := "", "", ""
	terminalCount := 0
	for i, raw := range events {
		var eventName, payload string
		for _, line := range strings.Split(raw, "\n") {
			if strings.HasPrefix(line, "event: ") {
				eventName = strings.TrimPrefix(line, "event: ")
			}
			if strings.HasPrefix(line, "data: ") {
				payload = strings.TrimPrefix(line, "data: ")
			}
		}
		if !json.Valid([]byte(payload)) {
			t.Fatalf("invalid SSE JSON: %s", raw)
		}
		e := gjson.Parse(payload)
		typ := e.Get("type").String()
		if eventName != typ {
			t.Fatalf("SSE name/type mismatch: %s", raw)
		}
		if seq := e.Get("sequence_number"); seq.Type != gjson.Number || seq.Int() != int64(i+1) {
			t.Fatalf("sequence mismatch: %s", raw)
		}
		if typ == "response.created" {
			responseID = e.Get("response.id").String()
			if responseID == "" {
				t.Fatal("empty response ID")
			}
		}
		if typ == "response.output_item.added" && e.Get("item.type").String() == "reasoning" {
			if id != "" {
				t.Fatalf("duplicate reasoning item: %s", raw)
			}
			id = e.Get("item.id").String()
			if id == "" || e.Get("output_index").Int() != 0 || e.Get("item.status").String() != "in_progress" {
				t.Fatalf("bad reasoning identity: %s", raw)
			}
			lifecycle = append(lifecycle, "item.added")
		}
		if strings.HasPrefix(typ, "response.reasoning_summary_") {
			if id == "" || e.Get("item_id").String() != id || e.Get("output_index").Type != gjson.Number || e.Get("output_index").Int() != 0 || e.Get("summary_index").Type != gjson.Number || e.Get("summary_index").Int() != 0 {
				t.Fatalf("reasoning reference changed: %s", raw)
			}
			lifecycle = append(lifecycle, strings.TrimPrefix(typ, "response.reasoning_summary_"))
			switch typ {
			case "response.reasoning_summary_text.delta":
				delta := e.Get("delta")
				if delta.Type != gjson.String || e.Get("text").Exists() {
					t.Errorf("reasoning delta must have delta:string and no text: %s", payload)
				}
				fragments = append(fragments, delta.String())
			case "response.reasoning_summary_text.done":
				if e.Get("text").Type != gjson.String || e.Get("text").String() != full || e.Get("delta").Exists() {
					t.Errorf("done.text changed: %s", payload)
				}
			case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
				want := ""
				if strings.HasSuffix(typ, ".done") {
					want = full
				}
				if e.Get("part.type").String() != "summary_text" || e.Get("part.text").Type != gjson.String || e.Get("part.text").String() != want || e.Get("part.delta").Exists() {
					t.Errorf("part.text changed: %s", payload)
				}
			}
		}
		if typ == "response.output_item.done" && e.Get("item.type").String() == "reasoning" {
			if e.Get("item.id").String() != id || e.Get("output_index").Int() != 0 || e.Get("item.status").String() != "completed" || e.Get("item.summary.0.type").String() != "summary_text" || e.Get("item.summary.0.text").String() != full {
				t.Errorf("reasoning done changed: %s", payload)
			}
			lifecycle = append(lifecycle, "item.done")
		}
		if typ == "response.output_text.delta" {
			if len(lifecycle) == 0 || lifecycle[len(lifecycle)-1] != "item.done" {
				t.Errorf("body before reasoning closure: %s", payload)
			}
			if e.Get("output_index").Int() != 1 || e.Get("item_id").String() == id {
				t.Errorf("body identity reused: %s", payload)
			}
			text += e.Get("delta").String()
		}
		if typ == "response.completed" {
			terminalCount++
			if i != len(events)-1 || e.Get("response.id").String() != responseID || e.Get("response.status").String() != "completed" {
				t.Errorf("terminal identity/order/status changed: %s", payload)
			}
			output := e.Get("response.output").Array()
			count := 1
			if follow != "reasoning_only" {
				count++
			}
			if len(output) != count {
				t.Fatalf("terminal output count = %d, want %d: %s", len(output), count, payload)
			}
			if output[0].Get("id").String() != id || output[0].Get("summary.0.text").String() != full {
				t.Errorf("terminal reasoning changed: %s", payload)
			}
			if follow == "text" && (output[1].Get("type").String() != "message" || output[1].Get("content.0.text").String() != "answer") {
				t.Errorf("terminal body changed: %s", payload)
			}
			if follow == "tool" {
				tool := output[1]
				// Gemini's existing terminal shape nests function details under content.
				if tool.Get("content").Exists() {
					tool = tool.Get("content")
				}
				if output[1].Get("type").String() != "function_call" || tool.Get("name").String() != "lookup" || tool.Get("arguments").String() != "{}" || tool.Get("call_id").String() == "" {
					t.Errorf("terminal tool changed: %s", payload)
				}
			}
		}
	}
	wantLifecycle := []string{"item.added", "part.added", "text.delta", "text.delta", "text.done", "part.done", "item.done"}
	if !reflect.DeepEqual(lifecycle, wantLifecycle) {
		t.Errorf("lifecycle = %v, want %v", lifecycle, wantLifecycle)
	}
	if !reflect.DeepEqual(fragments, []string{"first", " second"}) {
		t.Errorf("delta fragments = %q", fragments)
	}
	if terminalCount != 1 {
		t.Errorf("terminal count = %d", terminalCount)
	}
	if follow == "text" && text != "answer" {
		t.Errorf("body delta = %q", text)
	}
}

func TestStandardResponsesReasoningEventsUnchanged(t *testing.T) {
	for _, raw := range []string{
		`{"type":"response.reasoning_summary_text.delta","sequence_number":3,"item_id":"rs_1","output_index":1,"summary_index":2,"delta":"first"}`,
		`{"type":"response.reasoning_summary_text.done","item_id":"rs_1","output_index":1,"summary_index":2,"text":"first second"}`,
		`{"type":"response.reasoning_summary_part.done","part":{"type":"summary_text","text":"first second"}}`,
		`{"type":"response.output_item.done","item":{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"first second"}]}}`,
	} {
		event := mustEvent(t, raw)
		before, _ := json.Marshal(event)
		if NormalizeOpenRouterReasoningEvent(event) {
			t.Errorf("standard event reported rewritten: %s", raw)
		}
		after, _ := json.Marshal(event)
		if string(before) != string(after) {
			t.Errorf("standard event mutated: %s", raw)
		}
		line := "data: " + raw
		if got := NormalizeOpenRouterReasoningSSELine(line); got != line {
			t.Errorf("standard SSE changed: %s", got)
		}
	}
}

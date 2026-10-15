package converters

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestIndependentGeminiToolIdentityAndParameters(t *testing.T) {
	for _, separate := range []bool{false, true} {
		name := "same_chunk"
		if separate {
			name = "separate_chunks"
		}
		t.Run(name, func(t *testing.T) {
			params := []string{
				`{"nested":{"flags":[true,false,null],"text":"\u601d\u8003\ud83d\ude00"},"n":1}`,
				`{"n":2,"list":[1,{"x":"quote\" slash\\"}]}`,
				`{"n":3,"empty":{},"items":[]}`,
			}
			names := []string{"lookup", "lookup", "search"}
			var state any
			var events []map[string]any
			feed := func(parts string, finish bool) {
				candidate := map[string]any{"content": map[string]any{"parts": json.RawMessage(parts)}}
				if finish {
					candidate["finishReason"] = "STOP"
				}
				wire, err := json.Marshal(map[string]any{"candidates": []any{candidate}})
				if err != nil {
					t.Fatal(err)
				}
				for _, raw := range ConvertGeminiStreamToResponses(context.Background(), "m", nil, nil, append([]byte("data: "), wire...), &state) {
					for _, line := range strings.Split(raw, "\n") {
						if strings.HasPrefix(line, "data: ") {
							var e map[string]any
							if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil {
								t.Fatal(err)
							}
							events = append(events, e)
						}
					}
				}
			}
			feed("[{\"thought\":true,\"text\":\"independent reason\"},{\"text\":\"prefix \"}]", false)
			var parts []string
			for i, arg := range params {
				p, err := json.Marshal(map[string]any{"functionCall": map[string]any{"name": names[i], "args": json.RawMessage(arg)}})
				if err != nil {
					t.Fatal(err)
				}
				parts = append(parts, string(p))
			}
			if separate {
				for _, p := range parts {
					feed("["+p+"]", false)
				}
			} else {
				feed("["+strings.Join(parts, ",")+"]", false)
			}
			feed("[{\"text\":\"suffix\"}]", true)
			added := map[float64]map[string]any{}
			done := map[float64]map[string]any{}
			phases := map[float64][]string{}
			args := map[float64]string{}
			ids := map[string]bool{}
			var tools []float64
			var terminal []any
			for i, e := range events {
				if e["sequence_number"] != float64(i+1) {
					t.Fatalf("noncontiguous sequence: %v", e)
				}
				typ, _ := e["type"].(string)
				idx, _ := e["output_index"].(float64)
				if typ == "response.output_item.added" {
					item := e["item"].(map[string]any)
					id, _ := item["id"].(string)
					if id == "" || ids[id] {
						t.Fatalf("duplicate/empty identity %v", item)
					}
					ids[id] = true
					added[idx] = item
					if item["type"] == "function_call" {
						tools = append(tools, idx)
						phases[idx] = append(phases[idx], "added")
						if item["arguments"] != "" {
							t.Fatal("nonempty initial arguments")
						}
					}
				}
				if strings.HasPrefix(typ, "response.function_call_arguments.") {
					if added[idx] == nil || e["item_id"] != added[idx]["id"] {
						t.Fatalf("arguments before identity/mismatch: %v", e)
					}
					if strings.HasSuffix(typ, ".delta") {
						phases[idx] = append(phases[idx], "delta")
						args[idx] += e["delta"].(string)
					} else {
						phases[idx] = append(phases[idx], "args_done")
						if e["arguments"] != args[idx] {
							t.Fatal("args done mismatch")
						}
					}
				}
				if typ == "response.output_item.done" {
					item := e["item"].(map[string]any)
					done[idx] = item
					if item["type"] == "function_call" {
						phases[idx] = append(phases[idx], "done")
						if item["id"] != added[idx]["id"] || item["arguments"] != args[idx] {
							t.Fatal("item done mismatch")
						}
					}
				}
				if typ == "response.completed" {
					terminal = e["response"].(map[string]any)["output"].([]any)
				}
			}
			if len(tools) != 3 || len(terminal) != 5 {
				t.Fatalf("lost tools/output: %d/%d", len(tools), len(terminal))
			}
			for i, idx := range tools {
				if idx != float64(i+2) {
					t.Fatalf("wrong tool order/index %v", tools)
				}
				if !reflect.DeepEqual(phases[idx], []string{"added", "delta", "args_done", "done"}) {
					t.Fatalf("bad lifecycle %v", phases[idx])
				}
				item := done[idx]
				if item["name"] != names[i] || item["call_id"] != names[i] {
					t.Fatalf("identity changed %v", item)
				}
				var want, got any
				if err := json.Unmarshal([]byte(params[i]), &want); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(args[idx]), &got); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(want, got) {
					t.Fatalf("parameter loss: want %v got %v", want, got)
				}
			}
			for i, item := range terminal {
				if !reflect.DeepEqual(item, done[float64(i)]) {
					t.Fatalf("terminal order/identity conflict at %d", i)
				}
			}
		})
	}
}

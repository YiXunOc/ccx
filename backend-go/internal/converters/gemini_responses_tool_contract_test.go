package converters

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestGeminiResponsesToolLifecycleContract(t *testing.T) {
	tool := func(n int) string {
		return fmt.Sprintf(
			`{"functionCall":{"name":"lookup","args":{"n":%d}}}`, n)
	}
	for _, tc := range []struct {
		name   string
		chunks []string
		types  []string
		args   []string
	}{
		{"reasoning_tool", []string{`[{"thought":true,"text":"think"}]`, "[" + tool(1) + "]"}, []string{"reasoning", "function_call"}, []string{`{"n":1}`}},
		{"parallel_same_name", []string{"[" + tool(1) + "," + tool(2) + "]"}, []string{"function_call", "function_call"}, []string{`{"n":1}`, `{"n":2}`}},
		{"separate_chunks", []string{"[" + tool(1) + "]", "[" + tool(2) + "]"}, []string{"function_call", "function_call"}, []string{`{"n":1}`, `{"n":2}`}},
		{"text_before_tool", []string{`[{"text":"answer"}]`, "[" + tool(1) + "]"}, []string{"message", "function_call"}, []string{`{"n":1}`}},
		{"text_tool_text", []string{`[{"text":"ans"}]`, "[" + tool(1) + "]", `[{"text":"wer"}]`}, []string{"message", "function_call"}, []string{`{"n":1}`}},
		{"tool_before_text", []string{"[" + tool(1) + "]", `[{"text":"answer"}]`}, []string{"function_call", "message"}, []string{`{"n":1}`}},
	} {
		for _, finish := range []string{"STOP", "MAX_TOKENS", "SAFETY", "EOF"} {
			t.Run(tc.name+"/"+finish, func(t *testing.T) {
				var state any
				var events []gjson.Result
				feed := func(line string) {
					for _, raw := range ConvertGeminiStreamToResponses(context.Background(), "m", nil, nil, []byte("data: "+line), &state) {
						for _, line := range strings.Split(raw, "\n") {
							if strings.HasPrefix(line, "data: ") {
								events = append(events, gjson.Parse(strings.TrimPrefix(line, "data: ")))
							}
						}
					}
				}
				for _, parts := range tc.chunks {
					feed(`{"candidates":[{"content":{"parts":` + parts + `}}]}`)
				}
				if finish != "EOF" {
					feed(fmt.Sprintf(`{"candidates":[{"content":{"parts":[]},"finishReason":%q}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":4,"cachedContentTokenCount":2}}`, finish))
				}
				added := map[int]gjson.Result{}
				done := map[int]gjson.Result{}
				phases := map[int][]string{}
				deltas := map[int]string{}
				ids := map[string]bool{}
				var terminal gjson.Result
				reasoningClosed := false
				for i, e := range events {
					if e.Get("sequence_number").Int() != int64(i+1) {
						t.Errorf("sequence: %s", e.Raw)
					}
					typ := e.Get("type").String()
					index := int(e.Get("output_index").Int())
					if typ == "response.reasoning_summary_text.delta" && (e.Get("delta").String() != "think" || e.Get("text").Exists()) {
						t.Errorf("reasoning delta: %s", e.Raw)
					}
					if typ == "response.output_item.done" && e.Get("item.type").String() == "reasoning" {
						reasoningClosed = true
					}
					if typ == "response.output_item.added" {
						added[index] = e.Get("item")
						if ids[e.Get("item.id").String()] || e.Get("item.id").String() == "" {
							t.Errorf("item identity: %s", e.Raw)
						}
						ids[e.Get("item.id").String()] = true
						if e.Get("item.type").String() == "function_call" {
							if tc.types[0] == "reasoning" && !reasoningClosed {
								t.Error("reasoning must close before tool")
							}
							if e.Get("item.arguments").String() != "" || e.Get("item.status").String() != "in_progress" {
								t.Errorf("added: %s", e.Raw)
							}
							phases[index] = append(phases[index], "added")
						}
					}
					if strings.HasPrefix(typ, "response.function_call_arguments.") {
						if e.Get("item_id").String() != added[index].Get("id").String() {
							t.Errorf("argument identity: %s", e.Raw)
						}
						if strings.HasSuffix(typ, ".delta") {
							deltas[index] += e.Get("delta").String()
							phases[index] = append(phases[index], "delta")
						} else {
							phases[index] = append(phases[index], "arguments_done")
							if e.Get("arguments").String() != deltas[index] {
								t.Errorf("arguments done: %s", e.Raw)
							}
						}
					}
					if typ == "response.output_item.done" {
						done[index] = e.Get("item")
						if e.Get("item.type").String() == "function_call" {
							phases[index] = append(phases[index], "done")
						}
					}
					if typ == "response.completed" {
						if terminal.Exists() {
							t.Error("duplicate terminal")
						}
						terminal = e.Get("response")
					}
				}
				if len(added) != len(tc.types) {
					t.Errorf("added items=%d want=%d; missing tool lifecycle", len(added), len(tc.types))
				}
				toolIndex := 0
				for index, typ := range tc.types {
					if added[index].Get("type").String() != typ {
						t.Errorf("index %d added type=%s want=%s", index, added[index].Get("type").String(), typ)
					}
					if typ != "function_call" {
						continue
					}
					if !reflect.DeepEqual(phases[index], []string{"added", "delta", "arguments_done", "done"}) {
						t.Errorf("tool %d lifecycle=%v", index, phases[index])
					}
					if deltas[index] != tc.args[toolIndex] {
						t.Errorf("tool %d args=%q want=%q", index, deltas[index], tc.args[toolIndex])
					}
					toolIndex++
					if done[index].Get("call_id").String() != "lookup" || done[index].Get("name").String() != "lookup" || done[index].Get("arguments").String() != deltas[index] || done[index].Get("id").String() != added[index].Get("id").String() || done[index].Get("status").String() != "completed" || done[index].Get("content").Exists() {
						t.Errorf("tool done: %s", done[index].Raw)
					}
				}
				if finish == "EOF" {
					if terminal.Exists() {
						t.Error("EOF must not fabricate terminal")
					}
					return
				}
				if terminal.Get("status").String() != map[string]string{"STOP": "completed", "MAX_TOKENS": "incomplete", "SAFETY": "failed"}[finish] {
					t.Errorf("status: %s", terminal.Raw)
				}
				if terminal.Get("usage.input_tokens").Int() != 8 || terminal.Get("usage.output_tokens").Int() != 4 || terminal.Get("usage.input_tokens_details.cached_tokens").Int() != 2 {
					t.Errorf("usage: %s", terminal.Raw)
				}
				output := terminal.Get("output").Array()
				if len(output) != len(tc.types) {
					t.Fatalf("output length=%d want=%d", len(output), len(tc.types))
				}
				for i, typ := range tc.types {
					if output[i].Get("type").String() != typ {
						t.Errorf("snapshot index %d: %s", i, output[i].Raw)
					}
					if typ == "message" && output[i].Get("content.0.text").String() != "answer" {
						t.Errorf("body text: %s", output[i].Raw)
					}
					if !reflect.DeepEqual(output[i].Value(), done[i].Value()) {
						t.Errorf("snapshot != done at %d: %s / %s", i, output[i].Raw, done[i].Raw)
					}
				}
			})
		}
	}
}

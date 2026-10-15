package converters

import (
	"context"
	"fmt"
	"github.com/tidwall/gjson"
	"strings"
	"testing"
)

func TestConvertOpenAIChatToResponses_Stream(t *testing.T) {
	ctx := context.Background()

	// 模拟 OpenAI Chat Completions SSE 流
	sseLines := []string{
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world!"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		`data: [DONE]`,
	}

	originalReq := []byte(`{"model":"gpt-4o","input":"Hi"}`)

	var state any
	var allEvents []string

	for _, line := range sseLines {
		events := ConvertOpenAIChatToResponses(ctx, "gpt-4o", originalReq, nil, []byte(line), &state)
		allEvents = append(allEvents, events...)
	}

	// 验证事件序列
	if len(allEvents) == 0 {
		t.Fatal("should produce events")
	}

	// 检查是否有 response.created 事件
	hasCreated := false
	hasInProgress := false
	hasCompleted := false
	hasTextDelta := false

	for _, ev := range allEvents {
		if strings.Contains(ev, "response.created") {
			hasCreated = true
		}
		if strings.Contains(ev, "response.in_progress") {
			hasInProgress = true
		}
		if strings.Contains(ev, "response.completed") {
			hasCompleted = true
		}
		if strings.Contains(ev, "response.output_text.delta") {
			hasTextDelta = true
		}
	}

	if !hasCreated {
		t.Error("should have response.created event")
	}
	if !hasInProgress {
		t.Error("should have response.in_progress event")
	}
	if !hasCompleted {
		t.Error("should have response.completed event")
	}
	if !hasTextDelta {
		t.Error("should have response.output_text.delta event")
	}
}

func TestConvertOpenAIChatToResponses_StreamReasoningContent(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"chatcmpl-ds","object":"chat.completion.chunk","created":1234567890,"model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"reasoning_content":"chat reasoning"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-ds","object":"chat.completion.chunk","created":1234567890,"model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"content":"chat text"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-ds","object":"chat.completion.chunk","created":1234567890,"model":"deepseek-v4-pro","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}

	var state any
	var allEvents []string
	for _, line := range sseLines {
		events := ConvertOpenAIChatToResponses(ctx, "deepseek-v4-pro", []byte(`{"model":"deepseek-v4-pro","input":"hello"}`), nil, []byte(line), &state)
		allEvents = append(allEvents, events...)
	}

	joined := strings.Join(allEvents, "\n")
	if !strings.Contains(joined, `"type":"reasoning"`) {
		t.Fatalf("expected reasoning item, got %v", allEvents)
	}
	if !strings.Contains(joined, `"text":"chat reasoning"`) {
		t.Fatalf("expected reasoning summary text, got %v", allEvents)
	}
	if !strings.Contains(joined, `"delta":"chat text"`) {
		t.Fatalf("expected text delta after reasoning, got %v", allEvents)
	}
}

func TestConvertOpenAIChatToResponses_StreamVLLMReasoning(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"chatcmpl-vllm","object":"chat.completion.chunk","created":1234567890,"model":"glm-5.2","choices":[{"index":0,"delta":{"reasoning":"vllm reasoning"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-vllm","object":"chat.completion.chunk","created":1234567890,"model":"glm-5.2","choices":[{"index":0,"delta":{"content":"chat text"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-vllm","object":"chat.completion.chunk","created":1234567890,"model":"glm-5.2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}

	var state any
	var allEvents []string
	for _, line := range sseLines {
		events := ConvertOpenAIChatToResponses(ctx, "glm-5.2", []byte(`{"model":"glm-5.2","input":"hello"}`), nil, []byte(line), &state)
		allEvents = append(allEvents, events...)
	}

	joined := strings.Join(allEvents, "\n")
	if !strings.Contains(joined, `"type":"reasoning"`) {
		t.Fatalf("expected reasoning item, got %v", allEvents)
	}
	if !strings.Contains(joined, `"text":"vllm reasoning"`) {
		t.Fatalf("expected reasoning summary text from vLLM reasoning, got %v", allEvents)
	}
	if !strings.Contains(joined, `"delta":"chat text"`) {
		t.Fatalf("expected text delta after reasoning, got %v", allEvents)
	}
}

func TestConvertOpenAIChatToResponses_ToolCall(t *testing.T) {
	ctx := context.Background()

	// 模拟带 tool_call 的 SSE 流
	sseLines := []string{
		`data: {"id":"chatcmpl-456","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-456","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"loc"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-456","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ation\": \"NYC\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-456","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}

	originalReq := []byte(`{"model":"gpt-4o","input":"What's the weather?","tools":[{"name":"get_weather"}]}`)

	var state any
	var allEvents []string

	for _, line := range sseLines {
		events := ConvertOpenAIChatToResponses(ctx, "gpt-4o", originalReq, nil, []byte(line), &state)
		allEvents = append(allEvents, events...)
	}

	// 验证是否有 function_call 相关事件
	hasFuncAdded := false
	hasFuncDelta := false
	hasFuncDone := false

	for _, ev := range allEvents {
		if strings.Contains(ev, "response.output_item.added") && strings.Contains(ev, "function_call") {
			hasFuncAdded = true
		}
		if strings.Contains(ev, "response.function_call_arguments.delta") {
			hasFuncDelta = true
		}
		if strings.Contains(ev, "response.function_call_arguments.done") {
			hasFuncDone = true
		}
	}

	if !hasFuncAdded {
		t.Error("should have function_call output_item.added event")
	}
	if !hasFuncDelta {
		t.Error("should have function_call_arguments.delta event")
	}
	if !hasFuncDone {
		t.Error("should have function_call_arguments.done event")
	}
}

func TestConvertOpenAIChatToResponses_CustomToolCall(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"chatcmpl-custom","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_patch","type":"function","function":{"name":"apply_patch_add_file","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-custom","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"docs/test.md\",\"content\":\"# Test\\n\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-custom","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}
	originalReq := []byte(`{"model":"gpt-4o","input":"edit","tools":[{"type":"custom","name":"apply_patch"}],"transformer_metadata":{"codex_tool_compat_enabled":true}}`)

	var state any
	var allEvents []string
	for _, line := range sseLines {
		allEvents = append(allEvents, ConvertOpenAIChatToResponses(ctx, "gpt-4o", originalReq, nil, []byte(line), &state)...)
	}
	joined := strings.Join(allEvents, "\n")
	if strings.Contains(joined, `"type":"function_call"`) {
		t.Fatalf("custom tool stream leaked function_call events: %s", joined)
	}
	if strings.Contains(joined, "response.function_call_arguments.") {
		t.Fatalf("custom tool stream leaked function argument events: %s", joined)
	}
	if !strings.Contains(joined, `"type":"custom_tool_call"`) {
		t.Fatalf("missing custom_tool_call item: %s", joined)
	}
	if !strings.Contains(joined, "response.custom_tool_call_input.delta") {
		t.Fatalf("missing custom tool input delta: %s", joined)
	}
	if !strings.Contains(joined, "response.custom_tool_call_input.done") {
		t.Fatalf("missing custom tool input done: %s", joined)
	}
	if !strings.Contains(joined, `*** Add File: docs/test.md`) || strings.Contains(joined, `\n+\n*** End Patch`) {
		t.Fatalf("unexpected patch input: %s", joined)
	}
}

func TestConvertOpenAIChatToResponses_ToolSearchRestoresNativeCall(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"chatcmpl-tool-search","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_search","type":"function","function":{"name":"tool_search","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-tool-search","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"query\":\"sub-agent\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-tool-search","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}
	originalReq := []byte(`{"model":"gpt-4o","input":"find tools","tools":[{"type":"tool_search","execution":"client","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}],"transformer_metadata":{"codex_tool_compat_enabled":true}}`)

	var state any
	var allEvents []string
	for _, line := range sseLines {
		allEvents = append(allEvents, ConvertOpenAIChatToResponses(ctx, "gpt-4o", originalReq, nil, []byte(line), &state)...)
	}
	joined := strings.Join(allEvents, "\n")
	if !strings.Contains(joined, `"type":"tool_search_call"`) {
		t.Fatalf("missing native tool_search_call item: %s", joined)
	}
	if strings.Contains(joined, `"type":"custom_tool_call"`) || strings.Contains(joined, `"name":"tool_search"`) {
		t.Fatalf("tool_search should not be returned as custom_tool_call: %s", joined)
	}
	if strings.Contains(joined, "response.function_call_arguments.done") {
		t.Fatalf("tool_search should not be returned as a function_call: %s", joined)
	}
	if strings.Contains(joined, "response.custom_tool_call_input.") {
		t.Fatalf("tool_search should not emit custom tool input events: %s", joined)
	}
	if !strings.Contains(joined, `"execution":"client"`) {
		t.Fatalf("tool_search_call should carry client execution: %s", joined)
	}
	if !strings.Contains(joined, `"query":"sub-agent"`) {
		t.Fatalf("tool_search input should preserve query arguments: %s", joined)
	}
}

func TestConvertOpenAIChatToResponses_SubAgentFunctionsStayFunctionCalls(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"chatcmpl-subagent","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_spawn","type":"function","function":{"name":"spawn_agent","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-subagent","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"agent_type\":\"explorer\",\"message\":\"只读检查仓库\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-subagent","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_wait","type":"function","function":{"name":"wait_agent","arguments":"{\"targets\":[\"agent-1\"],\"timeout_ms\":60000}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-subagent","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}
	originalReq := []byte(`{"model":"gpt-4o","input":"try sub-agent","tools":[{"type":"tool_search","execution":"client","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}},{"type":"function","name":"spawn_agent","parameters":{"type":"object","properties":{"agent_type":{"type":"string"},"message":{"type":"string"}},"required":["agent_type","message"]}},{"type":"function","name":"wait_agent","parameters":{"type":"object","properties":{"targets":{"type":"array","items":{"type":"string"}},"timeout_ms":{"type":"integer"}},"required":["targets"]}}],"transformer_metadata":{"codex_tool_compat_enabled":true}}`)

	var state any
	var allEvents []string
	for _, line := range sseLines {
		allEvents = append(allEvents, ConvertOpenAIChatToResponses(ctx, "gpt-4o", originalReq, nil, []byte(line), &state)...)
	}
	joined := strings.Join(allEvents, "\n")
	if strings.Contains(joined, `"type":"custom_tool_call"`) {
		t.Fatalf("sub-agent function tools should not be remapped to custom_tool_call: %s", joined)
	}
	for _, name := range []string{"spawn_agent", "wait_agent"} {
		if !strings.Contains(joined, `"type":"function_call"`) || !strings.Contains(joined, `"name":"`+name+`"`) {
			t.Fatalf("missing function_call for %s: %s", name, joined)
		}
	}
	if !strings.Contains(joined, `\"agent_type\":\"explorer\"`) {
		t.Fatalf("spawn_agent arguments should be preserved: %s", joined)
	}
	if !strings.Contains(joined, `\"targets\":[\"agent-1\"]`) {
		t.Fatalf("wait_agent arguments should be preserved: %s", joined)
	}
}

func TestConvertOpenAIChatToResponses_Stream_ClaudeCacheTotalTokens(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"msg-claude-cache","object":"chat.completion.chunk","created":1234567890,"model":"claude","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`,
		`data: {"id":"msg-claude-cache","object":"chat.completion.chunk","created":1234567890,"model":"claude","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"input_tokens":100,"output_tokens":20,"cache_creation_input_tokens":10,"cache_read_input_tokens":30}}`,
		`data: [DONE]`,
	}

	originalReq := []byte(`{"model":"claude","input":"hi"}`)
	var state any
	var allEvents []string
	for _, line := range sseLines {
		allEvents = append(allEvents, ConvertOpenAIChatToResponses(ctx, "claude", originalReq, nil, []byte(line), &state)...)
	}

	usage := extractResponseCompletedUsage(t, allEvents)
	if got := int(usage["total_tokens"].(float64)); got != 160 {
		t.Fatalf("total_tokens = %d, want 160", got)
	}
	if got := int(usage["cache_creation_input_tokens"].(float64)); got != 10 {
		t.Fatalf("cache_creation_input_tokens = %d, want 10", got)
	}
	if got := int(usage["cache_read_input_tokens"].(float64)); got != 30 {
		t.Fatalf("cache_read_input_tokens = %d, want 30", got)
	}
}

func TestConvertOpenAIChatToResponses_Stream_OpenAICacheDetailsNormalizesInput(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"chatcmpl-openai-cache","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.5","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-openai-cache","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":38451,"completion_tokens":1275,"total_tokens":39726,"prompt_tokens_details":{"cached_tokens":36608}}}`,
		`data: [DONE]`,
	}

	originalReq := []byte(`{"model":"gpt-5.5","input":"hi"}`)
	var state any
	var allEvents []string
	for _, line := range sseLines {
		allEvents = append(allEvents, ConvertOpenAIChatToResponses(ctx, "gpt-5.5", originalReq, nil, []byte(line), &state)...)
	}

	usage := extractResponseCompletedUsage(t, allEvents)
	if got := int(usage["input_tokens"].(float64)); got != 1843 {
		t.Fatalf("input_tokens = %d, want 1843", got)
	}
	if got := int(usage["total_tokens"].(float64)); got != 39726 {
		t.Fatalf("total_tokens = %d, want 39726", got)
	}
	if _, exists := usage["cache_read_input_tokens"]; exists {
		t.Fatalf("cache_read_input_tokens should not be emitted for OpenAI cache details: %#v", usage)
	}
	details, ok := usage["input_tokens_details"].(map[string]interface{})
	if !ok {
		t.Fatalf("input_tokens_details missing: %#v", usage)
	}
	if got := int(details["cached_tokens"].(float64)); got != 36608 {
		t.Fatalf("cached_tokens = %d, want 36608", got)
	}
}

// additional_tools 提升场景的 chat 承接端到端：originalRequestJSON 经 handlers 层
// 注入后为「提升前 namespace/custom 形态 tools + transformer_metadata 开关置位」，
// 上游 chat 返回扁平名 tool_call（functions__exec），必须还原为 custom_tool_call
// 序列（name=exec、input=原始 JS），且 namespace function 还原 name+namespace。
func TestConvertOpenAIChatToResponses_HoistedAdditionalToolsRemap(t *testing.T) {
	ctx := context.Background()
	sseLines := []string{
		`data: {"id":"chatcmpl-hoist","object":"chat.completion.chunk","created":1234567890,"model":"gpt-6-astra","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_exec","type":"function","function":{"name":"functions__exec","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-hoist","object":"chat.completion.chunk","created":1234567890,"model":"gpt-6-astra","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"input\":\"await tools.exec_command({ cmd: \"cat probe.txt\" })\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-hoist","object":"chat.completion.chunk","created":1234567890,"model":"gpt-6-astra","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_wait","type":"function","function":{"name":"functions__wait","arguments":"{\"cell_id\":\"c1\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-hoist","object":"chat.completion.chunk","created":1234567890,"model":"gpt-6-astra","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}
	// 提升前原始形态（response.go 注入 codex_merged_raw_tools 后的 tools 字段）
	originalReq := []byte(`{"model":"gpt-6-astra","input":"run ls","tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec","format":{"type":"grammar","syntax":"lark","definition":"start: .*"}},{"type":"function","name":"wait","parameters":{"type":"object"}}]}],"transformer_metadata":{"codex_tool_compat_enabled":true}}`)

	var state any
	var allEvents []string
	for _, line := range sseLines {
		allEvents = append(allEvents, ConvertOpenAIChatToResponses(ctx, "gpt-6-astra", originalReq, nil, []byte(line), &state)...)
	}
	joined := strings.Join(allEvents, "\n")

	// functions__exec → custom_tool_call exec，input 还原为原始 JS
	if !strings.Contains(joined, `"type":"custom_tool_call"`) {
		t.Fatalf("missing custom_tool_call: %s", joined)
	}
	if !strings.Contains(joined, `"name":"exec"`) {
		t.Fatalf("custom_tool_call name 应为 exec: %s", joined)
	}
	if !strings.Contains(joined, "await tools.exec_command") {
		t.Fatalf("custom_tool_call input 应为原始 JS: %s", joined)
	}
	if strings.Contains(joined, `"name":"functions__exec"`) {
		t.Fatalf("扁平名 functions__exec 不应泄漏给客户端: %s", joined)
	}

	// functions__wait → function_call wait + namespace=functions
	if !strings.Contains(joined, `"name":"wait"`) || !strings.Contains(joined, `"namespace":"functions"`) {
		t.Fatalf("namespace function 应还原 name+namespace: %s", joined)
	}
	if strings.Contains(joined, `"name":"functions__wait"`) {
		t.Fatalf("扁平名 functions__wait 不应泄漏: %s", joined)
	}
}

func chatIdentityEvent(raw string) gjson.Result {
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "data: ") {
			return gjson.Parse(strings.TrimPrefix(line, "data: "))
		}
	}
	return gjson.Result{}
}

func TestChatMixedOutputSlots(t *testing.T) {
	tool := func(idx int, id, name, args string) string {
		return fmt.Sprintf("{\"tool_calls\":[{\"index\":%d,\"id\":%q,\"function\":{\"name\":%q,\"arguments\":%q}}]}", idx, id, name, args)
	}
	cases := []struct {
		name   string
		deltas []string
	}{
		{"tool_text_args", []string{tool(0, "call", "run", "{"), "{\"content\":\"hello\"}", tool(0, "", "", "}")}},
		{"tool_reasoning_args", []string{tool(0, "call", "run", "{"), "{\"reasoning_content\":\"think\"}", tool(0, "", "", "}")}},
		{"tool_text_reasoning_args", []string{tool(7, "call", "run", "{"), "{\"content\":\"hello\"}", "{\"reasoning_content\":\"think\"}", tool(7, "", "", "}")}},
		{"delayed_identity", []string{tool(7, "", "", "{"), "{\"content\":\"hello\"}", tool(7, "call", "run", "}")}},
		{"reverse_upstream_indexes", []string{tool(7, "c7", "run7", "{"), "{\"reasoning_content\":\"think\"}", tool(2, "c2", "run2", "{}"), "{\"content\":\"hello\"}", tool(7, "", "", "}")}},
		{"repeat_message", []string{tool(0, "call", "run", "{"), "{\"content\":\"hel\"}", tool(0, "", "", "}"), "{\"content\":\"lo\"}"}},
		{"repeat_reasoning", []string{tool(0, "call", "run", "{"), "{\"reasoning_content\":\"th\"}", tool(0, "", "", "}"), "{\"reasoning_content\":\"ink\"}"}},
	}
	for _, compat := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%t/%s", compat, tc.name), func(t *testing.T) {
				req := []byte(fmt.Sprintf("{\"transformer_metadata\":{\"codex_tool_compat_enabled\":%t}}", compat))
				var state any
				events := []string{}
				for _, delta := range tc.deltas {
					line := "data: {\"id\":\"mixed\",\"choices\":[{\"delta\":" + delta + "}]}"
					events = append(events, ConvertOpenAIChatToResponses(context.Background(), "m", req, nil, []byte(line), &state)...)
				}
				for _, line := range []string{"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}", "data: [DONE]", "data: [DONE]"} {
					events = append(events, ConvertOpenAIChatToResponses(context.Background(), "m", req, nil, []byte(line), &state)...)
				}
				slots := map[int]string{}
				byID := map[string]int{}
				added := map[string]gjson.Result{}
				done := map[string]int{}
				args := map[string]string{}
				text, reason := "", ""
				terminals := 0
				lastSeq := int64(0)
				for _, raw := range events {
					e := chatIdentityEvent(raw)
					typ := e.Get("type").String()
					idx := int(e.Get("output_index").Int())
					if seq := e.Get("sequence_number").Int(); seq != lastSeq+1 {
						t.Fatalf("sequence gap: %s", raw)
					} else {
						lastSeq = seq
					}
					if typ == "response.output_item.added" {
						item := e.Get("item")
						id := item.Get("id").String()
						if id == "" || slots[idx] != "" || added[id].Exists() {
							t.Fatalf("slot reused at added: index=%d id=%s previous=%s event=%s", idx, id, slots[idx], raw)
						}
						slots[idx] = id
						byID[id] = idx
						added[id] = item
					}
					if id := e.Get("item_id").String(); id != "" {
						if slot, ok := byID[id]; !ok || slot != idx || done[id] != 0 {
							t.Fatalf("item_id reference moved/closed: id=%s slot=%d expected=%d event=%s", id, idx, slot, raw)
						}
					}
					switch typ {
					case "response.function_call_arguments.delta":
						args[e.Get("item_id").String()] += e.Get("delta").String()
					case "response.output_text.delta":
						text += e.Get("delta").String()
					case "response.reasoning_summary_text.delta":
						reason += e.Get("delta").String()
					case "response.output_item.done":
						item := e.Get("item")
						id := item.Get("id").String()
						slot, ok := byID[id]
						if !ok || slot != idx || done[id] != 0 || added[id].Get("type").String() != item.Get("type").String() {
							t.Fatalf("done moved/repeated: %s", raw)
						}
						done[id]++
						if item.Get("type").String() == "function_call" && (args[id] != "{}" || item.Get("arguments").String() != args[id] || added[id].Get("call_id").String() != item.Get("call_id").String() || added[id].Get("name").String() != item.Get("name").String()) {
							t.Fatalf("tool arguments/identity changed: %s args=%v", raw, args)
						}
					case "response.completed", "response.incomplete":
						terminals++
						if typ != "response.completed" {
							t.Fatalf("valid mixed stream rejected: %s", raw)
						}
						items := e.Get("response.output").Array()
						if len(items) != len(slots) {
							t.Fatalf("terminal lost allocated slot: %s slots=%v", raw, slots)
						}
						for i, item := range items {
							id := item.Get("id").String()
							if slots[i] != id || done[id] != 1 {
								t.Fatalf("terminal output position mismatch: i=%d id=%s slotID=%s terminal=%s", i, id, slots[i], raw)
							}
						}
					}
				}
				if terminals != 1 || len(done) != len(added) {
					t.Fatalf("terminal/lifecycle count: terminals=%d done=%v added=%v", terminals, done, added)
				}
				wantText, wantReason := "", ""
				for _, delta := range tc.deltas {
					d := gjson.Parse(delta)
					wantText += d.Get("content").String()
					wantReason += d.Get("reasoning_content").String()
				}
				if text != wantText || reason != wantReason {
					t.Fatalf("text/reasoning replayed: text=%q want=%q reason=%q want=%q", text, wantText, reason, wantReason)
				}
			})
		}
	}
}

func TestChatMixedCodexOutputSlots(t *testing.T) {
	cases := []struct{ name, upstream, args, nativeType, nativeName, input string }{
		{"custom", "functions__exec", "{\"input\":\"hello\"}", "custom_tool_call", "exec", "hello"},
		{"namespace", "functions__wait", "{}", "function_call", "wait", ""},
		{"search", "tool_search", "{\"query\":\"q\"}", "tool_search_call", "", ""},
		{"subagent", "spawn_agent", "{}", "function_call", "spawn_agent", ""},
	}
	for _, compat := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%t/%s", compat, tc.name), func(t *testing.T) {
				req := []byte(fmt.Sprintf("{\"tools\":[{\"type\":\"namespace\",\"name\":\"functions\",\"tools\":[{\"type\":\"custom\",\"name\":\"exec\",\"format\":{\"type\":\"text\"}},{\"type\":\"function\",\"name\":\"wait\",\"parameters\":{\"type\":\"object\"}}]},{\"type\":\"tool_search\",\"execution\":\"client\",\"parameters\":{\"type\":\"object\"}},{\"type\":\"function\",\"name\":\"spawn_agent\",\"parameters\":{\"type\":\"object\"}}],\"transformer_metadata\":{\"codex_tool_compat_enabled\":%t}}", compat))
				call := func(first bool, args string) string {
					id, name := "", ""
					if first {
						id, name = "call", tc.upstream
					}
					return fmt.Sprintf("data: {\"id\":\"mixed\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":7,\"id\":%q,\"function\":{\"name\":%q,\"arguments\":%q}}]}}]}", id, name, args)
				}
				var state any
				events := []string{}
				mid := len(tc.args) / 2
				for _, line := range []string{call(true, tc.args[:mid]), "data: {\"choices\":[{\"delta\":{\"content\":\"visible\"}}]}", "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"}}]}", call(false, tc.args[mid:]), "data: [DONE]"} {
					events = append(events, ConvertOpenAIChatToResponses(context.Background(), "m", req, nil, []byte(line), &state)...)
				}
				slots := map[int]string{}
				indices := map[string]int{}
				done := map[string]gjson.Result{}
				var terminal gjson.Result
				deltas := map[string]string{}
				seq := int64(0)
				for _, raw := range events {
					e := chatIdentityEvent(raw)
					if e.Get("sequence_number").Int() != seq+1 {
						t.Fatalf("sequence gap: %s", raw)
					}
					seq++
					typ := e.Get("type").String()
					idx := int(e.Get("output_index").Int())
					if typ == "response.output_item.added" {
						id := e.Get("item.id").String()
						if slots[idx] != "" {
							t.Fatalf("slot collision: %s", raw)
						}
						slots[idx] = id
						indices[id] = idx
					}
					if id := e.Get("item_id").String(); id != "" {
						if slot, ok := indices[id]; !ok || slot != idx {
							t.Fatalf("reference drift: %s", raw)
						}
					}
					if typ == "response.output_item.done" {
						id := e.Get("item.id").String()
						if indices[id] != idx || done[id].Exists() {
							t.Fatalf("done drift/repeated: %s", raw)
						}
						done[id] = e.Get("item")
					}
					if typ == "response.function_call_arguments.delta" || typ == "response.custom_tool_call_input.delta" {
						deltas[e.Get("item_id").String()] += e.Get("delta").String()
					}
					if typ == "response.completed" {
						terminal = e
					}
					if typ == "response.incomplete" {
						t.Fatalf("valid Codex stream rejected: %s", raw)
					}
				}
				items := terminal.Get("response.output").Array()
				if len(items) != 3 || len(done) != 3 {
					t.Fatalf("missing mixed output: %s", terminal.Raw)
				}
				for idx, item := range items {
					id := item.Get("id").String()
					if slots[idx] != id || !done[id].Exists() || done[id].Get("type").String() != item.Get("type").String() {
						t.Fatalf("terminal position/type mismatch: %s", terminal.Raw)
					}
				}
				native := items[0]
				wantType, wantName := "function_call", tc.upstream
				if compat {
					wantType, wantName = tc.nativeType, tc.nativeName
				}
				if native.Get("type").String() != wantType || native.Get("name").String() != wantName || native.Get("call_id").String() != "call" {
					t.Fatalf("tool restore changed: %s", native.Raw)
				}
				id := native.Get("id").String()
				if wantType == "custom_tool_call" {
					if native.Get("input").String() != tc.input || deltas[id] != tc.input {
						t.Fatalf("custom input lost/replayed: %s delta=%v", native.Raw, deltas)
					}
				} else if wantType == "tool_search_call" {
					if native.Get("arguments.query").String() != "q" || native.Get("execution").String() != "client" || deltas[id] != "" {
						t.Fatalf("search semantics changed: %s", native.Raw)
					}
				} else {
					if native.Get("arguments").String() != tc.args || deltas[id] != tc.args {
						t.Fatalf("function args lost/replayed: %s delta=%v", native.Raw, deltas)
					}
				}
				if compat && tc.name == "namespace" && native.Get("namespace").String() != "functions" {
					t.Fatalf("namespace lost: %s", native.Raw)
				}
			})
		}
	}
}

func TestChatIncompleteDeliveredOutputSnapshots(t *testing.T) {
	for _, compat := range []bool{false, true} {
		for _, closed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%t/closed_%t", compat, closed), func(t *testing.T) {
				req := []byte(fmt.Sprintf("{\"transformer_metadata\":{\"codex_tool_compat_enabled\":%t}}", compat))
				var state any
				events := []string{}
				args := "{"
				if closed {
					args = "{}"
				}
				lines := []string{fmt.Sprintf("data: {\"id\":\"snapshot\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"function\":{\"name\":\"run\",\"arguments\":%q}}]}}]}", args)}
				if closed {
					lines = append(lines, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}")
				}
				lines = append(lines, "data: {\"choices\":[{\"delta\":{\"content\":\"visible\"}}]}", "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"evil\",\"function\":{\"name\":\"changed\",\"arguments\":\"UNSENT\"}}]}}]}", "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3,\"total_tokens\":15}}", "data: [DONE]", "data: [DONE]")
				for _, line := range lines {
					events = append(events, ConvertOpenAIChatToResponses(context.Background(), "m", req, nil, []byte(line), &state)...)
				}
				indices := map[string]int{}
				done := 0
				sentArgs := ""
				terminals := 0
				var terminal gjson.Result
				for seq, raw := range events {
					e := chatIdentityEvent(raw)
					typ := e.Get("type").String()
					if e.Get("sequence_number").Int() != int64(seq+1) {
						t.Fatal("sequence gap")
					}
					idx := int(e.Get("output_index").Int())
					if typ == "response.output_item.added" {
						indices[e.Get("item.id").String()] = idx
					}
					if id := e.Get("item_id").String(); id != "" {
						if slot, ok := indices[id]; !ok || slot != idx {
							t.Fatalf("reference drift: %s", raw)
						}
					}
					if typ == "response.function_call_arguments.delta" {
						sentArgs += e.Get("delta").String()
					}
					if typ == "response.output_item.done" && e.Get("item.type").String() == "function_call" {
						done++
					}
					if typ == "response.function_call_arguments.done" && !closed {
						t.Fatalf("fake argument done: %s", raw)
					}
					if typ == "response.completed" || typ == "response.incomplete" {
						terminals++
						terminal = e
					}
				}
				items := terminal.Get("response.output").Array()
				if terminals != 1 || terminal.Get("type").String() != "response.incomplete" || len(items) != 2 {
					t.Fatalf("missing unique abnormal slots: %s terminals=%d", terminal.Raw, terminals)
				}
				wantStatus := "incomplete"
				wantDone := 0
				if closed {
					wantStatus = "completed"
					wantDone = 1
				}
				tool := items[0]
				msg := items[1]
				if tool.Get("id").String() != "fc_call" || tool.Get("call_id").String() != "call" || tool.Get("name").String() != "run" || tool.Get("status").String() != wantStatus || tool.Get("arguments").String() != args || sentArgs != args || done != wantDone {
					t.Fatalf("snapshot adopted unsent identity/args or fake done: %s sent=%q done=%d", terminal.Raw, sentArgs, done)
				}
				if indices["fc_call"] != 0 || indices[msg.Get("id").String()] != 1 || msg.Get("content.0.text").String() != "visible" {
					t.Fatalf("valid text slot lost: %s indices=%v", terminal.Raw, indices)
				}
				if terminal.Get("response.usage.input_tokens").Int() != 12 || terminal.Get("response.usage.output_tokens").Int() != 3 || terminal.Get("response.usage.total_tokens").Int() != 15 {
					t.Fatalf("usage changed: %s", terminal.Raw)
				}
				if len(ConvertOpenAIChatToResponses(context.Background(), "m", req, nil, []byte("data: {\"choices\":[{\"delta\":{\"content\":\"AFTER\"}}]}"), &state)) != 0 {
					t.Fatal("content leaked after terminal")
				}
			})
		}
	}
}

func TestChatIncompletePendingToolSnapshots(t *testing.T) {
	cases := []struct {
		name, upstream, args, typ string
		compat                    bool
	}{
		{"function_off", "run", "{", "function_call", false}, {"function_on", "run", "{", "function_call", true},
		{"custom", "functions__exec", "{\"input\":\"PRIVATE\"}", "custom_tool_call", true},
		{"search", "tool_search", "{\"query\":\"PRIVATE\"}", "tool_search_call", true},
		{"namespace", "functions__wait", "{", "function_call", true},
	}
	for _, tc := range cases {
		for _, ending := range []string{"identity_conflict", "error", "eof"} {
			t.Run(tc.name+"/"+ending, func(t *testing.T) {
				req := []byte(fmt.Sprintf("{\"tools\":[{\"type\":\"namespace\",\"name\":\"functions\",\"tools\":[{\"type\":\"custom\",\"name\":\"exec\",\"format\":{\"type\":\"text\"}},{\"type\":\"function\",\"name\":\"wait\",\"parameters\":{\"type\":\"object\"}}]},{\"type\":\"tool_search\",\"execution\":\"client\",\"parameters\":{\"type\":\"object\"}}],\"transformer_metadata\":{\"codex_tool_compat_enabled\":%t}}", tc.compat))
				var state any
				events := []string{}
				lines := []string{fmt.Sprintf("data: {\"id\":\"pending\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":7,\"id\":\"call\",\"function\":{\"name\":%q,\"arguments\":%q}}]}}]}", tc.upstream, tc.args), "data: {\"choices\":[{\"delta\":{\"content\":\"visible\"}}]}", "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3,\"total_tokens\":15}}"}
				if ending == "identity_conflict" {
					lines = append(lines, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":7,\"function\":{\"name\":\"CHANGED\",\"arguments\":\"UNSENT\"}}]}}]}", "data: [DONE]")
				} else if ending == "error" {
					lines = append(lines, "data: {\"error\":{\"message\":\"bad\"}}")
				}
				for _, line := range lines {
					events = append(events, ConvertOpenAIChatToResponses(context.Background(), "m", req, nil, []byte(line), &state)...)
				}
				if ending == "eof" {
					events = append(events, FinalizeChatResponsesIncomplete(req, &state, "upstream_eof")...)
				}
				added := map[int]gjson.Result{}
				sent := ""
				terminals := 0
				var last gjson.Result
				for _, raw := range events {
					e := chatIdentityEvent(raw)
					typ := e.Get("type").String()
					idx := int(e.Get("output_index").Int())
					if typ == "response.output_item.added" {
						added[idx] = e.Get("item")
					}
					if strings.HasSuffix(typ, ".delta") && idx == 0 {
						sent += e.Get("delta").String()
					}
					if idx == 0 && (strings.HasSuffix(typ, ".done")) {
						t.Fatalf("unfinished tool received fake done: %s", raw)
					}
					if typ == "response.completed" || typ == "response.incomplete" {
						terminals++
						last = e
					}
				}
				output := last.Get("response.output").Array()
				if terminals != 1 || last.Get("type").String() != "response.incomplete" || len(output) != 2 {
					t.Fatalf("abnormal slot output lost: %s", last.Raw)
				}
				tool := output[0]
				if tool.Get("type").String() != tc.typ || tool.Get("status").String() != "incomplete" || tool.Get("id").String() != added[0].Get("id").String() || tool.Get("call_id").String() != added[0].Get("call_id").String() || tool.Get("name").String() != added[0].Get("name").String() {
					t.Fatalf("snapshot identity/status changed: %s added=%s", last.Raw, added[0].Raw)
				}
				if tc.typ == "function_call" && (tool.Get("arguments").String() != sent || sent != tc.args) {
					t.Fatalf("unsent args adopted: %s sent=%q", tool.Raw, sent)
				}
				if tc.typ == "custom_tool_call" && (tool.Get("input").String() != "" || sent != "" || strings.Contains(tool.Raw, "PRIVATE")) {
					t.Fatalf("unsent custom input leaked: %s", tool.Raw)
				}
				if tc.typ == "tool_search_call" && (len(tool.Get("arguments").Map()) != 0 || sent != "" || strings.Contains(tool.Raw, "PRIVATE")) {
					t.Fatalf("unsent search args leaked: %s", tool.Raw)
				}
				if output[1].Get("id").String() != added[1].Get("id").String() || output[1].Get("content.0.text").String() != "visible" {
					t.Fatalf("visible text slot lost: %s", last.Raw)
				}
				if len(FinalizeChatResponsesIncomplete(req, &state, "upstream_error")) != 0 {
					t.Fatal("duplicate terminal")
				}
			})
		}
	}
}

func TestChatToolIdentityLifecycle(t *testing.T) {
	cases := []struct {
		name    string
		calls   []string
		invalid bool
	}{
		{"same_chunk", []string{`{"index":0,"id":"c0","function":{"name":"run","arguments":"{}"}}`}, false},
		{"empty_args", []string{`{"index":0,"id":"c0","function":{"name":"run"}}`}, false},
		{"late_id_only", []string{`{"index":0,"function":{"name":"run","arguments":"{}"}}`, `{"index":0,"id":"c0"}`}, false},
		{"conflicting_name", []string{`{"index":0,"id":"c0","function":{"name":"run","arguments":"{"}}`, `{"index":0,"function":{"name":"changed","arguments":"}"}}`}, true},
		{"id_first", []string{`{"index":0,"id":"c0","function":{"arguments":"{"}}`, `{"index":0,"function":{"name":"run","arguments":"}"}}`}, false},
		{"name_first", []string{`{"index":0,"function":{"name":"run","arguments":"{"}}`, `{"index":0,"id":"c0","function":{"arguments":"}"}}`}, false},
		{"args_first", []string{`{"index":0,"function":{"arguments":"{"}}`, `{"index":0,"id":"c0"}`, `{"index":0,"function":{"name":"run","arguments":"}"}}`}, false},
		{"repeat_id", []string{`{"index":0,"id":"c0","function":{"name":"run","arguments":"{"}}`, `{"index":0,"id":"c0","function":{"name":"run","arguments":"}"}}`}, false},
		{"interleaved", []string{`{"index":0,"id":"c0","function":{"arguments":"{"}}`, `{"index":1,"function":{"name":"run1","arguments":"{"}}`, `{"index":0,"function":{"name":"run","arguments":"}"}}`, `{"index":1,"id":"c1","function":{"arguments":"}"}}`}, false},
		{"missing_name", []string{`{"index":0,"id":"c0","function":{"arguments":"{}"}}`}, true},
		{"missing_id", []string{`{"index":0,"function":{"name":"run","arguments":"{}"}}`}, true},
		{"conflicting_id", []string{`{"index":0,"id":"c0","function":{"name":"run","arguments":"{"}}`, `{"index":0,"id":"changed","function":{"arguments":"}"}}`}, true},
		{"duplicate_id", []string{`{"index":0,"id":"c0","function":{"name":"run","arguments":"{}"}}`, `{"index":1,"id":"c0","function":{"name":"run1","arguments":"{}"}}`}, true},
	}
	for _, compat := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%t/%s", compat, tc.name), func(t *testing.T) {
				req := []byte(fmt.Sprintf(`{"transformer_metadata":{"codex_tool_compat_enabled":%t}}`, compat))
				var state any
				var events []string
				for _, call := range tc.calls {
					line := `data: {"id":"chat","choices":[{"delta":{"tool_calls":[` + call + `]}}]}`
					events = append(events, ConvertOpenAIChatToResponses(context.Background(), "m", req, nil, []byte(line), &state)...)
				}
				for _, line := range []string{`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}`, "data: [DONE]", "data: [DONE]"} {
					events = append(events, ConvertOpenAIChatToResponses(context.Background(), "m", req, nil, []byte(line), &state)...)
				}
				added := map[string]gjson.Result{}
				args := map[string]string{}
				done := map[string]bool{}
				terminals := 0
				previousSeq := int64(0)
				for _, event := range events {
					e := chatIdentityEvent(event)
					if seq := e.Get("sequence_number").Int(); seq != previousSeq+1 {
						t.Fatalf("non-contiguous sequence: %s", event)
					} else {
						previousSeq = seq
					}
					typ := e.Get("type").String()
					id := e.Get("item_id").String()
					switch typ {
					case "response.output_item.added":
						item := e.Get("item")
						if item.Get("type").String() != "function_call" {
							continue
						}
						id = item.Get("id").String()
						if strings.TrimSpace(item.Get("name").String()) == "" || item.Get("call_id").String() == "" || added[id].Exists() {
							t.Fatalf("invalid or repeated added: %s", event)
						}
						added[id] = item
					case "response.function_call_arguments.delta":
						if !added[id].Exists() || done[id] {
							t.Fatalf("delta outside lifecycle: %s", event)
						}
						args[id] += e.Get("delta").String()
					case "response.function_call_arguments.done":
						if !added[id].Exists() || done[id] || args[id] != e.Get("arguments").String() {
							t.Fatalf("arguments missing/replayed: %s args=%q", event, args[id])
						}
						done[id] = true
					case "response.output_item.done":
						item := e.Get("item")
						if item.Get("type").String() != "function_call" {
							continue
						}
						id = item.Get("id").String()
						if !done[id] || item.Get("name").String() != added[id].Get("name").String() || item.Get("call_id").String() != added[id].Get("call_id").String() {
							t.Fatalf("identity drift: %s", event)
						}
					case "response.completed", "response.incomplete":
						terminals++
						if (typ == "response.incomplete") != tc.invalid {
							t.Fatalf("wrong terminal: %s", event)
						}
						if e.Get("response.usage.input_tokens").Int() != 12 || e.Get("response.usage.output_tokens").Int() != 3 {
							t.Fatalf("usage lost: %s", event)
						}
						for _, item := range e.Get("response.output").Array() {
							if item.Get("type").String() == "function_call" {
								id := item.Get("id").String()
								pending := typ == "response.incomplete" && item.Get("status").String() == "incomplete" && added[id].Exists() && item.Get("name").String() == added[id].Get("name").String() && item.Get("call_id").String() == added[id].Get("call_id").String() && item.Get("arguments").String() == args[id]
								if item.Get("name").String() == "" || (!done[id] && !pending) {
									t.Fatalf("invalid terminal tool: %s", event)
								}
							}
						}
					}
				}
				if terminals != 1 {
					t.Fatalf("terminal count=%d", terminals)
				}
				if !tc.invalid && len(done) != len(added) {
					t.Fatal("unclosed tools")
				}
			})
		}
	}
}

func TestChatToolErrorTerminal(t *testing.T) {
	var state any
	var events []string
	for _, line := range []string{`data: {"id":"chat","choices":[{"delta":{"content":"visible"}}]}`, `data: {"error":{"message":"upstream failure"},"usage":{"prompt_tokens":12,"completion_tokens":3}}`, `data: {"choices":[{"delta":{"content":"must not leak"}}]}`, "data: [DONE]"} {
		events = append(events, ConvertOpenAIChatToResponses(context.Background(), "m", nil, nil, []byte(line), &state)...)
	}
	joined := strings.Join(events, "\n")
	if strings.Contains(joined, "must not leak") || strings.Contains(joined, "response.completed") || strings.Count(joined, `"type":"response.incomplete"`) != 1 {
		t.Fatalf("error did not end stream: %s", joined)
	}
}

func TestChatToolDelayedCodexIdentity(t *testing.T) {
	req := []byte(`{"tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec","format":{"type":"text"}},{"type":"function","name":"wait","parameters":{"type":"object"}}]}],"transformer_metadata":{"codex_tool_compat_enabled":true}}`)
	var state any
	var events []string
	for _, line := range []string{`data: {"id":"chat","choices":[{"delta":{"reasoning_content":"reason"}}]}`, `data: {"choices":[{"delta":{"content":"visible"}}]}`, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"input\":\"hello\"}"}},{"index":1,"function":{"arguments":"{}"}}]}}]}`, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c0","function":{"name":"functions__exec"}},{"index":1,"id":"c1","function":{"name":"functions__wait"}}]}}]}`, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`, "data: [DONE]"} {
		events = append(events, ConvertOpenAIChatToResponses(context.Background(), "m", req, nil, []byte(line), &state)...)
	}
	added := map[string]int64{}
	deltas := map[string]string{}
	var terminal gjson.Result
	for _, event := range events {
		e := chatIdentityEvent(event)
		typ := e.Get("type").String()
		item := e.Get("item")
		switch typ {
		case "response.output_item.added":
			id := item.Get("id").String()
			added[id] = e.Get("output_index").Int()
			if (item.Get("type").String() == "function_call" || item.Get("type").String() == "custom_tool_call") && item.Get("name").String() == "" {
				t.Fatalf("empty name: %s", event)
			}
		case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
			id := e.Get("item_id").String()
			idx, ok := added[id]
			if !ok || idx != e.Get("output_index").Int() {
				t.Fatalf("delta precedes added or index changed: %s", event)
			}
			deltas[id] += e.Get("delta").String()
		case "response.completed":
			terminal = e
		}
	}
	if deltas["ctc_c0"] != "hello" || deltas["fc_c1"] != "{}" || len(terminal.Get("response.output").Array()) != 4 {
		t.Fatalf("missing/replayed Codex output: %v terminal=%s", deltas, terminal.Raw)
	}
	if terminal.Get("response.output.2.name").String() != "exec" || terminal.Get("response.output.2.type").String() != "custom_tool_call" || terminal.Get("response.output.3.namespace").String() != "functions" {
		t.Fatalf("Codex remap lost: %s", terminal.Raw)
	}
}

func TestChatToolIncompleteWithoutUsage(t *testing.T) {
	var state any
	var events []string
	for _, line := range []string{`data: {"id":"chat","choices":[{"delta":{"tool_calls":[{"index":0,"id":"bad"}]}}]}`, "data: [DONE]"} {
		events = append(events, ConvertOpenAIChatToResponses(context.Background(), "m", nil, nil, []byte(line), &state)...)
	}
	for _, event := range events {
		e := chatIdentityEvent(event)
		if e.Get("type").String() == "response.incomplete" && e.Get("response.usage").Exists() {
			t.Fatalf("unknown usage fabricated: %s", event)
		}
	}
}

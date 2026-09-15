package types

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseResponsesInput_NormalizesLegacyResponsesItemsSlice(t *testing.T) {
	items, err := ParseResponsesInput([]ResponsesItem{
		{
			Type: "tool_call",
			ToolUse: &ToolUse{
				ID:   "toolu_1",
				Name: "get_weather",
				Input: map[string]interface{}{
					"location": "NYC",
				},
			},
		},
		{
			Type: "tool_result",
			Content: map[string]interface{}{
				"tool_use_id": "toolu_1",
				"content":     map[string]interface{}{"temperature": 72},
			},
		},
	})
	if err != nil {
		t.Fatalf("ParseResponsesInput failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Type != "function_call" || items[0].CallID != "toolu_1" || items[0].Name != "get_weather" {
		t.Fatalf("legacy tool_call not normalized: %#v", items[0])
	}
	if items[0].Arguments == "" {
		t.Fatalf("legacy tool_call arguments not preserved: %#v", items[0])
	}
	if items[1].Type != "function_call_output" || items[1].CallID != "toolu_1" {
		t.Fatalf("legacy tool_result not normalized: %#v", items[1])
	}
	outputMap, ok := items[1].Output.(map[string]interface{})
	if !ok || outputMap["temperature"] != 72 {
		t.Fatalf("legacy tool_result output not preserved: %#v", items[1].Output)
	}
}

func TestParseResponsesInput_PreservesCustomToolCallInput(t *testing.T) {
	items, err := ParseResponsesInput([]interface{}{
		map[string]interface{}{
			"type":    "custom_tool_call",
			"call_id": "call_1",
			"name":    "apply_patch",
			"input":   "*** Begin Patch\n*** End Patch",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d", len(items))
	}
	if items[0].Input != "*** Begin Patch\n*** End Patch" {
		t.Fatalf("input = %q", items[0].Input)
	}
}

func TestParseResponsesInput_InfersMessageTypeFromRole(t *testing.T) {
	items, err := ParseResponsesInput([]interface{}{
		map[string]interface{}{
			"role":    "user",
			"content": "Who are you?",
		},
	})
	if err != nil {
		t.Fatalf("ParseResponsesInput failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Type != "message" || items[0].Role != "user" || items[0].Content != "Who are you?" {
		t.Fatalf("message type not inferred: %#v", items[0])
	}
}

func TestParseResponsesInput_PreservesCompactionEncryptedContent(t *testing.T) {
	items, err := ParseResponsesInput([]interface{}{
		map[string]interface{}{
			"type":              "compaction",
			"encrypted_content": "summary payload",
		},
	})
	if err != nil {
		t.Fatalf("ParseResponsesInput failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Type != "compaction" || items[0].EncryptedContent != "summary payload" {
		t.Fatalf("compaction encrypted_content not preserved: %#v", items[0])
	}
}

func TestParseResponsesInput_PreservesToolSearchOutputTools(t *testing.T) {
	items, err := ParseResponsesInput([]interface{}{
		map[string]interface{}{
			"type":      "tool_search_output",
			"execution": "client",
			"tools": []interface{}{
				map[string]interface{}{
					"type": "namespace",
					"name": "multi_agent_v1",
					"tools": []interface{}{
						map[string]interface{}{
							"type": "function",
							"name": "spawn_agent",
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ParseResponsesInput failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Type != "tool_search_output" || items[0].Execution != "client" {
		t.Fatalf("tool_search_output metadata not preserved: %#v", items[0])
	}
	if len(items[0].Tools) != 1 {
		t.Fatalf("tool_search_output tools not preserved: %#v", items[0].Tools)
	}
}

func TestNormalizeResponsesItem_IsIdempotent(t *testing.T) {
	original := ResponsesItem{
		Type:      "function_call",
		CallID:    "call_1",
		Name:      "get_weather",
		Arguments: `{"location":"Tokyo"}`,
	}
	once := NormalizeResponsesItem(original)
	twice := NormalizeResponsesItem(once)

	if !reflect.DeepEqual(once, twice) {
		t.Fatalf("normalize should be idempotent, once=%#v twice=%#v", once, twice)
	}
}

func TestNormalizeResponsesItem_NormalizesNestedLegacyToolCall(t *testing.T) {
	item := NormalizeResponsesItem(ResponsesItem{
		Type: "tool_call",
		Content: map[string]interface{}{
			"content": map[string]interface{}{
				"id":   "toolu_nested",
				"name": "get_weather",
				"input": map[string]interface{}{
					"location": "Paris",
				},
			},
		},
	})

	if item.Type != "function_call" {
		t.Fatalf("expected function_call, got %#v", item)
	}
	if item.CallID != "toolu_nested" || item.Name != "get_weather" {
		t.Fatalf("nested legacy tool_call not normalized: %#v", item)
	}
	if item.Arguments == "" {
		t.Fatalf("nested legacy tool_call arguments not preserved: %#v", item)
	}
}

func TestParseResponsesInput_PreservesEncryptedFunctionArgs(t *testing.T) {
	items, err := ParseResponsesInput([]interface{}{
		map[string]interface{}{
			"type":      "function_call",
			"name":      "read_item",
			"namespace": "history",
			"call_id":   "call-1",
			"encrypted_function_args": []interface{}{
				"seg-alpha",
				"seg-beta",
			},
		},
	})
	if err != nil {
		t.Fatalf("ParseResponsesInput failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	got := items[0].EncryptedFunctionArgs
	if len(got) != 2 || got[0] != "seg-alpha" || got[1] != "seg-beta" {
		t.Fatalf("encrypted_function_args not preserved: %#v", got)
	}

	// 序列化 roundtrip：json tag 必须与 wire 字段名一致
	data, err := json.Marshal(items[0])
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(data), `"encrypted_function_args":["seg-alpha","seg-beta"]`) {
		t.Fatalf("wire field name mismatch: %s", data)
	}
}

package converters

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func extractResponseCompletedUsage(t *testing.T, events []string) map[string]interface{} {
	t.Helper()
	for _, event := range events {
		if !strings.Contains(event, "event: response.completed") {
			continue
		}
		for _, line := range strings.Split(event, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			jsonStr := strings.TrimPrefix(line, "data: ")
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
				continue
			}
			response, ok := payload["response"].(map[string]interface{})
			if !ok {
				continue
			}
			usage, ok := response["usage"].(map[string]interface{})
			if ok {
				return usage
			}
		}
	}
	t.Fatalf("未找到 response.completed usage 事件: %v", events)
	return nil
}

// collectThinkStreamText 聚合事件中的 reasoning 与 text delta 内容。
func collectThinkStreamText(events []string) (reasoning, text string) {
	for _, ev := range events {
		for _, line := range strings.Split(ev, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")
			typ := gjson.Get(payload, "type").String()
			switch typ {
			case "response.reasoning_summary_text.delta":
				reasoning += gjson.Get(payload, "delta").String()
			case "response.output_text.delta":
				text += gjson.Get(payload, "delta").String()
			}
		}
	}
	return reasoning, text
}

func runThinkStream(t *testing.T, chunks []string) []string {
	t.Helper()
	ctx := context.Background()
	originalReq := []byte(`{"model":"MiniMax-M2.7","input":"hi"}`)
	var state any
	var allEvents []string
	for _, chunk := range chunks {
		events := ConvertOpenAIChatToResponses(ctx, "MiniMax-M2.7", originalReq, nil, []byte(chunk), &state)
		allEvents = append(allEvents, events...)
	}
	return allEvents
}

package converters

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Loaded by go test -overlay; production and existing test files stay unchanged.
func TestExportInstalledClientFixtures(t *testing.T) {
	type converter func(context.Context, string, []byte, []byte, []byte, *any) []string
	all := map[string][]string{}
	for _, source := range []struct {
		name    string
		convert converter
	}{
		{"chat", ConvertOpenAIChatToResponses},
		{"claude", ConvertClaudeMessagesToResponses},
		{"gemini", ConvertGeminiStreamToResponses},
	} {
		for _, follow := range []string{"reasoning_only", "text", "tool", "no_reasoning"} {
			upstreamFollow := follow
			if follow == "no_reasoning" {
				upstreamFollow = "text"
			}
			var state any
			events := []string{}
			for _, line := range reasoningContractInput(source.name, upstreamFollow) {
				if follow == "no_reasoning" {
					if source.name == "chat" && strings.Contains(line, "reasoning_content") {
						continue
					}
					if source.name == "gemini" && strings.Contains(line, `"thought":true`) {
						continue
					}
					if source.name == "claude" && strings.Contains(line, `"index":0`) {
						continue
					}
				}
				events = append(events, source.convert(context.Background(), "m", []byte(`{"model":"m","input":"hi"}`), nil, []byte("data: "+line), &state)...)
			}
			all[source.name+"/"+follow] = events
		}
	}
	body, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("RESPONSES_COMPAT_FIXTURES"), body, 0600); err != nil {
		t.Fatal(err)
	}
}

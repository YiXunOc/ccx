package metrics

import (
	"github.com/BenedictKing/ccx/internal/types"
	"testing"
)

func TestCacheUsageAccountingAcrossProtocols(t *testing.T) {
	cases := []struct {
		name                          string
		usage                         types.Usage
		missed, output, created, read int64
	}{
		{"inclusive chat", types.Usage{InputTokens: 100, PromptTokensTotal: 100, OutputTokens: 5, CacheReadInputTokens: 70}, 30, 5, 0, 70},
		{"exclusive messages", types.Usage{InputTokens: 30, OutputTokens: 5, CacheReadInputTokens: 70, CacheCreationInputTokens: 2}, 30, 5, 2, 70},
		{"inclusive gemini", types.Usage{InputTokens: 100, PromptTokensTotal: 100, OutputTokens: 5, CacheReadInputTokens: 70}, 30, 5, 0, 70},
		{"no cache", types.Usage{InputTokens: 100, PromptTokensTotal: 100, OutputTokens: 5}, 100, 5, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b, c, d := extractUsageTokens(&tc.usage)
			if a != tc.missed || b != tc.output || c != tc.created || d != tc.read {
				t.Fatalf("got %d/%d/%d/%d", a, b, c, d)
			}
		})
	}
}

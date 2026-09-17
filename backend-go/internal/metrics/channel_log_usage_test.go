package metrics

import (
	"github.com/BenedictKing/ccx/internal/types"
	"testing"
)

func TestChannelLogUpdateUsage(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		usage                *types.Usage
		input, output, cache int64
	}{
		{"messages", &types.Usage{InputTokens: 100, OutputTokens: 20, CacheReadInputTokens: 40, CacheCreationInputTokens: 10}, 150, 20, 40},
		{"responses", &types.Usage{InputTokens: 140, PromptTokensTotal: 140, OutputTokens: 20, CacheReadInputTokens: 40}, 140, 20, 40},
		{"zero", &types.Usage{}, 0, 0, 0},
		{"missing", nil, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewChannelLogStore()
			store.Record("channel", &ChannelLog{RequestID: "request", Status: StatusStreaming})
			store.UpdateUsage("channel", "request", tc.usage)
			got := store.Get("channel")[0].Usage
			if tc.usage == nil {
				if got != nil {
					t.Fatal("missing usage must stay unknown")
				}
				return
			}
			if got == nil {
				t.Fatal("usage missing")
			}
			if got.InputTokens != tc.input || got.OutputTokens != tc.output || got.CacheReadTokens != tc.cache || got.TotalTokens != tc.input+tc.output {
				t.Fatalf("unexpected usage: %+v", got)
			}
		})
	}
}

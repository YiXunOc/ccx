package keypool

import (
	"github.com/BenedictKing/ccx/internal/config"
	"testing"
)

func TestCandidatesForModel_GroupModelPatterns(t *testing.T) {
	up := &config.UpstreamConfig{
		APIKeys:             []string{"a1", "a2", "b", "single", "other"},
		APIKeyConfigs:       []config.APIKeyConfig{{Key: "a1", QuotaGroup: "a"}, {Key: "a2", QuotaGroup: "a"}, {Key: "b", QuotaGroup: "b"}},
		DisabledGroupModels: []config.DisabledGroupModelInfo{{QuotaGroup: "a", Model: " GPT-* "}, {Key: "single", Model: "*"}},
	}
	for _, tc := range []struct {
		model string
		want  int
	}{{"gpt-5", 2}, {"claude", 4}, {"", 5}} {
		if got := len(CandidatesForModel(up, nil, tc.model)); got != tc.want {
			t.Fatalf("model=%q candidates=%d want=%d", tc.model, got, tc.want)
		}
	}
	up.DisabledGroupModels = append(up.DisabledGroupModels, config.DisabledGroupModelInfo{QuotaGroup: "b", Model: "*"}, config.DisabledGroupModelInfo{Key: "other", Model: "*"})
	if got := CandidatesForModel(up, nil, "gpt-5"); len(got) != 0 {
		t.Fatal("all excluded candidates must remain empty")
	}
}

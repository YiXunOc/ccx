package config

import (
	"os"
	"reflect"
	"testing"
	"time"
)

func TestGroupModelPattern(t *testing.T) {
	for _, tc := range []struct {
		pattern, model string
		want           bool
	}{
		{" GPT-5 ", " gPt-5 ", true}, {"gpt-5", "gpt-5-mini", false},
		{" GPT-* ", " gpt-5 ", true}, {"gpt-*", "xgpt-5", false},
		{"*-mini", "GPT-5-MINI", true}, {"*-mini", "gpt-5-mini-x", false},
		{"*GPT*", "vendor/gpt-5-mini", true}, {"*GPT*", "claude", false},
		{"*", "anything", true}, {"*", " ", false},
		{"a.b+", "A.B+", true}, {"a.b+", "axb", false},
	} {
		t.Run(tc.pattern+"/"+tc.model, func(t *testing.T) {
			if err := validateGroupModelPattern(tc.pattern); err != nil {
				t.Fatal(err)
			}
			up := UpstreamConfig{DisabledGroupModels: []DisabledGroupModelInfo{{Key: "single", Model: tc.pattern}}}
			if got := up.IsGroupModelDisabled("single", "", tc.model); got != tc.want {
				t.Fatalf("matched=%v want=%v", got, tc.want)
			}
			if up.IsGroupModelDisabled("other", "", tc.model) || up.IsGroupModelDisabled("single", "other-group", tc.model) {
				t.Fatal("rule escaped key scope")
			}
		})
	}
	for _, pattern := range []string{"", " ", "?", "gpt-?", "[abc]", "!gpt-*", "^gpt", "gpt*mini", "*gpt*mini*", "**", "***", "gpt**", "**gpt"} {
		if validateGroupModelPattern(pattern) == nil {
			t.Fatalf("accepted %q", pattern)
		}
		if matchesGroupModelPattern(pattern, "gpt-mini") {
			t.Fatalf("invalid historical pattern matched: %q", pattern)
		}
	}
}

func TestGroupModelPatternPersistenceIdentityAndIsolation(t *testing.T) {
	cm := newGroupModelTestConfigManager(t)
	for _, rule := range []struct{ key, model string }{
		{"sk-a1", " GPT-* "}, {"sk-a1", "gpt-5"}, {"sk-a1", "*mini*"}, {"sk-a1", "*-preview"}, {"sk-single", "*"},
	} {
		if _, _, err := cm.DisableGroupModel("Messages", 0, rule.key, rule.model); err != nil {
			t.Fatal(err)
		}
	}
	before := cm.GetConfig().Upstream[0].DisabledGroupModels
	bytesBefore, err := os.ReadFile(cm.configFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cm.DisableGroupModel("Messages", 0, "sk-a2", " gpt-* "); err != nil {
		t.Fatal(err)
	}
	bytesAfter, err := os.ReadFile(cm.configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytesBefore) != string(bytesAfter) || !reflect.DeepEqual(before, cm.GetConfig().Upstream[0].DisabledGroupModels) {
		t.Fatal("duplicate rewrote rules or persisted bytes")
	}
	for _, pattern := range []string{"gpt*mini", "!gpt-*", "gpt-?", "[ab]", "**"} {
		if _, _, err := cm.DisableGroupModel("Messages", 0, "sk-a1", pattern); err == nil {
			t.Fatalf("accepted %q", pattern)
		}
	}
	if !reflect.DeepEqual(before, cm.GetConfig().Upstream[0].DisabledGroupModels) {
		t.Fatal("invalid write changed rules")
	}
	if err := cm.Close(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewConfigManager(cm.configFile, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	up := reloaded.GetConfig().Upstream[0]
	if !reflect.DeepEqual(before, up.DisabledGroupModels) {
		t.Fatal("reload changed expression identities")
	}
	for _, model := range []string{"gpt-5", "gpt-6", "vendor-mini", "claude-preview"} {
		if !up.IsAPIKeyGroupModelDisabled("sk-a2", model) {
			t.Fatalf("group rule not reloaded for %s", model)
		}
		if up.IsAPIKeyGroupModelDisabled("sk-b", model) {
			t.Fatal("group rule escaped scope")
		}
	}
	if !up.IsAPIKeyGroupModelDisabled("sk-single", "anything") {
		t.Fatal("single-key * not reloaded")
	}
	if _, _, err := reloaded.RestoreGroupModel("Messages", 0, "", "account-a", " GPT-5 "); err != nil {
		t.Fatal(err)
	}
	up = reloaded.GetConfig().Upstream[0]
	if len(up.DisabledGroupModels) != 4 || !up.IsAPIKeyGroupModelDisabled("sk-a2", "gpt-5") {
		t.Fatal("exact restore removed overlapping wildcard")
	}
	if _, _, err := reloaded.RestoreGroupModel("Messages", 0, "", "account-a", "gpt-*"); err != nil {
		t.Fatal(err)
	}
	up = reloaded.GetConfig().Upstream[0]
	if up.IsAPIKeyGroupModelDisabled("sk-a2", "gpt-5") || !up.IsAPIKeyGroupModelDisabled("sk-a2", "gpt-mini") {
		t.Fatal("expression restore affected wrong rule")
	}
	// Temporary model_not_found restrictions retain literal identity.
	up.DisabledKeyModels = []DisabledKeyModelInfo{{Key: "sk-b", Model: "gpt-*"}}
	if up.IsKeyModelDisabledNow("sk-b", "gpt-5", time.Now()) {
		t.Fatal("temporary restrictions gained wildcard semantics")
	}
}

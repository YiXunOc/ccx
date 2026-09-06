package keypool

import (
	"reflect"
	"testing"
)

func TestApplyModelRules(t *testing.T) {
	tests := []struct {
		name           string
		discovered     []string
		rules          []string
		wantDiscovered []string
		wantManual     []string
	}{
		{
			name:           "精确与通配 allow 过滤发现清单且精确项可补候选",
			discovered:     []string{"gpt-4o", "gpt-4o-mini", "gpt-5", "claude-sonnet"},
			rules:          []string{"gpt-4o", "gpt-*", "!gpt-4o-mini", "manual-only"},
			wantDiscovered: []string{"gpt-4o", "gpt-5"},
			wantManual:     []string{"manual-only"},
		},
		{
			name:           "仅 deny 规则只排除不生成候选",
			discovered:     []string{"model-good", "model-bad"},
			rules:          []string{"!model-bad", "!never-discovered"},
			wantDiscovered: []string{"model-good"},
			wantManual:     nil,
		},
		{
			name:           "通配 allow 只补全发现命中不把规则本身当模型",
			discovered:     []string{"claude-sonnet", "gpt-5"},
			rules:          []string{"claude-*"},
			wantDiscovered: []string{"claude-sonnet"},
			wantManual:     nil,
		},
		{
			name:           "已发现的精确 allow 不重复补入",
			discovered:     []string{"Manual-Model", "manual-model"},
			rules:          []string{"manual-model"},
			wantDiscovered: []string{"Manual-Model"},
			wantManual:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDiscovered, gotManual := ApplyModelRules(tt.discovered, tt.rules)
			if !reflect.DeepEqual(gotDiscovered, tt.wantDiscovered) {
				t.Fatalf("发现模型 = %v, want %v", gotDiscovered, tt.wantDiscovered)
			}
			if !reflect.DeepEqual(gotManual, tt.wantManual) {
				t.Fatalf("手动候选 = %v, want %v", gotManual, tt.wantManual)
			}
		})
	}
}

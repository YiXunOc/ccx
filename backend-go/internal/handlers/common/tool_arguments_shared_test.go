package common

import (
	"github.com/BenedictKing/ccx/internal/utils"
	"testing"
)

func TestNamedToolArgumentsSharedRules(t *testing.T) {
	for _, tc := range []struct {
		name string
		args interface{}
		bad  bool
	}{
		{" READ ", nil, true}, {"read", "", true}, {"read", "{}", true}, {"read", map[string]interface{}{}, true},
		{"read", "{", true}, {"unknown", "{", true}, {"unknown", "", false}, {"unknown", "{}", false},
		{"read", "null", false}, {"read", "[]", false}, {"read", []interface{}{}, true},
		{"read", map[string]interface{}{"path": "x"}, false},
		{"mcp__serena__find_symbol", "{}", true}, {"functions.read", "{}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMalformedNamedToolArguments(tc.name, tc.args); got != tc.bad {
				t.Fatalf("common got %v want %v", got, tc.bad)
			}
			if got := utils.IsMalformedNamedToolArguments(tc.name, tc.args); got != tc.bad {
				t.Fatalf("utils got %v want %v", got, tc.bad)
			}
		})
	}
}

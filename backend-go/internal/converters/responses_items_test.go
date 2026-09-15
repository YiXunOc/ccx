package converters

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

// TestResolveFunctionCallItem_EncryptedArgsPlaceholder 验证 Codex namespace 工具的
// 密文分段 function_call 转换时 arguments 补 "{}" 占位（空串会被 DeepSeek 等上游拒收），
// 无密文时保持原样。
func TestResolveFunctionCallItem_EncryptedArgsPlaceholder(t *testing.T) {
	t.Run("encrypted segments yield placeholder", func(t *testing.T) {
		callID, name, args, err := resolveFunctionCallItem(types.ResponsesItem{
			Type:                  "function_call",
			Name:                  "read_item",
			CallID:                "call-1",
			EncryptedFunctionArgs: []string{"cipher-segment"},
		})
		if err != nil {
			t.Fatalf("resolveFunctionCallItem err = %v", err)
		}
		if callID != "call-1" || name != "read_item" {
			t.Fatalf("identity wrong: %s/%s", callID, name)
		}
		if args != "{}" {
			t.Fatalf("args = %q, want {} placeholder for encrypted-only call", args)
		}
	})

	t.Run("no encrypted segments keeps empty args", func(t *testing.T) {
		_, _, args, err := resolveFunctionCallItem(types.ResponsesItem{
			Type:   "function_call",
			Name:   "shell",
			CallID: "call-2",
		})
		if err != nil {
			t.Fatalf("resolveFunctionCallItem err = %v", err)
		}
		if args != "" {
			t.Fatalf("args = %q, want empty without encrypted segments", args)
		}
	})

	t.Run("plaintext arguments take precedence", func(t *testing.T) {
		_, _, args, err := resolveFunctionCallItem(types.ResponsesItem{
			Type:                  "function_call",
			Name:                  "write_file",
			CallID:                "call-3",
			Arguments:             `{"path":"a.txt"}`,
			EncryptedFunctionArgs: []string{"cipher"},
		})
		if err != nil {
			t.Fatalf("resolveFunctionCallItem err = %v", err)
		}
		if args != `{"path":"a.txt"}` {
			t.Fatalf("args = %q, want plaintext preserved", args)
		}
	})
}

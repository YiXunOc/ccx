package config

import (
	"strings"
	"testing"
)

// 被拒 beta token 的增量学习：上游逐个点名拒绝不同 token 时，Record 必须
// 合并 evidence 并再次返回 true（触发同 Key 重试剥离），否则发送前剥离永远
// 停在第一个 token，后续 token 反复 400 直至模型熔断。
func TestRecordBetaHeaderMergesNewTokens(t *testing.T) {
	cache := NewChannelCompatCache()

	if !cache.Record("ch1", "kh1", "m1", TraitUnsupportedBetaHeader, true, CompatSourceErrorSignal,
		"尚未验证或不支持的 anthropic-beta：context-1m-2025-08-07") {
		t.Fatal("首次学习应返回 true")
	}

	// 同一 token 重复报错：不视为新增，不触发重试（防死循环）
	if cache.Record("ch1", "kh1", "m1", TraitUnsupportedBetaHeader, true, CompatSourceErrorSignal,
		"尚未验证或不支持的 anthropic-beta：context-1m-2025-08-07") {
		t.Fatal("重复 token 不应返回 true")
	}

	// 新 token：合并 evidence 并视为新增
	if !cache.Record("ch1", "kh1", "m1", TraitUnsupportedBetaHeader, true, CompatSourceErrorSignal,
		"尚未验证或不支持的 anthropic-beta：interleaved-thinking-2025-05-14") {
		t.Fatal("新 token 应返回 true")
	}

	state, ok := cache.Trait("ch1", "kh1", "m1", TraitUnsupportedBetaHeader)
	if !ok {
		t.Fatal("记录应存在")
	}
	tokens := ExtractRejectedBetaTokens(state.Evidence)
	if len(tokens) != 2 || tokens[0] != "context-1m-2025-08-07" || tokens[1] != "interleaved-thinking-2025-05-14" {
		t.Fatalf("合并后应含全部两个 token, got evidence=%q tokens=%v", state.Evidence, tokens)
	}

	// 合并态下再遇旧 token：不新增
	if cache.Record("ch1", "kh1", "m1", TraitUnsupportedBetaHeader, true, CompatSourceErrorSignal,
		"unsupported anthropic-beta header: context-1m-2025-08-07") {
		t.Fatal("合并态下旧 token 不应返回 true")
	}

	// 证据不含可提取 token：不新增（格式不符，保持原记录）
	if cache.Record("ch1", "kh1", "m1", TraitUnsupportedBetaHeader, true, CompatSourceErrorSignal,
		"unsupported anthropic-beta configuration") {
		t.Fatal("无 token 文案不应触发合并")
	}
	if got := ExtractRejectedBetaTokens(mustTrait(t, cache)); len(got) != 2 {
		t.Fatalf("原记录不应被无 token 文案覆盖, got %v", got)
	}

	// 其他 trait 不受合并语义影响：同结论重复记录仍返回 false
	if !cache.Record("ch1", "kh1", "m1", TraitNoDocumentSupport, true, CompatSourceErrorSignal, "doc refused") {
		t.Fatal("其他 trait 首次学习应返回 true")
	}
	if cache.Record("ch1", "kh1", "m1", TraitNoDocumentSupport, true, CompatSourceErrorSignal, "doc refused again") {
		t.Fatal("其他 trait 同结论重复记录应返回 false")
	}
}

func mustTrait(t *testing.T, cache *ChannelCompatCache) string {
	t.Helper()
	state, ok := cache.Trait("ch1", "kh1", "m1", TraitUnsupportedBetaHeader)
	if !ok {
		t.Fatal("记录应存在")
	}
	return state.Evidence
}

// 合并态 evidence 必须保持可解析且语义自描述。
func TestMergeRejectedBetaEvidenceFormat(t *testing.T) {
	merged, changed := mergeRejectedBetaEvidence(
		"尚未验证或不支持的 anthropic-beta：context-1m-2025-08-07",
		"anthropic-beta `interleaved-thinking-2025-05-14` is not enabled",
	)
	if !changed {
		t.Fatal("应报告 changed")
	}
	if !strings.Contains(merged, "context-1m-2025-08-07") || !strings.Contains(merged, "interleaved-thinking-2025-05-14") {
		t.Fatalf("合并态应含全部 token: %q", merged)
	}
	// 合并态可被自身再解析（供消费端剥离与后续增量合并）
	if tokens := ExtractRejectedBetaTokens(merged); len(tokens) != 2 {
		t.Fatalf("合并态应可再解析出 2 个 token, got %v", tokens)
	}

	if _, changed := mergeRejectedBetaEvidence(merged, "尚未验证或不支持的 anthropic-beta：context-1m-2025-08-07"); changed {
		t.Fatal("无新 token 时不应 changed")
	}
	if _, changed := mergeRejectedBetaEvidence(merged, "unsupported anthropic-beta configuration"); changed {
		t.Fatal("无 token 文案不应 changed")
	}
}

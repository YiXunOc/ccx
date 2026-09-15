package autopilot

import (
	"reflect"
	"testing"
)

// ProtocolModelsForChannel 聚合语义测试：并集、保序去重、空输入回退、别名兜底。
// 有效清单未初始化时 ListActiveByChannel fail-open（等价 ListByChannel），
// 测试直接 Upsert 即可命中。

func upsertProtocolTestProfile(t *testing.T, store *ProfileStore, channelUID, keyHash string, protocolModels map[string][]string, available []string) {
	t.Helper()
	p := newTestProfile("ep-"+keyHash, channelUID, "openai", "https://example.test")
	p.KeyHash = keyHash
	p.ProtocolModels = protocolModels
	p.AvailableModels = available
	if err := store.Upsert(p); err != nil {
		t.Fatalf("Upsert(%s): %v", keyHash, err)
	}
}

func TestProtocolModelsForChannel(t *testing.T) {
	store, err := NewProfileStoreWithDB(newTestDB(t))
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB: %v", err)
	}
	channelUID := "ch_cap_probe"
	upsertProtocolTestProfile(t, store, channelUID, "keyhash-a", map[string][]string{
		"responses": {"glm-5.3", "deepseek-v4-pro-0813", "qwen3.8-max"},
	}, []string{"glm-5.3", "minimax-m2.7"})
	upsertProtocolTestProfile(t, store, channelUID, "keyhash-b", map[string][]string{
		"responses": {"glm-5.3", "longcat-2.0"}, // glm-5.3 重复，应去重
	}, nil)

	got := ProtocolModelsForChannel(store, channelUID, "responses")
	want := []string{"glm-5.3", "deepseek-v4-pro-0813", "qwen3.8-max", "longcat-2.0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("聚合结果 = %v, want %v", got, want)
	}

	// availableModels 不参与协议聚合（protocolModels 是逐协议验证过的更精确清单）
	if got := ProtocolModelsForChannel(store, channelUID, "gemini"); len(got) != 0 {
		t.Fatalf("无该协议画像应返回空，got %v", got)
	}
	if got := ProtocolModelsForChannel(store, "ch_missing", "responses"); len(got) != 0 {
		t.Fatalf("无画像渠道应返回空，got %v", got)
	}
	if got := ProtocolModelsForChannel(nil, channelUID, "responses"); len(got) != 0 {
		t.Fatalf("store 为 nil 应返回空，got %v", got)
	}
	if got := ProtocolModelsForChannel(store, "", "responses"); len(got) != 0 {
		t.Fatalf("channelUID 为空应返回空，got %v", got)
	}
}

func TestProtocolModelsForChannelAlias(t *testing.T) {
	store, err := NewProfileStoreWithDB(newTestDB(t))
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB: %v", err)
	}
	channelUID := "ch_cap_probe_alias"
	upsertProtocolTestProfile(t, store, channelUID, "keyhash-a", map[string][]string{
		"messages": {"claude-fable-5"},
	}, nil)

	// "claude" 协议名应经别名映射取到 "messages" 键的清单
	got := ProtocolModelsForChannel(store, channelUID, "claude")
	if !reflect.DeepEqual(got, []string{"claude-fable-5"}) {
		t.Fatalf("别名聚合结果 = %v, want [claude-fable-5]", got)
	}
}

func TestSharedProtocolModelsForChannelUnregistered(t *testing.T) {
	orig := getSharedProfileStore()
	SetSharedProfileStore(nil)
	defer SetSharedProfileStore(orig)
	// 未注册（或初始化失败）时 fail-open：返回空，调用方回退内置清单
	if got := SharedProtocolModelsForChannel("ch_any", "responses"); len(got) != 0 {
		t.Fatalf("未注册共享 store 应返回空，got %v", got)
	}
}

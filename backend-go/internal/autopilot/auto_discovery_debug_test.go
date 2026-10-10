package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestDiscoveryDebugHelpers(t *testing.T) {
	// 测试 discoveryKeyID 生成稳定哈希
	key1 := "sk-test-key-123"
	key2 := "sk-test-key-456"

	id1 := discoveryKeyID(key1)
	id2 := discoveryKeyID(key2)

	if id1 == id2 {
		t.Error("不同 key 应生成不同 ID")
	}

	if discoveryKeyID(key1) != id1 {
		t.Error("相同 key 应生成相同 ID")
	}

	if len(id1) != 12 { // 6 bytes = 12 hex chars
		t.Errorf("ID 长度应为 12，实际 %d", len(id1))
	}

	// 测试 discoveryDebugEnabled 在非开发模式下返回 false
	env := config.NewEnvConfig()
	runner := NewAutoDiscoveryRunner(nil, nil)
	if !env.IsDevelopment() {
		enabled := runner.discoveryDebugEnabled()
		if enabled {
			t.Error("非开发模式下不应启用调试日志")
		}
	} else {
		// 开发模式下，验证缓存标志与实时判断一致
		if runner.discoveryDebugEnabled() != (env.IsDevelopment() && env.ShouldLog("debug")) {
			t.Error("调试标志缓存与实时判断不一致")
		}
	}

	// 测试计数辅助函数
	testChannel := &config.UpstreamConfig{
		APIKeys:  []string{"key1", "key2"},
		BaseURLs: []string{"http://url1", "http://url2"},
	}

	if discoveryKeyCount(testChannel) != 2 {
		t.Errorf("期望 2 个 key，实际 %d", discoveryKeyCount(testChannel))
	}

	if discoveryBaseURLCount(testChannel) != 2 {
		t.Errorf("期望 2 个 base URL，实际 %d", discoveryBaseURLCount(testChannel))
	}

	if discoveryKeyCount(nil) != 0 {
		t.Error("nil channel 应返回 0")
	}
}

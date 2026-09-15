package config

import "testing"

func TestEnsureChannelGroupMultiplierLimits(t *testing.T) {
	low, high := 0.5, 2.0
	cm := &ConfigManager{}
	cm.config.ChatUpstream = []UpstreamConfig{
		{
			Name: "mixed",
			APIKeyConfigs: []APIKeyConfig{
				{Key: "a", GroupMultiplier: &low, MaxGroupMultiplier: &high},
				{Key: "b", GroupMultiplier: &low, MaxGroupMultiplier: &low},
				{Key: "legacy"},
			},
		},
		{
			Name:               "already-gated",
			MaxGroupMultiplier: &high,
			APIKeyConfigs: []APIKeyConfig{
				{Key: "c", GroupMultiplier: &low, MaxGroupMultiplier: &low},
			},
		},
		{
			Name:          "no-key-max",
			APIKeyConfigs: []APIKeyConfig{{Key: "d"}},
		},
	}

	if !cm.ensureChannelGroupMultiplierLimits() {
		t.Fatal("首次迁移应报告变更")
	}

	got := cm.config.ChatUpstream
	// 渠道 0：渠道级为空 → 聚合 key 级上限最大值 2.0，key 级全部清空。
	if got[0].MaxGroupMultiplier == nil || *got[0].MaxGroupMultiplier != high {
		t.Fatalf("渠道 0 应聚合 key 级上限最大值, got %+v", got[0].MaxGroupMultiplier)
	}
	for _, cfg := range got[0].APIKeyConfigs {
		if cfg.MaxGroupMultiplier != nil {
			t.Fatalf("渠道 0 的 key 级上限应清空, got %+v", cfg)
		}
	}
	// 渠道 1：已有渠道级上限保持不变（更宽松的 key 上限不放大它）。
	if got[1].MaxGroupMultiplier == nil || *got[1].MaxGroupMultiplier != high {
		t.Fatalf("渠道 1 应保留渠道级上限, got %+v", got[1].MaxGroupMultiplier)
	}
	if got[1].APIKeyConfigs[0].MaxGroupMultiplier != nil {
		t.Fatalf("渠道 1 的 key 级上限应清空, got %+v", got[1].APIKeyConfigs[0])
	}
	// 渠道 2：无 key 级上限则不产生渠道级上限。
	if got[2].MaxGroupMultiplier != nil {
		t.Fatalf("渠道 2 不应生成渠道级上限, got %+v", got[2].MaxGroupMultiplier)
	}

	// 幂等：二次迁移无变化。
	if cm.ensureChannelGroupMultiplierLimits() {
		t.Fatal("二次迁移应无变化")
	}
}

package config

import "testing"

func TestMergeAPIKeyConfigPreservesMultiplierMetadataFromStaleFormSnapshot(t *testing.T) {
	ratio := 0.05
	existing := APIKeyConfig{
		Key:                  "sk-1",
		GroupMultiplier:      &ratio,
		MultiplierSource:     "manual",
		MultiplierSyncStatus: "manual",
		ConsumptionPolicy:    KeyConsumptionOpportunistic,
	}
	// 渠道编辑表单快照早于 Key 倍率弹窗保存：incoming 不携带倍率元数据。
	incoming := APIKeyConfig{Key: "sk-1"}

	merged := mergeAPIKeyConfig(&existing, incoming)
	if merged.GroupMultiplier == nil || *merged.GroupMultiplier != ratio {
		t.Fatalf("GroupMultiplier 应保留 existing，got %+v", merged.GroupMultiplier)
	}
	if merged.MultiplierSource != "manual" || merged.MultiplierSyncStatus != "manual" {
		t.Fatalf("倍率来源/状态应保留 existing，got %q/%q", merged.MultiplierSource, merged.MultiplierSyncStatus)
	}
	if merged.ConsumptionPolicy != KeyConsumptionOpportunistic {
		t.Fatalf("ConsumptionPolicy 应保留 existing，got %q", merged.ConsumptionPolicy)
	}
}

func TestMergeAPIKeyConfigAllowsExplicitOverwrite(t *testing.T) {
	oldRatio, newRatio := 0.05, 0.5
	existing := APIKeyConfig{Key: "sk-1", GroupMultiplier: &oldRatio, MultiplierSource: "manual"}
	// incoming 携带显式新值（同步服务/表单持有完整数据）时按 incoming 覆盖。
	incoming := APIKeyConfig{Key: "sk-1", GroupMultiplier: &newRatio, MultiplierSource: "new_api"}

	merged := mergeAPIKeyConfig(&existing, incoming)
	if merged.GroupMultiplier == nil || *merged.GroupMultiplier != newRatio {
		t.Fatalf("显式新值应覆盖，got %+v", merged.GroupMultiplier)
	}
	if merged.MultiplierSource != "new_api" {
		t.Fatalf("显式来源应覆盖，got %q", merged.MultiplierSource)
	}
}

func TestApplyAPIKeyConfigUpdateSkipMerge(t *testing.T) {
	ratio := 0.05
	up := &UpstreamConfig{
		APIKeys:       []string{"sk-1"},
		APIKeyConfigs: []APIKeyConfig{{Key: "sk-1", GroupMultiplier: &ratio, MultiplierSource: "manual", MultiplierSyncStatus: "manual"}},
	}
	// Key 倍率端点的显式清除：倍率与同步状态归零。
	updates := UpstreamUpdate{
		APIKeyConfigs:         []APIKeyConfig{{Key: "sk-1"}},
		SkipAPIKeyConfigMerge: true,
	}
	applyAPIKeyConfigUpdate(up, updates)
	cfg := up.APIKeyConfigs[0]
	if cfg.GroupMultiplier != nil || cfg.MultiplierSource != "" || cfg.MultiplierSyncStatus != "" {
		t.Fatalf("SkipAPIKeyConfigMerge 应允许显式清除，got %+v", cfg)
	}

	// 默认路径（渠道编辑表单）仍受回填保护。
	up2 := &UpstreamConfig{
		APIKeys:       []string{"sk-2"},
		APIKeyConfigs: []APIKeyConfig{{Key: "sk-2", GroupMultiplier: &ratio, MultiplierSource: "manual", MultiplierSyncStatus: "manual"}},
	}
	applyAPIKeyConfigUpdate(up2, UpstreamUpdate{APIKeyConfigs: []APIKeyConfig{{Key: "sk-2"}}})
	cfg2 := up2.APIKeyConfigs[0]
	if cfg2.GroupMultiplier == nil || *cfg2.GroupMultiplier != ratio {
		t.Fatalf("默认合并路径应回填倍率，got %+v", cfg2.GroupMultiplier)
	}
}

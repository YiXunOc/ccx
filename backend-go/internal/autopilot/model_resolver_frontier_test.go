package autopilot

import (
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// frontierIntegrationCandidates 构造使用内置注册表实时质量与成本数据的集成测试候选集。
//
// 四者 benchmark 均处于 provisional 泳道且无实测置信度，质量区间（±0.15）相互重叠，
// 因此全部留在 Pareto 前沿上；opus 因质量断点独占一簇，其余三者同簇。
func frontierIntegrationCandidates() []ModelProfile {
	return []ModelProfile{
		makeModelProfile("claude-opus-5", ModelFamilyClaude, QualityTierPremium, 1000000,
			true, true, true, true, 0),
		makeModelProfile("claude-sonnet-5", ModelFamilyClaude, QualityTierHigh, 1000000,
			true, true, true, true, 0),
		makeModelProfile("gpt-5.4", ModelFamilyOpenAI, QualityTierHigh, 1000000,
			true, true, true, true, 0),
		makeModelProfile("glm-5.2", ModelFamilyGLM, QualityTierHigh, 1048576,
			true, false, true, true, 0),
	}
}

// newFrontierTestResolver 创建带指定成本倾向车道的测试 resolver。
// Frontier/Ladder 默认启用，无需开关。
func newFrontierTestResolver(t *testing.T, profiles []ModelProfile, mode string) *ModelResolver {
	t.Helper()
	return newTestResolverWithConfig(t, profiles, config.Config{
		AutopilotRouting: config.AutopilotRoutingConfig{
			CostPreference: config.CostPreferenceConfig{Mode: mode},
		},
	})
}

// 默认启用：无 cfgManager 时仍使用 balanced 车道；当前质量增益足以支持 opus 簇的成本溢价。
func TestRankEligibleModels_FrontierEnabledByDefault(t *testing.T) {
	best := rankTestModels(frontierIntegrationCandidates(), "claude-sonnet-4-6")
	if best.ModelID != "claude-opus-5" {
		t.Fatalf("default balanced frontier should pick claude-opus-5, got %s", best.ModelID)
	}
}

// balanced 车道按当前 frontierMaxCostPremiumPerQualityBase=10.0 校准；
// 当前注册表中的 opus 质量增益值回溢价。过高溢价仍由 TestCapClusterCostPremium 拒绝。
func TestRankEligibleModels_FrontierBalancedAllowsJustifiedCostPremium(t *testing.T) {
	resolver := newFrontierTestResolver(t, nil, "balanced")
	best := resolver.rankEligibleModels(frontierIntegrationCandidates(), "claude-sonnet-4-6", "ch_test", "messages", CapabilityFloor{})
	if best.profile.ModelID != "claude-opus-5" {
		t.Fatalf("balanced frontier should pick claude-opus-5, got %s", best.profile.ModelID)
	}
	if !strings.Contains(best.frontierNote, "frontier:balanced") {
		t.Fatalf("frontierNote = %q, want frontier:balanced marker", best.frontierNote)
	}
}

// cost_first 车道：无同族候选时取前沿上综合成本最低者 glm-5.2（$5.8）。
func TestRankEligibleModels_FrontierCostFirstPicksCheapest(t *testing.T) {
	resolver := newFrontierTestResolver(t, nil, "cost_first")
	best := resolver.rankEligibleModels(frontierIntegrationCandidates(), "deepseek-v4-flash", "ch_test", "messages", CapabilityFloor{})
	if best.profile.ModelID != "glm-5.2" {
		t.Fatalf("cost_first frontier should pick glm-5.2, got %s", best.profile.ModelID)
	}
	if !strings.Contains(best.frontierNote, "frontier:cost_first") {
		t.Fatalf("frontierNote = %q, want frontier:cost_first marker", best.frontierNote)
	}
}

// quality_first 车道：显著 benchmark 优势（≥premiumFrontierBenchmarkMinDelta）直接兑现，
// 不被低成本并列池吞掉。当前示例中 claude-opus-5(83.07) 显著高于 claude-sonnet-5(64.78)，
// 因此 quality_first 应选择更强的 opus-5；只有差距不足阈值时才按成本 tie-break。
func TestRankEligibleModels_FrontierQualityFirstRespectsBenchmarkDelta(t *testing.T) {
	resolver := newFrontierTestResolver(t, nil, "quality_first")
	best := resolver.rankEligibleModels(frontierIntegrationCandidates(), "claude-sonnet-4-6", "ch_test", "messages", CapabilityFloor{})
	if best.profile.ModelID != "claude-opus-5" {
		t.Fatalf("quality_first frontier should pick significantly stronger claude-opus-5, got %s", best.profile.ModelID)
	}
	if !strings.Contains(best.frontierNote, "tie_pool=") {
		t.Fatalf("frontierNote = %q, want tie_pool marker", best.frontierNote)
	}
}

// 成本证据不足时 fail-open 回退旧链，结果与旧链直接计算一致，并标注回退原因。
func TestRankEligibleModels_FrontierFallbackWithoutComparableCost(t *testing.T) {
	eligible := []ModelProfile{
		makeModelProfile("custom-beta", ModelFamilyClaude, QualityTierNormal, 100000,
			true, false, false, true, 0),
		makeModelProfile("custom-alpha", ModelFamilyClaude, QualityTierNormal, 100000,
			true, false, false, true, 0),
	}
	resolver := newFrontierTestResolver(t, nil, "balanced")
	best := resolver.rankEligibleModels(eligible, "claude-sonnet-5", "ch_test", "messages", CapabilityFloor{})

	legacy := rankTestModels(eligible, "claude-sonnet-5")
	if best.profile.ModelID != legacy.ModelID {
		t.Fatalf("fallback result %s differs from legacy %s", best.profile.ModelID, legacy.ModelID)
	}
	if best.frontierNote != "frontier:fallback=insufficient_comparable_cost" {
		t.Fatalf("frontierNote = %q, want fallback reason", best.frontierNote)
	}
}

func TestBuildRankedCandidates_KeepsComparableModelsAcrossLegacyTiers(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("gpt-5.6-sol", ModelFamilyOpenAI, QualityTierPremium, 272000,
			true, true, true, true, 0),
		makeModelProfile("gpt-5.6-terra", ModelFamilyOpenAI, QualityTierPremium, 272000,
			true, true, true, true, 0),
		makeModelProfile("gpt-5.6-luna", ModelFamilyOpenAI, QualityTierPremium, 272000,
			true, true, true, true, 0),
		makeModelProfile("gpt-5.4-mini", ModelFamilyOpenAI, QualityTierNormal, 272000,
			true, true, true, true, 0),
	}
	resolver := newFrontierTestResolver(t, profiles, "quality_first")
	ranked := resolver.buildRankedCandidates(profiles, "claude-opus-4-8", "ch_test", "responses", CapabilityFloor{
		QualityBenefitCap: QualityTierNormal,
	})
	if len(ranked) != len(profiles) {
		t.Fatalf("Frontier candidates = %d, want all %d models before dynamic clustering", len(ranked), len(profiles))
	}
	seen := make(map[string]bool, len(ranked))
	for _, candidate := range ranked {
		seen[candidate.profile.ModelID] = true
	}
	for _, profile := range profiles {
		if !seen[profile.ModelID] {
			t.Fatalf("model %q was removed by legacy QualityTier before Frontier", profile.ModelID)
		}
	}
}

func TestRankEligibleModels_QualityBenefitCapUsesDynamicCluster(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("gpt-5.6-sol", ModelFamilyOpenAI, QualityTierPremium, 272000,
			true, true, true, true, 0),
		makeModelProfile("gpt-5.5-openai-compact", ModelFamilyOpenAI, QualityTierPremium, 272000,
			true, true, true, true, 0),
		makeModelProfile("gpt-5.5", ModelFamilyOpenAI, QualityTierPremium, 272000,
			true, true, true, true, 0),
	}
	resolver := newFrontierTestResolver(t, profiles, "quality_first")
	best := resolver.rankEligibleModels(profiles, "claude-opus-4-8", "ch_test", "responses", CapabilityFloor{
		QualityBenefitCap: QualityTierNormal,
	})
	// 注册表快照演进后 sol($4/$20)比 gpt-5.5($5/$30)便宜且 bench 更高，
	// cap 簇内按"便宜优先"应选 sol——cap 语义是不为超档质量付溢价，而非禁选高档
	if best.profile.ModelID != "gpt-5.6-sol" {
		t.Fatalf("benefit-capped dynamic cluster selected %q, want cheapest adequate model gpt-5.6-sol", best.profile.ModelID)
	}
	if !strings.Contains(best.frontierNote, "benefit_cap=normal") {
		t.Fatalf("frontierNote = %q, want dynamic benefit cap marker", best.frontierNote)
	}
}

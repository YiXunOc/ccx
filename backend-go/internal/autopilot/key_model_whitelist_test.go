package autopilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/utils"
)

func TestAutoDiscoveryKeyModelRulesPreserveManualEvidenceBoundary(t *testing.T) {
	db := newTestDB(t)
	profileStore, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB: %v", err)
	}
	modelStore, err := NewModelProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewModelProfileStoreWithDB: %v", err)
	}

	const (
		channelUID = "ch-key-model-rules"
		baseURL    = "https://models.example.com/v1"
		apiKey     = "sk-key-model-rules"
	)
	channel := config.UpstreamConfig{
		ChannelUID:  channelUID,
		BaseURL:     baseURL,
		APIKeys:     []string{apiKey},
		ServiceType: "openai",
		AutoManaged: true,
		APIKeyConfigs: []config.APIKeyConfig{{
			Key:    apiKey,
			Models: []string{"gpt-*", "manual-only", "!gpt-blocked"},
		}},
	}
	_, cleanup := createTestConfigManager(t, config.Config{Upstream: []config.UpstreamConfig{channel}})
	t.Cleanup(cleanup)

	discoveredAt := time.Now().UTC().Truncate(time.Second)
	const discoveryMessage = "models API returned concrete models"
	endpoint := EndpointDiscoveryResult{
		KeyMask:               utils.MaskAPIKey(apiKey),
		BaseURL:               baseURL,
		Models:                []string{"gpt-discovered", "gpt-blocked", "claude-unlisted"},
		ModelsCount:           3,
		ProtocolOk:            true,
		ModelDiscoverySource:  ModelDiscoverySourceModelsAPI,
		ModelDiscoveryMessage: discoveryMessage,
		ModelsDiscoveredAt:    &discoveredAt,
		apiKey:                apiKey,
	}

	runner := NewAutoDiscoveryRunner(profileStore, nil)
	runner.ModelProfileStore = modelStore
	runner.applyKeyModelRules(&channel, &endpoint)
	if got, want := endpoint.Models, []string{"gpt-discovered"}; !slices.Equal(got, want) {
		t.Fatalf("过滤后的自动发现模型 = %v, want %v", got, want)
	}
	if endpoint.ModelsCount != 1 {
		t.Fatalf("ModelsCount = %d, want 1", endpoint.ModelsCount)
	}
	if got, want := endpoint.manualModels, []string{"manual-only"}; !slices.Equal(got, want) {
		t.Fatalf("手动补入候选 = %v, want %v", got, want)
	}

	uid, err := runner.writeProfileForEndpoint(channelUID, &channel, endpoint, 0, "messages", nil)
	if err != nil {
		t.Fatalf("writeProfileForEndpoint: %v", err)
	}
	storedEndpoint := profileStore.Get(uid)
	if storedEndpoint == nil || !slices.Equal(storedEndpoint.AvailableModels, []string{"gpt-discovered", "manual-only"}) {
		t.Fatalf("endpoint 有效候选应包含过滤后的发现模型与精确手动项: %+v", storedEndpoint)
	}
	if got := storedEndpoint.ProtocolModels["chat"]; !slices.Equal(got, []string{"gpt-discovered"}) {
		t.Fatalf("协议发现元数据不得混入手动模型: %v", got)
	}
	if storedEndpoint.ModelDiscoverySource != ModelDiscoverySourceModelsAPI ||
		storedEndpoint.ModelDiscoveryMessage != discoveryMessage ||
		storedEndpoint.ModelsDiscoveredAt == nil || !storedEndpoint.ModelsDiscoveredAt.Equal(discoveredAt) {
		t.Fatalf("真实发现时间/来源/消息应保留且不得由手动候选改写: %+v", storedEndpoint)
	}
	expectedHash := sha256.Sum256([]byte("gpt-discovered"))
	if storedEndpoint.ModelListHash != hex.EncodeToString(expectedHash[:8]) {
		t.Fatalf("模型哈希只能基于过滤后的发现清单: %q", storedEndpoint.ModelListHash)
	}

	actualMetricsKey := computeMetricsIdentityKey(baseURL, apiKey, channel.ServiceType)
	discovered := modelStore.Get(channelUID, "messages", actualMetricsKey, "gpt-discovered")
	if discovered == nil || !discovered.ProbeSuccess || discovered.Source != "auto_discovery" {
		t.Fatalf("真实发现模型证据不正确: %+v", discovered)
	}
	manual := modelStore.Get(channelUID, "messages", actualMetricsKey, "manual-only")
	if manual == nil || manual.ProbeSuccess || manual.Source != "manual" {
		t.Fatalf("手动候选不得伪装成自动发现/已验证: %+v", manual)
	}
	for _, forbidden := range []string{"gpt-blocked", "claude-unlisted"} {
		if got := modelStore.Get(channelUID, "messages", actualMetricsKey, forbidden); got != nil {
			t.Fatalf("规则排除的模型 %q 不应生成画像: %+v", forbidden, got)
		}
	}

	// ReconcileModels 的 keep 集合须包含手动候选；同名已有成功实证也不得被手动项降级。
	verifiedManual := *manual
	verifiedManual.ProbeSuccess = true
	verifiedManual.Source = "capability_test"
	verifiedManual.LastProbeAt = discoveredAt
	verifiedManual.ProbeLatencyMs = 42
	verifiedManual.ProbeConfidence = 0.9
	if err := modelStore.Upsert(&verifiedManual); err != nil {
		t.Fatalf("写入既有成功证据: %v", err)
	}

	// 自动发现刷新仍从 APIKeyConfig.Models 重新补入手动项，且不升级或覆盖已有更强证据。
	refreshed := EndpointDiscoveryResult{
		KeyMask:              utils.MaskAPIKey(apiKey),
		BaseURL:              baseURL,
		Models:               []string{"gpt-new"},
		ModelsCount:          1,
		ProtocolOk:           true,
		ModelDiscoverySource: ModelDiscoverySourceModelsAPI,
		apiKey:               apiKey,
	}
	runner.applyKeyModelRules(&channel, &refreshed)
	if _, err := runner.writeProfileForEndpoint(channelUID, &channel, refreshed, 0, "messages", nil); err != nil {
		t.Fatalf("刷新画像失败: %v", err)
	}
	manual = modelStore.Get(channelUID, "messages", actualMetricsKey, "manual-only")
	if manual == nil {
		t.Fatal("ReconcileModels 不得删除仍在配置中的手动候选")
	}
	if !manual.ProbeSuccess || manual.Source != "capability_test" ||
		!manual.LastProbeAt.Equal(discoveredAt) || manual.ProbeLatencyMs != 42 || manual.ProbeConfidence != 0.9 {
		t.Fatalf("刷新不得把同名既有成功实证降级为手动未验证画像: %+v", manual)
	}
	if got := modelStore.Get(channelUID, "messages", actualMetricsKey, "gpt-discovered"); got != nil {
		t.Fatalf("ReconcileModels 应删除本轮已不在有效候选中的旧发现模型: %+v", got)
	}
	storedEndpoint = profileStore.Get(uid)
	if storedEndpoint == nil || !slices.Equal(storedEndpoint.AvailableModels, []string{"gpt-new", "manual-only"}) {
		t.Fatalf("刷新后的 endpoint 有效候选不正确: %+v", storedEndpoint)
	}
}

// TestAutoDiscoveryCheckpointRestoreConvergesCurrentKeyRules covers a resumed endpoint
// whose APIKeyConfig.Models changed while the discovery task was paused. The checkpoint
// keeps the upstream list, while the endpoint and model profiles must be rewritten using
// the current rules rather than only changing the transient discovery result.
func TestAutoDiscoveryCheckpointRestoreConvergesCurrentKeyRules(t *testing.T) {
	db := newTestDB(t)
	profileStore, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB: %v", err)
	}
	modelStore, err := NewModelProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewModelProfileStoreWithDB: %v", err)
	}
	const (
		channelUID = "ch-checkpoint-current-rules"
		baseURL    = "https://checkpoint.example.com"
		apiKey     = "sk-checkpoint-current-rules"
	)
	channel := config.UpstreamConfig{
		AccountUID:  "acct-checkpoint-current-rules",
		ChannelUID:  channelUID,
		ServiceType: "claude",
		BaseURL:     baseURL,
		APIKeys:     []string{apiKey},
		AutoManaged: true,
		APIKeyConfigs: []config.APIKeyConfig{{
			Key:    apiKey,
			// This is the current rule set; the checkpoint was created before
			// the old manual-only model was replaced by manual-new.
			Models: []string{"gpt-*", "manual-new"},
		}},
	}
	cfgManager, cleanup := createTestConfigManager(t, config.Config{Upstream: []config.UpstreamConfig{channel}})
	t.Cleanup(cleanup)

	runner := NewAutoDiscoveryRunner(profileStore, nil)
	runner.ModelProfileStore = modelStore
	endpointUID := GenerateEndpointUID(channelUID, utils.CanonicalBaseURL(baseURL, channel.ServiceType), KeyHashFromAPIKey(apiKey))
	metricsKey := computeMetricsIdentityKey(baseURL, apiKey, channel.ServiceType)
	// Seed an old persisted endpoint profile and a model profile that only the
	// current reconciliation can remove; a transient result-only update would
	// leave both stale rows behind.
	oldEndpoint := &KeyEndpointProfile{
		EndpointUID:    endpointUID,
		ChannelUID:     channelUID,
		ChannelKind:    "messages",
		ServiceType:    channel.ServiceType,
		BaseURL:        baseURL,
		MetricsKey:     metricsKey,
		KeyHash:        KeyHashFromAPIKey(apiKey),
		KeyMask:        utils.MaskAPIKey(apiKey),
		AvailableModels: []string{"legacy-model", "manual-old"},
	}
	if err := profileStore.Upsert(oldEndpoint); err != nil {
		t.Fatalf("seed endpoint profile: %v", err)
	}
	if err := modelStore.Upsert(&ModelProfile{
		ChannelUID:  channelUID,
		ChannelKind: "messages",
		ServiceType: channel.ServiceType,
		MetricsKey:  metricsKey,
		ModelID:     "legacy-model",
		ProbeSuccess: true,
		Source:       "auto_discovery",
	}); err != nil {
		t.Fatalf("seed stale model profile: %v", err)
	}
	if err := runner.flushStores(); err != nil {
		t.Fatalf("flush seed profiles: %v", err)
	}

	results := runner.discoverEndpointsWithCheckpoint(context.Background(), channelUID, &channel, cfgManager, []CheckpointedEndpoint{{
		EndpointUID: endpointUID,
		KeyHash:     KeyHashFromAPIKey(apiKey),
		BaseURL:     utils.CanonicalBaseURL(baseURL, channel.ServiceType),
		Models:      []string{"gpt-current", "manual-old", "claude-unlisted"},
		ModelsCount: 3,
		ProtocolOk:  true,
		ProfilePersisted: true,
	}})
	if len(results) != 1 {
		t.Fatalf("checkpoint 恢复结果数 = %d, want 1", len(results))
	}
	if got, want := results[0].Models, []string{"gpt-current"}; !slices.Equal(got, want) {
		t.Fatalf("恢复结果应按当前 Key 规则过滤上游清单: %v, want %v", got, want)
	}
	if got, want := results[0].manualModels, []string{"manual-new"}; !slices.Equal(got, want) {
		t.Fatalf("恢复结果应补入当前精确 allow: %v, want %v", got, want)
	}

	storedEndpoint := profileStore.Get(endpointUID)
	if storedEndpoint == nil || !slices.Equal(storedEndpoint.AvailableModels, []string{"gpt-current", "manual-new"}) {
		t.Fatalf("checkpoint 恢复必须持久化当前 endpoint 候选: %+v", storedEndpoint)
	}
	if got := modelStore.Get(channelUID, "messages", metricsKey, "legacy-model"); got != nil {
		t.Fatalf("当前规则收敛后不应保留旧模型画像: %+v", got)
	}
	if got := modelStore.Get(channelUID, "messages", metricsKey, "gpt-current"); got == nil || !got.ProbeSuccess || got.Source != "auto_discovery" {
		t.Fatalf("当前发现模型画像未持久化: %+v", got)
	}
	if got := modelStore.Get(channelUID, "messages", metricsKey, "manual-new"); got == nil || got.ProbeSuccess || got.Source != "manual" {
		t.Fatalf("当前精确 allow 的手动画像未持久化: %+v", got)
	}
	if err := runner.flushStores(); err != nil {
		t.Fatalf("flush restored profiles: %v", err)
	}
	reloadedEndpointStore, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("reload endpoint profiles: %v", err)
	}
	if got := reloadedEndpointStore.Get(endpointUID); got == nil || !slices.Equal(got.AvailableModels, []string{"gpt-current", "manual-new"}) {
		t.Fatalf("Flush 后 endpoint 未按当前规则持久收敛: %+v", got)
	}
	reloadedModelStore, err := NewModelProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("reload model profiles: %v", err)
	}
	if got := reloadedModelStore.Get(channelUID, "messages", metricsKey, "legacy-model"); got != nil {
		t.Fatalf("Flush 后旧模型画像仍存在: %+v", got)
	}
	if got := reloadedModelStore.Get(channelUID, "messages", metricsKey, "manual-new"); got == nil {
		t.Fatal("Flush 后当前手动模型画像丢失")
	}
}

// TestAutoDiscoveryReconcilePreservesEvidenceAcrossModelCasing verifies that a
// casing-only upstream rename keeps one stable ModelID and all strong probe evidence.
func TestAutoDiscoveryReconcilePreservesEvidenceAcrossModelCasing(t *testing.T) {
	db := newTestDB(t)
	profileStore, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB: %v", err)
	}
	modelStore, err := NewModelProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewModelProfileStoreWithDB: %v", err)
	}
	const (
		channelUID       = "ch-casing-model-identity"
		baseURL          = "https://casing.example.com"
		apiKey           = "sk-casing-model-identity"
		canonicalModelID = "GPT-Current"
	)
	channel := config.UpstreamConfig{
		ChannelUID:  channelUID,
		BaseURL:     baseURL,
		APIKeys:     []string{apiKey},
		ServiceType: "openai",
		AutoManaged: true,
	}
	metricsKey := computeMetricsIdentityKey(baseURL, apiKey, channel.ServiceType)
	probeAt := time.Date(2026, 9, 5, 12, 34, 56, 0, time.UTC)
	existing := &ModelProfile{
		ChannelUID:      channelUID,
		ChannelKind:     "messages",
		ServiceType:     channel.ServiceType,
		MetricsKey:      metricsKey,
		ModelID:         canonicalModelID,
		ProbeSuccess:    true,
		Source:          "capability_test",
		LastProbeAt:     probeAt,
		ProbeLatencyMs:  321,
		ProbeConfidence: 0.97,
	}
	if err := modelStore.Upsert(existing); err != nil {
		t.Fatalf("seed strong model evidence: %v", err)
	}
	runner := NewAutoDiscoveryRunner(profileStore, nil)
	runner.ModelProfileStore = modelStore
	// The upstream now reports only a casing variant of the same model.
	endpoint := EndpointDiscoveryResult{
		KeyMask: utils.MaskAPIKey(apiKey),
		BaseURL: baseURL,
		Models: []string{"gpt-current"},
		ModelsCount: 1,
		ProtocolOk: true,
		apiKey: apiKey,
	}
	if _, err := runner.writeProfileForEndpoint(channelUID, &channel, endpoint, 0, "messages", nil); err != nil {
		t.Fatalf("writeProfileForEndpoint: %v", err)
	}
	got := modelStore.Get(channelUID, "messages", metricsKey, canonicalModelID)
	if got == nil {
		t.Fatal("应按既有 canonical ModelID 命中画像")
	}
	if got.ModelID != canonicalModelID {
		t.Fatalf("ModelID = %q, want canonical %q", got.ModelID, canonicalModelID)
	}
	if !got.ProbeSuccess || got.Source != existing.Source ||
		!got.LastProbeAt.Equal(probeAt) || got.ProbeLatencyMs != existing.ProbeLatencyMs ||
		got.ProbeConfidence != existing.ProbeConfidence {
		t.Fatalf("casing 变化不应丢失既有强证据: %+v", got)
	}
	profiles := modelStore.ListByChannel(channelUID)
	if len(profiles) != 1 || profiles[0].ModelID != canonicalModelID {
		t.Fatalf("casing 变化后应只保留 canonical 画像: %+v", profiles)
	}
	if got := modelStore.Get(channelUID, "messages", metricsKey, "gpt-current"); got != nil {
		t.Fatalf("不应创建 casing 变化的第二个画像键: %+v", got)
	}
}

func TestEndpointPolicyAllowsOnlyExplicitManualModelRules(t *testing.T) {
	profile := &KeyEndpointProfile{AvailableModels: []string{"discovered"}}

	manual := config.APIKeyConfig{Models: []string{"manual-only", "manual-*", "!manual-blocked"}}
	if !profileSupportsRequestModel(profile, "manual-only", nil, nil, &manual) {
		t.Fatal("精确 allow 规则应放行精确请求")
	}
	if profileSupportsRequestModel(profile, "manual-wildcard-only", nil, nil, &manual) {
		t.Fatal("通配 allow 只能过滤已发现候选，不得补入未知模型")
	}
	if profileSupportsRequestModel(profile, "manual-blocked", nil, nil, &manual) {
		t.Fatal("deny 规则应优先排除精确请求")
	}

	denyOnly := config.APIKeyConfig{Models: []string{"!blocked"}}
	if profileSupportsRequestModel(profile, "unlisted", nil, nil, &denyOnly) {
		t.Fatal("纯 deny 规则不得凭空补入未发现模型")
	}

	legacyEmptyProfile := &KeyEndpointProfile{}
	if profileSupportsRequestModel(legacyEmptyProfile, "unlisted", nil, nil, &manual) {
		t.Fatal("当前 include 规则必须约束尚无模型清单的旧画像")
	}
	if profileSupportsRequestModel(legacyEmptyProfile, "manual-blocked", nil, nil, &manual) {
		t.Fatal("当前 deny 规则必须约束尚无模型清单的旧画像")
	}
	if !profileSupportsRequestModel(legacyEmptyProfile, "manual-only", nil, nil, &manual) {
		t.Fatal("旧画像仍应放行当前精确 positive allow")
	}
}

func TestModelResolverManualModelRequiresSuccessOnlyForSubstitution(t *testing.T) {
	manual := makeModelProfile("manual-only", ModelFamilyClaude, QualityTierHigh, 200000,
		true, true, true, false, 0)
	manual.Source = "manual"
	resolver := newTestResolver(t, []ModelProfile{manual})

	if target, ok, reason := resolver.ResolveModel("manual-only", "ch_test", "messages", "metrics_test", CapabilityFloor{}); !ok || target.Model != "manual-only" || reason != "found_exact_manual_model" {
		t.Fatalf("精确手动模型应可路由: target=%+v ok=%v reason=%q", target, ok, reason)
	}
	if target, ok, reason := resolver.ResolveModelAnyEndpoint("manual-only", "ch_test", "messages"); !ok || target.Model != "manual-only" || reason != "found_exact_manual_model" {
		t.Fatalf("渠道级精确手动模型应可路由: target=%+v ok=%v reason=%q", target, ok, reason)
	}
	if target, ok, reason := resolver.ResolveModelAnyEndpointWithFloor("manual-only", "ch_test", "messages", CapabilityFloor{NeedsVision: true}); !ok || target.Model != "manual-only" || reason != "found_exact_manual_model" {
		t.Fatalf("渠道级能力下界入口也应保留精确手动路由: target=%+v ok=%v reason=%q", target, ok, reason)
	}
	if got := resolver.ResolveModelsAnyEndpointWithFloor("claude-sonnet-5", "ch_test", "messages", CapabilityFloor{}, 0); len(got) != 0 {
		t.Fatalf("未经成功实证的手动模型不得进入渠道级枚举: %+v", got)
	}
	if target, ok, _ := resolver.ResolveModel("claude-sonnet-5", "ch_test", "messages", "metrics_test", CapabilityFloor{}); ok || target.Model != "claude-sonnet-5" {
		t.Fatalf("未经成功实证的手动模型不得跨模型替代: target=%+v ok=%v", target, ok)
	}

	manual.ProbeSuccess = true
	verifiedResolver := newTestResolver(t, []ModelProfile{manual})
	if target, ok, _ := verifiedResolver.ResolveModel("claude-sonnet-5", "ch_test", "messages", "metrics_test", CapabilityFloor{}); !ok || target.Model != "manual-only" {
		t.Fatalf("成功实证后应沿用既有候选链路: target=%+v ok=%v", target, ok)
	}
	if got := verifiedResolver.ResolveModelsAnyEndpointWithFloor("claude-sonnet-5", "ch_test", "messages", CapabilityFloor{}, 0); len(got) != 1 || got[0].profile.ModelID != "manual-only" {
		t.Fatalf("成功实证后应进入渠道级枚举: %+v", got)
	}
}

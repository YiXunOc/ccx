package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// 协议端点学习闭环（路由侧读取）的回归测试。
// 写入侧识别器见 handlers/common/protocol_unsupported_signal_test.go。

func TestLearnedProtocolUnsupported(t *testing.T) {
	orig := learnedProtocolUnsupportedLookup
	defer func() { learnedProtocolUnsupportedLookup = orig }()

	learned := map[string]bool{}
	learnedProtocolUnsupportedLookup = func(channelUID, protocol, model string) bool {
		return learned[channelUID+"|"+protocol+"|"+model]
	}

	if learnedProtocolUnsupported("ch_a", "responses", "m1") {
		t.Fatal("无记忆时应 fail-open 返回 false")
	}
	if learnedProtocolUnsupported("", "responses", "m1") ||
		learnedProtocolUnsupported("ch_a", "", "m1") ||
		learnedProtocolUnsupported("ch_a", "responses", "") {
		t.Fatal("空参数应直接返回 false")
	}

	learned["ch_a|responses|m1"] = true
	if !learnedProtocolUnsupported("ch_a", "responses", "m1") {
		t.Fatal("有记忆时应返回 true")
	}
	// 协议维隔离：同模型 chat 端点不受 responses 结论影响
	if learnedProtocolUnsupported("ch_a", "chat", "m1") {
		t.Fatal("其他协议端点不应命中")
	}
	if learnedProtocolUnsupported("ch_a", "responses", "m2") {
		t.Fatal("其他模型不应命中")
	}
}

func TestFilterLearnedToolCallCapable(t *testing.T) {
	orig := learnedToolCallUnsupportedLookup
	defer func() { learnedToolCallUnsupportedLookup = orig }()
	learnedToolCallUnsupportedLookup = func(channelUID, model string) bool {
		return channelUID == "ch_a" && model == "bad-model"
	}

	profiles := []ModelProfile{
		{ModelID: "bad-model"},
		{ModelID: "good-model"},
		{ModelID: "also-good"},
	}
	got := filterLearnedToolCallCapable(profiles, "ch_a")
	if len(got) != 2 || got[0].ModelID != "good-model" || got[1].ModelID != "also-good" {
		t.Fatalf("应剔除 bad-model，保留其余，got %v", got)
	}

	all := []ModelProfile{{ModelID: "bad-model"}}
	if got := filterLearnedToolCallCapable(all, "ch_a"); len(got) != 0 {
		t.Fatalf("全部剔除时应为空，got %v", got)
	}
}

// ── 工具调用正向白名单（运行期证据）──

func TestFilterLearnedToolCallCapableWhitelistMode(t *testing.T) {
	origUnsupported := learnedToolCallUnsupportedLookup
	origVerified := verifiedToolCallModelsLookup
	defer func() {
		learnedToolCallUnsupportedLookup = origUnsupported
		verifiedToolCallModelsLookup = origVerified
	}()
	learnedToolCallUnsupportedLookup = func(channelUID, model string) bool {
		return channelUID == "ch_a" && model == "bad-model"
	}

	profiles := []ModelProfile{
		{ModelID: "bad-model"},   // 黑名单
		{ModelID: "plain-model"}, // 无记录（fail-open 基线：保留）
		{ModelID: "Good-Model"},  // 大小写归一验证
	}

	// 无白名单记录：黑名单模式（fail-open，不堵冷启动渠道）
	verifiedToolCallModelsLookup = func(string) map[string]bool { return nil }
	got := filterLearnedToolCallCapable(profiles, "ch_a")
	if len(got) != 2 || got[0].ModelID != "plain-model" || got[1].ModelID != "Good-Model" {
		t.Fatalf("无白名单时应保持黑名单模式，got %v", got)
	}

	// 白名单存在：候选只从验证组合产生（大小写不敏感），黑名单/未验证全部出局
	verifiedToolCallModelsLookup = func(string) map[string]bool {
		return map[string]bool{"good-model": true}
	}
	got = filterLearnedToolCallCapable(profiles, "ch_a")
	if len(got) != 1 || got[0].ModelID != "Good-Model" {
		t.Fatalf("白名单模式应只留验证组合，got %v", got)
	}

	// 验证组合与候选无交集：回退黑名单逻辑，不空转
	verifiedToolCallModelsLookup = func(string) map[string]bool {
		return map[string]bool{"other-model": true}
	}
	got = filterLearnedToolCallCapable(profiles, "ch_a")
	if len(got) != 2 {
		t.Fatalf("白名单无交集时应回退黑名单模式，got %v", got)
	}
}

// ── 路由间排他（白名单路由集合，按执行协议独立判定）──

func TestVerifiedToolCallRouteExclusivity(t *testing.T) {
	origModels := verifiedToolCallModelsLookup
	origRoutes := verifiedToolCallRoutesLookup
	defer func() {
		verifiedToolCallModelsLookup = origModels
		verifiedToolCallRoutesLookup = origRoutes
	}()

	profiles := []ModelProfile{{ModelID: "some-model"}}

	// 该协议无白名单路由：fail-open，一切照旧（黑名单逻辑）
	verifiedToolCallRoutesLookup = func(string) map[string]bool { return nil }
	verifiedToolCallModelsLookup = func(string) map[string]bool { return nil }
	if got := filterLearnedToolCallCapable(profiles, "lc_any#responses"); len(got) != 1 {
		t.Fatalf("无白名单路由时应 fail-open，got %v", got)
	}
}

// ── 稳定路由身份：画像侧物理 UID 翻译与跨重铸存活 ──

// resolver 的身份翻译：画像携带当前物理 ch_，须翻译为逻辑锚路由身份；
// 渠道已从 config 消失（幽灵 UID）时回退原始值（查询自然 miss，fail-open）。
// 逻辑 UID 不硬编码——加载迁移会重建逻辑卡，从加载后的 config 动态读取。
func TestToolRouteIdentityTranslation(t *testing.T) {
	cfg := config.Config{
		ResponsesUpstream: []config.UpstreamConfig{
			{ChannelUID: "ch_today", AccountUID: "acct_test", BaseURL: "https://example.com", ServiceType: "responses", Name: "ark"},
		},
	}
	resolver := newTestResolverWithConfig(t, nil, cfg)

	logicalUID := resolver.cfgManager.GetConfig().ResponsesUpstream[0].LogicalChannelUID
	if logicalUID == "" {
		t.Fatal("加载迁移应已为测试渠道回填逻辑 UID")
	}
	if got := resolver.toolRouteIdentity("ch_today", "responses"); got != logicalUID+"#responses" {
		t.Fatalf("当前物理 UID 应翻译为逻辑路由身份 %q，got %q", logicalUID+"#responses", got)
	}
	if got := resolver.toolRouteIdentity("ch_ghost", "responses"); got != "ch_ghost" {
		t.Fatalf("幽灵 UID 应回退原始值（fail-open），got %q", got)
	}
	if got := resolver.toolRouteIdentity("", "responses"); got != "" {
		t.Fatalf("空 UID 应返回空，got %q", got)
	}
	// 无 cfgManager 的 resolver 一律回退原始值
	bare := newTestResolver(t, nil)
	if got := bare.toolRouteIdentity("ch_today", "responses"); got != "ch_today" {
		t.Fatalf("无 cfgManager 应回退原始值，got %q", got)
	}
}

// 跨重铸存活（本修复的核心回归锁）：旧代物理渠道上学习的白名单，经逻辑路由身份
// 在新代物理渠道上继续生效——resolver 主路径过滤不因 UID 重铸而 miss。
// 根因背景：2026-09-12 白名单种子种在 7 月代幽灵 UID（ch_july）上，四层查询
// 全部以当前 UID（ch_today）发起，端到端全部 miss。
// 两代渠道用同账号归组到同一逻辑卡（convergeLogicalByAccount），模拟重铸。
func TestFilterLearnedToolCallCapableSurvivesUIDRemint(t *testing.T) {
	restore := config.SwapSharedChannelCompatCacheForTest(config.NewChannelCompatCache())
	defer restore()

	cfg := config.Config{
		ResponsesUpstream: []config.UpstreamConfig{
			{ChannelUID: "ch_july", AccountUID: "acct_ark", BaseURL: "https://ark.example.com", ServiceType: "responses", Name: "ark-gen1"},
			{ChannelUID: "ch_today", AccountUID: "acct_ark", BaseURL: "https://ark.example.com", ServiceType: "responses", Name: "ark-gen2"},
		},
	}
	resolver := newTestResolverWithConfig(t, nil, cfg)

	loaded := resolver.cfgManager.GetConfig().ResponsesUpstream
	gen1, gen2 := loaded[0], loaded[1]
	if gen1.LogicalChannelUID == "" || gen1.LogicalChannelUID != gen2.LogicalChannelUID {
		t.Fatalf("同账号两代渠道应归组到同一逻辑卡，got %q vs %q", gen1.LogicalChannelUID, gen2.LogicalChannelUID)
	}

	// 旧代渠道（将被重铸淘汰）上写入运行期验证证据
	cache := config.SharedChannelCompatCache()
	cache.Record(config.ToolRouteIdentity(&gen1, "responses"), "keyhash1", "kimi-k3",
		config.TraitVerifiedToolCalls, true, config.CompatSourceRuntimeSignal, "真实 function_call")

	// 新代渠道的 resolver 主路径过滤
	profiles := []ModelProfile{
		{ModelID: "glm-5.3-flash"}, // 未验证组合
		{ModelID: "Kimi-K3"},       // 验证组合（大小写归一）
	}
	got := filterLearnedToolCallCapable(profiles, resolver.toolRouteIdentity(gen2.ChannelUID, "responses"))
	if len(got) != 1 || got[0].ModelID != "Kimi-K3" {
		t.Fatalf("重铸后白名单应继续只放行验证组合，got %v", got)
	}
}

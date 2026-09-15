package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// sseHandler 返回固定 SSE 响应的上游桩。
func sseHandler(t *testing.T, status int, payload string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		if _, err := w.Write([]byte(payload)); err != nil {
			t.Errorf("写 SSE 响应失败: %v", err)
		}
	}))
}

// messages 强制 tool_choice 探针返回工具调用 → Supported=true。
func TestRunCapabilityToolCallProbeSupported(t *testing.T) {
	server := sseHandler(t, http.StatusOK,
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"t1\",\"name\":\"ccx_probe\",\"input\":{}}}\n\n"+
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n")
	defer server.Close()

	channel := &config.UpstreamConfig{BaseURL: server.URL, ServiceType: "claude"}
	summary := runCapabilityToolCallProbe(context.Background(), channel, "messages", "probe-model", "sk-test")

	if !summary.Tested || !summary.Supported {
		t.Fatalf("Supported = %v (Tested=%v, evidence=%s), want true", summary.Supported, summary.Tested, summary.Evidence)
	}
}

// messages 探针返回有效文本但无工具调用 → Supported=false（唯一可学习结论）。
func TestRunCapabilityToolCallProbeUnsupported(t *testing.T) {
	server := sseHandler(t, http.StatusOK,
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"I cannot call tools.\"}}\n\n")
	defer server.Close()

	channel := &config.UpstreamConfig{BaseURL: server.URL, ServiceType: "claude"}
	summary := runCapabilityToolCallProbe(context.Background(), channel, "messages", "probe-model", "sk-test")

	if !summary.Tested || summary.Supported {
		t.Fatalf("Supported = %v (Tested=%v), want false", summary.Supported, summary.Tested)
	}
}

// 非 2xx → inconclusive，不学习。
func TestRunCapabilityToolCallProbeUpstreamErrorInconclusive(t *testing.T) {
	server := sseHandler(t, http.StatusServiceUnavailable, `{"error":{"message":"overloaded"}}`)
	defer server.Close()

	channel := &config.UpstreamConfig{BaseURL: server.URL, ServiceType: "claude"}
	summary := runCapabilityToolCallProbe(context.Background(), channel, "messages", "probe-model", "sk-test")

	if !summary.Tested || summary.Supported {
		t.Fatalf("Supported = %v (Tested=%v), want inconclusive (Tested=true, Supported=false)", summary.Supported, summary.Tested)
	}
}

// 不支持的协议 → Tested=false，不发送任何请求。
func TestRunCapabilityToolCallProbeUnsupportedProtocol(t *testing.T) {
	summary := runCapabilityToolCallProbe(context.Background(), &config.UpstreamConfig{BaseURL: "http://127.0.0.1:1"}, "vectors", "m", "k")
	if summary.Tested {
		t.Fatal("vectors 协议不应执行工具探针")
	}
}

// recordToolCallProbeResult：仅 Tested && !Supported 落库，且写入共享缓存。
// 键为稳定路由身份（无逻辑 UID 的渠道回退物理 UID#协议）。
func TestRecordToolCallProbeResult(t *testing.T) {
	restore := config.SwapSharedChannelCompatCacheForTest(config.NewChannelCompatCache())
	defer restore()

	channel := &config.UpstreamConfig{ChannelUID: "ch_probe", Name: "probe"}
	unsupported := ToolCallProbeSummary{Tested: true, Supported: false, ConfirmedUnsupported: true, Evidence: "有效内容但无工具调用"}
	recordToolCallProbeResult(channel, "sk-test", "fake-model", "responses", unsupported)

	cache := config.SharedChannelCompatCache()
	if !cache.IsToolCallUnsupportedForChannelModel("ch_probe#responses", "fake-model") {
		t.Fatal("不支持结论应按路由身份写入共享兼容性记忆")
	}
	// 裸物理 UID（无协议维度的旧口径查询）不应命中——协议维隔离是设计语义
	if cache.IsToolCallUnsupportedForChannelModel("ch_probe", "fake-model") {
		t.Fatal("裸 UID 查询不应命中路由身份键")
	}

	// Supported=true 与 inconclusive 不落库
	supported := ToolCallProbeSummary{Tested: true, Supported: true}
	recordToolCallProbeResult(channel, "sk-test", "ok-model", "responses", supported)
	inconclusive := ToolCallProbeSummary{Tested: true, Supported: false, Error: "timeout"}
	recordToolCallProbeResult(channel, "sk-test", "timeout-model", "responses", inconclusive)
	if cache.IsToolCallUnsupportedForChannelModel("ch_probe#responses", "ok-model") {
		t.Fatal("支持结论不应落库")
	}
	if cache.IsToolCallUnsupportedForChannelModel("ch_probe#responses", "timeout-model") {
		t.Fatal("inconclusive 结论不应落库")
	}
}

// 跨渠道 UID 重铸的存活回归：物理 UID 会随渠道重建/账号同步被重铸（2026-09 ark
// 两个月 ≥5 代，种子曾种在 7 月代幽灵 UID 上全 miss），工具能力学习必须锚定逻辑
// 渠道 UID 才能在重铸后继续命中——旧代渠道写入的证据，新代渠道（同逻辑卡）可查。
func TestRecordToolCallProbeResultSurvivesUIDRemint(t *testing.T) {
	restore := config.SwapSharedChannelCompatCacheForTest(config.NewChannelCompatCache())
	defer restore()

	gen1 := &config.UpstreamConfig{ChannelUID: "ch_july", LogicalChannelUID: "lc_stable", Name: "ark-gen1"}
	gen2 := &config.UpstreamConfig{ChannelUID: "ch_today", LogicalChannelUID: "lc_stable", Name: "ark-gen2"}

	supported := ToolCallProbeSummary{Tested: true, Supported: true, Evidence: "真实 function_call"}
	recordToolCallProbeResult(gen1, "sk-test", "kimi-k3", "responses", supported)

	cache := config.SharedChannelCompatCache()
	identity := config.ToolRouteIdentity(gen2, "responses")
	if identity != "lc_stable#responses" {
		t.Fatalf("新代渠道的路由身份应为逻辑锚，got %s", identity)
	}
	verified := cache.VerifiedToolCallModelsForChannel(identity, false)
	if !verified["kimi-k3"] {
		t.Fatal("同逻辑卡新代物理渠道应命中旧代写入的验证记录")
	}
	routes := cache.VerifiedToolCallRoutes("responses", false)
	if !routes["lc_stable#responses"] {
		t.Fatal("排他集合应包含逻辑路由身份")
	}
	// 协议维隔离：responses 证据不得外溢到 messages 排他集合
	if ms := cache.VerifiedToolCallRoutes("messages", false); len(ms) != 0 {
		t.Fatalf("messages 集合不应包含 responses 证据，got %v", ms)
	}
	// 旧代物理 UID 直接查询（无逻辑锚的口径）自然 miss，不误伤
	if cache.VerifiedToolCallModelsForChannel("ch_july", false) != nil {
		t.Fatal("裸物理 UID 查询不应命中路由身份键")
	}
}

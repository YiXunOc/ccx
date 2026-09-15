package common

import (
	"time"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/racing"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// ── 延迟负反馈学习：渠道×Key×模型×任务类 组合的慢证据累积 → 调度软降权 ──
//
// 三个慢信号源（均为无歧义证据）：
//  1. 竞速 primary 被影子击败（另一候选同请求更快交付）；
//  2. 竞速触发本身（首字等待超过同家族 p90 自适应阈值）；
//  3. 普通流式请求首字超同家族阈值（竞速未开启/未武装时的持续观测）。
//
// 连续慢证据 streak ≥ 3 才降权（单次慢是抖动）；任一快样本乐观翻转清零。
// 存储：ChannelCompatCache.latencyPenalties 分区（24h TTL，落盘 channel_compat.json）。
// 红线：竞速败出/被取消分支不学习（部分流的时序不代表组合真实延迟）。

// requestTaskClassForLatency 从请求 context 取 TaskClass（未分类返回空串，
// 存储侧统一落 unknown 桶）。
func requestTaskClassForLatency(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	if profile, ok := autopilot.RequestProfileFromContext(c.Request.Context()); ok {
		return string(profile.TaskClass)
	}
	return ""
}

// recordLatencySlowEvidence 记一次慢证据。跨过降权阈值时打一行日志（首次劣化可观测）。
func recordLatencySlowEvidence(c *gin.Context, channelUID, apiKey, model, signal string, firstByteMs int64) {
	if channelUID == "" || model == "" || apiKey == "" {
		return
	}
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return
	}
	keyHash := autopilot.KeyHashFromAPIKey(apiKey)
	if cache.RecordSlowEvidence(channelUID, keyHash, model, requestTaskClassForLatency(c), firstByteMs, time.Now()) {
		RequestLogf(c, "[Latency-Learn] 渠道 %s 模型 %s (taskClass=%s) 连续 %d 次慢证据（%s, 最近首字 %dms），调度降权生效",
			channelUID, model, requestTaskClassForLatency(c), config.LatencySlowStreakThreshold(), signal, firstByteMs)
	}
}

// RecordLatencyFastEvidenceFromSuccess 成功请求的快样本翻转：
// 首字显著低于该家族自适应阈值时清零 streak（渠道恢复即回升）。
func RecordLatencyFastEvidenceFromSuccess(c *gin.Context, channelUID, apiKey, model string) {
	if channelUID == "" || model == "" || apiKey == "" {
		return
	}
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return
	}
	observer := GetStreamTimeoutObserver(c)
	firstByteMs := observer.FirstContentMs()
	if firstByteMs <= 0 {
		return // 非流式/未观测首字：无快慢结论，不学习
	}
	if !isLatencyFastForModel(model, firstByteMs) {
		return // 不够快不算恢复证据
	}
	keyHash := autopilot.KeyHashFromAPIKey(apiKey)
	if cache.RecordFastEvidence(channelUID, keyHash, model, requestTaskClassForLatency(c), time.Now()) {
		RequestLogf(c, "[Latency-Recover] 渠道 %s 模型 %s (taskClass=%s) 快样本翻转，调度降权解除",
			channelUID, model, requestTaskClassForLatency(c))
	}
}

// MaybeLearnLatencyDegradation 普通流式成功请求的首字观测学习：
// 首字超过同家族自适应阈值记慢证据，显著低于阈值记快样本。
// 挂载点与 MaybeLearnSeverityClassOutcome 同点（handleSuccess 成功路径）。
// 竞速败出/被取消分支不学习。
func MaybeLearnLatencyDegradation(c *gin.Context, channelUID, apiKey, model string, superseded bool) {
	if superseded || channelUID == "" || model == "" || apiKey == "" {
		return
	}
	observer := GetStreamTimeoutObserver(c)
	firstByteMs := observer.FirstContentMs()
	if firstByteMs <= 0 {
		return
	}
	if isLatencySlowForModel(model, firstByteMs) {
		recordLatencySlowEvidence(c, channelUID, apiKey, model, "first_content_over_threshold", firstByteMs)
		return
	}
	if isLatencyFastForModel(model, firstByteMs) {
		RecordLatencyFastEvidenceFromSuccess(c, channelUID, apiKey, model)
	}
}

// recordPrimaryRacingTriggerEvidence 慢证据信号二：竞速触发（首字超家族阈值）。
// 主分支 apiKey 用 selection 的 key 身份 pin 反查；反查不到即跳过本信号——
// 不得 fallback 到 APIKeys[0] 把慢证据记到未参与请求的 keyHash 上
// （败出豁免点还会用精确 attempt key 记一次更准的）。
func recordPrimaryRacingTriggerEvidence(c *gin.Context, selection *scheduler.SelectionResult, requestModel string) {
	if selection == nil || selection.Upstream == nil {
		return
	}
	apiKey := ""
	if selection.ExecutionKeyIdentity != "" {
		apiKey = autopilot.ResolvePinnedAPIKey(selection.Upstream, selection.ExecutionKeyIdentity)
	}
	model := selection.ExecutionModel
	if model == "" {
		model = requestModel
	}
	// firstByteMs 传 0：此信号的时间值即阈值本身，LastFirstByteMs 由信号三补充更准的观测。
	recordLatencySlowEvidence(c, selection.Upstream.ChannelUID, apiKey, model, "racing_triggered", 0)
}

// RecordLatencySupersededEvidence 慢证据信号一：竞速 primary 被影子击败。
// 由败者豁免点（upstream_failover）调用——那里有精确的 attempt apiKey 与实际出站模型，
// 比编排器层反查更可靠。
func RecordLatencySupersededEvidence(c *gin.Context, channelUID, apiKey, model string) {
	recordLatencySlowEvidence(c, channelUID, apiKey, model, "racing_lost", GetStreamTimeoutObserver(c).FirstContentMs())
}

// isLatencySlowForModel 首字是否超过该模型家族的自适应阈值（竞速注册表同源）。
func isLatencySlowForModel(model string, firstByteMs int64) bool {
	hub := getRacingHub()
	if hub == nil || hub.Registry == nil {
		return false
	}
	behavior := racing.BehaviorForCostPreference("balanced")
	threshold := hub.Registry.ThresholdMs(racing.FamilyForModel(model), racing.StageStreamFirstContent, behavior.StreamFloorMs, 600000)
	return firstByteMs > int64(threshold)
}

// isLatencyFastForModel 首字是否显著低于家族阈值（半阈值以下视为明确快证据，
// 防止在阈值附近的抖动反复翻转）。
func isLatencyFastForModel(model string, firstByteMs int64) bool {
	hub := getRacingHub()
	if hub == nil || hub.Registry == nil {
		return false
	}
	behavior := racing.BehaviorForCostPreference("balanced")
	threshold := hub.Registry.ThresholdMs(racing.FamilyForModel(model), racing.StageStreamFirstContent, behavior.StreamFloorMs, 600000)
	return firstByteMs*2 < int64(threshold)
}

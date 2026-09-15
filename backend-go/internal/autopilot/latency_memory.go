package autopilot

import (
	"github.com/BenedictKing/ccx/internal/config"
)

// learnedLatencyDegradedLookup 供测试替换的查询入口（同 severity_class_memory 模式）。
var learnedLatencyDegradedLookup = func(channelUID, model, taskClass string) bool {
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return false
	}
	return cache.IsLatencyDegraded(channelUID, model, taskClass)
}

// learnedLatencyDegraded 返回该渠道×模型×任务类组合是否被学习为延迟劣化
// （任一 Key 的连续慢证据达阈值，保守口径与 learnedSeverityClassUnsupported 一致）。
// 无记忆时 fail-open 返回 false。软降权而非硬排除：延迟差不是能力缺失。
func learnedLatencyDegraded(channelUID, model string, taskClass TaskClass) bool {
	if channelUID == "" || model == "" {
		return false
	}
	return learnedLatencyDegradedLookup(channelUID, model, string(taskClass))
}

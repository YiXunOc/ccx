package autopilot

import (
	"strings"
)

// racingShadowExcludesLookup 供测试替换的慢组合排除查询（默认查延迟负反馈学习结论）。
var racingShadowExcludesLookup = func(channelUID, model string) bool {
	return learnedLatencyDegradedLookup(channelUID, model, "")
}

// RacingShadowCandidates 从 SmartRouter 排名缓存中选取竞速影子候选。
//
// 同渠道行一律不作影子（2026-09-12 决策，与 ABTest 的 selectShadowCandidates
// 整渠道排除对齐）：同渠道影子是同 provider 同队列的重复消耗——key 级对冲已由
// attempt 内部轮转覆盖，模型绑定又经工具白名单终审收敛到同一验证模型，跨 key /
// 跨绑定的「多样性」执行结果完全等价，纯属放大流量成本，对延迟/可用性无实质改善。
//
// 保留纪律（kiro.rs 原版对齐，2026-09-08 生产事故补齐）：
//  1. 主 key 一律不选（同一明文 key 跨渠道重复配置时，与主分支并发同账号请求
//     是放大器：限速/空流）；
//  2. 多影子之间按 keyIdentity 分散（同渠道多 key 行入选时，同 key 只取一行）。
//
// 返回按排名原序至多 limit 条。
func RacingShadowCandidates(candidates []RoutingCandidate, primaryChannelUID, primaryKeyIdentity string, limit int) []RoutingCandidate {
	if limit <= 0 || len(candidates) == 0 {
		return nil
	}
	pool := make([]RoutingCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		// Selected 语义 = 通过当前模式硬约束的可行候选（可为多个），主调度选中行只是其中之一；
		// 影子必须从可行集选取，不可行候选（FilterReasons 非空）不得作影子。
		if !candidate.Selected || candidate.ChannelUID == primaryChannelUID {
			continue
		}
		// 主 key 一律不作影子：与主分支并发同账号请求是放大器（限速/空流）。
		if strings.TrimSpace(candidate.KeyIdentity) != "" &&
			strings.TrimSpace(candidate.KeyIdentity) == strings.TrimSpace(primaryKeyIdentity) {
			continue
		}
		// 延迟负反馈学习：已学习为慢的组合不作影子目标——已知慢的候选救不了另一个慢的。
		if racingShadowExcludesLookup(candidate.ChannelUID, candidate.ActualModel) {
			continue
		}
		pool = append(pool, candidate)
	}
	if len(pool) == 0 {
		return nil
	}
	// 多影子按 keyIdentity 去重（同 key 只取一行），保持排名原序。
	selected := make([]RoutingCandidate, 0, limit)
	seenKeys := make(map[string]bool)
	for _, candidate := range pool {
		keyID := strings.TrimSpace(candidate.KeyIdentity)
		if keyID != "" {
			if seenKeys[keyID] {
				continue
			}
			seenKeys[keyID] = true
		}
		selected = append(selected, candidate)
		if len(selected) >= limit {
			break
		}
	}
	return selected
}

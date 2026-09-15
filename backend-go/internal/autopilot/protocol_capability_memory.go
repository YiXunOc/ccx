package autopilot

import "github.com/BenedictKing/ccx/internal/config"

// 协议端点不支持结论在模型映射侧的读取。
//
// 写入方是 internal/handlers/common 的 failover 错误路径（上游 400 明确报
// model_not_supported_on_endpoint 或等价文案），读取方是 ModelResolver 的候选
// 过滤（probedModelsAnyEndpoint）。两侧通过 config.SharedChannelCompatCache
// 共享同一实例：autopilot 不能 import handlers（依赖方向相反），config 是双方
// 共同的下层依赖。学习口径与读写约定见 protocol_unsupported_signal.go 文件头。

// learnedProtocolUnsupportedLookup 供测试替换的查询入口。
// 生产实现读共享兼容性记忆；测试里替换成内存桩，避免依赖落盘状态。
var learnedProtocolUnsupportedLookup = func(channelUID, protocol, model string) bool {
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return false
	}
	return cache.IsProtocolUnsupportedForChannelModel(channelUID, protocol, model)
}

// learnedProtocolUnsupported 返回该渠道-模型在指定执行协议端点是否已学到
// 「不可用」结论（no_protocol_support:<protocol>）。
//
// 为什么任一 Key 命中即不可用：与工具调用记忆同口径——映射决策发生在选定
// 具体 Key 之前，同渠道不同 Key 背后可能是不同上游，任一 Key 已实测拒绝就
// 按不可用处理是保守的。
//
// fail-open：无任何记忆时返回 false，不额外限制新组合。
func learnedProtocolUnsupported(channelUID, protocol, model string) bool {
	if channelUID == "" || protocol == "" || model == "" {
		return false
	}
	return learnedProtocolUnsupportedLookup(channelUID, protocol, model)
}

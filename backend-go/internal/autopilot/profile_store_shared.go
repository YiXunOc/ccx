package autopilot

import (
	"sort"
	"strings"
	"sync"
)

// 共享 ProfileStore 单例（对齐 config.SharedChannelCompatCache 模式）。
//
// ProfileStore 由 main.go 创建并注入 SmartRouter / AutoDiscovery 等组件；
// handlers 侧的能力测试需要读画像的 protocolModels（探测范围对齐）但不在
// 注入链上，穿参会波及全部协议路由注册。此处以包级注册暴露只读访问。

var (
	sharedProfileStoreMu sync.RWMutex
	sharedProfileStore   *ProfileStore
)

// SetSharedProfileStore 注册全局共享 ProfileStore（main.go 初始化后调用一次）。
func SetSharedProfileStore(store *ProfileStore) {
	sharedProfileStoreMu.Lock()
	sharedProfileStore = store
	sharedProfileStoreMu.Unlock()
}

// getSharedProfileStore 读取全局共享 ProfileStore（nil = 未注册/初始化失败）。
func getSharedProfileStore() *ProfileStore {
	sharedProfileStoreMu.RLock()
	defer sharedProfileStoreMu.RUnlock()
	return sharedProfileStore
}

// protocolModelAliases 能力测试协议名到画像 protocolModels 键的别名
// （画像键是上游原生协议名；messages 协议在部分调用方以 "claude" 传入）。
var protocolModelAliases = map[string][]string{
	"claude": {"messages"},
	"openai": {"chat"},
}

// ProtocolModelsForChannel 聚合渠道下全部活跃 endpoint 画像在指定协议下
// 已验证的模型清单：任一 key 的画像登记过即纳入（并集，按首现保序去重）。
//
// 用途：能力测试的探测范围对齐。内置通用清单（gpt/claude 家族）对没有
// 对应模型家族的渠道天然全 MODEL_NOT_AVAILABLE，探不到任何真实结论；
// 画像 protocolModels 是发现层在该渠道×协议逐模型实测验证过的清单，
// 测它才能产出「能否对话 / 是否执行工具调用」的有效结论。
// store 为 nil（未注册）或画像为空时返回 nil，调用方回退既有清单。
func ProtocolModelsForChannel(store *ProfileStore, channelUID, protocol string) []string {
	if store == nil || channelUID == "" || protocol == "" {
		return nil
	}
	profiles := store.ListActiveByChannel(channelUID)
	if len(profiles) == 0 {
		return nil
	}
	// ListActiveByChannel 按缓存 map 迭代，顺序随机；「按首现保序去重」的口径
	// 要求多 key 渠道的聚合顺序确定（能力测试的探测清单顺序影响候选补位保序），
	// 先按 KeyHash 稳定排序再聚合。
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].KeyHash != profiles[j].KeyHash {
			return profiles[i].KeyHash < profiles[j].KeyHash
		}
		return profiles[i].EndpointUID < profiles[j].EndpointUID
	})
	seen := make(map[string]bool)
	var merged []string
	collect := func(models []string) {
		for _, m := range models {
			m = strings.TrimSpace(m)
			if m == "" || seen[m] {
				continue
			}
			seen[m] = true
			merged = append(merged, m)
		}
	}
	for _, p := range profiles {
		if p == nil {
			continue
		}
		collect(p.ProtocolModels[protocol])
	}
	// 别名兜底：直接键为空时按协议别名再聚合一轮（如 claude -> messages）。
	if len(merged) == 0 {
		for _, alias := range protocolModelAliases[protocol] {
			for _, p := range profiles {
				if p == nil {
					continue
				}
				collect(p.ProtocolModels[alias])
			}
		}
	}
	return merged
}

// SharedProtocolModelsForChannel 经共享单例读取（能力测试等未持有 store 的调用方）。
func SharedProtocolModelsForChannel(channelUID, protocol string) []string {
	return ProtocolModelsForChannel(getSharedProfileStore(), channelUID, protocol)
}

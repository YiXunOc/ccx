package config

import (
	"log"
	"sort"
	"strings"
	"time"
)

// latencyPenaltyTTL 慢证据有效期，对齐主 cache 的 24h：
// 延迟劣化通常是暂态（中转站排队/上游扩容前夜），到期自动恢复、重新学习，
// 避免一次拥挤的下午把组合永久打入冷宫。
const latencyPenaltyTTL = channelCompatTTL

// latencySlowStreakThreshold 判定「组合劣化」所需的连续慢证据次数：
// 单次慢可能是偶发抖动，3 次（竞速被击败/触发竞速/首字超阈值任一信号）才降权。
const latencySlowStreakThreshold = 3

// LatencySlowStreakThreshold 返回降权阈值（handlers 层观测日志用）。
func LatencySlowStreakThreshold() int { return latencySlowStreakThreshold }

// LatencyPenaltyState 渠道×Key×模型×任务类 粒度的慢证据累积（降权方向）。
//
// 与 traits（能力缺失布尔结论）、contextWindows（窗口棘轮）互补：
// 延迟差不是能力缺失（请求能成功，只是慢），故走软降权而非硬过滤；
// 连续快样本乐观翻转清零，渠道恢复后评分立即回升。
type LatencyPenaltyState struct {
	// SlowStreak 连续慢证据计数（快样本清零）。
	SlowStreak int `json:"slow_streak,omitempty"`
	// LastSlowAt 最近一次慢证据时间（TTL 判定基准）。
	LastSlowAt time.Time `json:"last_slow_at,omitempty"`
	// LastFirstByteMs 最近一次慢证据的首字耗时（诊断展示）。
	LastFirstByteMs int64 `json:"last_first_byte_ms,omitempty"`
	// LastFastAt 最近一次快样本时间（诊断展示；有 streak 时为旧值）。
	LastFastAt time.Time `json:"last_fast_at,omitempty"`
}

// latencyPenaltyKey 存储键：渠道|keyHash|模型|任务类（四段 "|"，与
// contextWindows 的三段键、entries 的 ":" 键空间错开；任务类空段以 "unknown" 占位）。
func latencyPenaltyKey(channelUID, keyHash, model, taskClass string) string {
	if strings.TrimSpace(taskClass) == "" {
		taskClass = "unknown"
	}
	return channelUID + "|" + keyHash + "|" + model + "|" + taskClass
}

func latencyPenaltyFresh(state *LatencyPenaltyState, now time.Time) bool {
	return state != nil && !state.LastSlowAt.IsZero() && now.Sub(state.LastSlowAt) <= latencyPenaltyTTL
}

// RecordSlowEvidence 记录一次慢证据（竞速被击败/竞速触发/首字超家族阈值）。
// streak 未达阈值前只累积不产生调度效果；返回本次是否跨过降权阈值（首次达 threshold）。
func (c *ChannelCompatCache) RecordSlowEvidence(channelUID, keyHash, model, taskClass string, firstByteMs int64, now time.Time) bool {
	if channelUID == "" || model == "" {
		return false
	}
	c.mu.Lock()
	key := latencyPenaltyKey(channelUID, keyHash, model, taskClass)
	state := c.latencyPenalties[key]
	if state == nil {
		state = &LatencyPenaltyState{}
		c.latencyPenalties[key] = state
	}
	crossed := false
	if !latencyPenaltyFresh(state, now) {
		// 过期重建：旧 streak 不延续，本次重新起算。
		crossed = latencySlowStreakThreshold <= 1
		state.SlowStreak = 1
	} else {
		state.SlowStreak++
		crossed = state.SlowStreak == latencySlowStreakThreshold
	}
	state.LastSlowAt = now
	if firstByteMs > 0 {
		state.LastFirstByteMs = firstByteMs
	}
	c.dirty = true
	// 防抖落盘：慢证据是请求热路径上的高频统计样本，不做每次同步写盘
	c.scheduleFlushLocked()
	c.mu.Unlock()

	return crossed
}

// RecordFastEvidence 记录一次快样本：乐观翻转，streak 清零（渠道恢复即回升）。
// 返回是否从降权态翻回（此前 streak 已达阈值）。
func (c *ChannelCompatCache) RecordFastEvidence(channelUID, keyHash, model, taskClass string, now time.Time) bool {
	if channelUID == "" || model == "" {
		return false
	}
	c.mu.Lock()
	key := latencyPenaltyKey(channelUID, keyHash, model, taskClass)
	state := c.latencyPenalties[key]
	if state == nil {
		c.mu.Unlock()
		return false
	}
	recovered := state.SlowStreak >= latencySlowStreakThreshold
	state.SlowStreak = 0
	state.LastFastAt = now
	c.dirty = true
	// 防抖落盘：同 RecordSlowEvidence，热路径不同步写盘
	c.scheduleFlushLocked()
	c.mu.Unlock()

	return recovered
}

// IsLatencyDegraded 渠道×模型×任务类 组合是否被学习为延迟劣化（跨 Key 聚合：
// 任一 Key 的未过期 streak ≥ 阈值即劣化——保守口径与 isTraitEnabledForChannelModel 一致，
// 同渠道不同 key 背后上游可能相同）。任务类匹配精确 taskClass 优先，其次 "unknown"
// 全量记录（未分类请求写入侧统一落 unknown，读取侧两类都查）。
func (c *ChannelCompatCache) IsLatencyDegraded(channelUID, model, taskClass string) bool {
	return c.IsLatencyDegradedAt(channelUID, model, taskClass, time.Now())
}

// IsLatencyDegradedAt 带时钟版本（测试注入用）。
func (c *ChannelCompatCache) IsLatencyDegradedAt(channelUID, model, taskClass string, now time.Time) bool {
	if channelUID == "" || model == "" {
		return false
	}
	if strings.TrimSpace(taskClass) == "" {
		taskClass = "unknown"
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	for key, state := range c.latencyPenalties {
		if !latencyPenaltyFresh(state, now) || state.SlowStreak < latencySlowStreakThreshold {
			continue
		}
		parts := strings.SplitN(key, "|", 4)
		if len(parts) != 4 {
			continue
		}
		if parts[0] != channelUID || !strings.EqualFold(parts[2], model) {
			continue
		}
		if parts[3] == taskClass || parts[3] == "unknown" {
			return true
		}
	}
	return false
}

// LatencyPenaltySnapshotEntry 管理端查看用的一条慢证据记录视图（复合键拆开）。
type LatencyPenaltySnapshotEntry struct {
	ChannelUID      string    `json:"channelUid"`
	KeyHash         string    `json:"keyHash"`
	Model           string    `json:"model"`
	TaskClass       string    `json:"taskClass"`
	SlowStreak      int       `json:"slowStreak"`
	Degraded        bool      `json:"degraded"`
	LastSlowAt      time.Time `json:"lastSlowAt,omitempty"`
	LastFirstByteMs int64     `json:"lastFirstByteMs,omitempty"`
	LastFastAt      time.Time `json:"lastFastAt,omitempty"`
}

// LatencyPenaltySnapshot 返回全部延迟慢证据视图（管理端只读拷贝）。
func (c *ChannelCompatCache) LatencyPenaltySnapshot() []LatencyPenaltySnapshotEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	now := time.Now()
	entries := make([]LatencyPenaltySnapshotEntry, 0, len(c.latencyPenalties))
	for key, state := range c.latencyPenalties {
		if state == nil {
			continue
		}
		parts := strings.SplitN(key, "|", 4)
		if len(parts) != 4 {
			continue
		}
		entries = append(entries, LatencyPenaltySnapshotEntry{
			ChannelUID:      parts[0],
			KeyHash:         parts[1],
			Model:           parts[2],
			TaskClass:       parts[3],
			SlowStreak:      state.SlowStreak,
			Degraded:        latencyPenaltyFresh(state, now) && state.SlowStreak >= latencySlowStreakThreshold,
			LastSlowAt:      state.LastSlowAt,
			LastFirstByteMs: state.LastFirstByteMs,
			LastFastAt:      state.LastFastAt,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ChannelUID != entries[j].ChannelUID {
			return entries[i].ChannelUID < entries[j].ChannelUID
		}
		if entries[i].Model != entries[j].Model {
			return entries[i].Model < entries[j].Model
		}
		return entries[i].TaskClass < entries[j].TaskClass
	})
	return entries
}

// ClearLatencyPenalties 只清除延迟慢证据分区（traits/limits/windows 保留），返回清除条目数。
func (c *ChannelCompatCache) ClearLatencyPenalties() int {
	c.mu.Lock()
	removed := len(c.latencyPenalties)
	if removed == 0 {
		c.mu.Unlock()
		return 0
	}
	c.latencyPenalties = make(map[string]*LatencyPenaltyState)
	c.dirty = true
	c.mu.Unlock()

	if err := c.Flush(); err != nil {
		log.Printf("[ChannelCompat-Flush] 清除延迟分区后落盘失败: %v", err)
	}
	return removed
}

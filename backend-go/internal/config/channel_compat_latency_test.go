package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newLatencyTestCache() *ChannelCompatCache {
	return NewChannelCompatCache() // 纯内存
}

func TestLatencySlowStreakThresholding(t *testing.T) {
	cache := newLatencyTestCache()
	now := time.Now()

	// 两次慢证据：未达阈值，不劣化
	cache.RecordSlowEvidence("ch_a", "kh_1", "model-x", "lightweight", 8000, now)
	if cache.RecordSlowEvidence("ch_a", "kh_1", "model-x", "lightweight", 9000, now) {
		t.Fatal("第 2 次不应跨过阈值")
	}
	if cache.IsLatencyDegraded("ch_a", "model-x", "lightweight") {
		t.Fatal("streak=2 不应判劣化")
	}
	// 第三次：跨过阈值
	if !cache.RecordSlowEvidence("ch_a", "kh_1", "model-x", "lightweight", 7000, now) {
		t.Fatal("第 3 次应跨过阈值")
	}
	if !cache.IsLatencyDegraded("ch_a", "model-x", "lightweight") {
		t.Fatal("streak=3 应判劣化")
	}
	// 模型大小写不敏感
	if !cache.IsLatencyDegraded("ch_a", "Model-X", "lightweight") {
		t.Fatal("模型名匹配应大小写不敏感")
	}
	// 其他 taskClass 不受影响（精确桶）
	if cache.IsLatencyDegraded("ch_a", "model-x", "worker") {
		t.Fatal("taskClass 精确桶不应外溢")
	}
	// 其他渠道/模型不受影响
	if cache.IsLatencyDegraded("ch_b", "model-x", "lightweight") || cache.IsLatencyDegraded("ch_a", "model-y", "lightweight") {
		t.Fatal("渠道/模型隔离失效")
	}
}

func TestLatencyCrossKeyAggregation(t *testing.T) {
	cache := newLatencyTestCache()
	now := time.Now()
	// 同渠道不同 key 各自累积：任一 key 达阈值即渠道×模型劣化（保守口径）
	for i := 0; i < 3; i++ {
		cache.RecordSlowEvidence("ch_a", "kh_1", "model-x", "", 8000, now)
	}
	if !cache.IsLatencyDegraded("ch_a", "model-x", "worker") {
		t.Fatal("跨 Key 聚合应判劣化")
	}
	// kh_1 之外 kh_2 只有一次：不影响（聚合读不区分 key，但 kh_1 已达标）
	if !cache.IsLatencyDegraded("ch_a", "model-x", "") {
		t.Fatal("空 taskClass 读取应命中 unknown 写入桶")
	}
}

func TestLatencyUnknownBucketFallback(t *testing.T) {
	cache := newLatencyTestCache()
	now := time.Now()
	// 写入侧 taskClass 为空 → 落 unknown 桶；读取侧任何 taskClass 都命中（全量记录）
	for i := 0; i < 3; i++ {
		cache.RecordSlowEvidence("ch_a", "kh_1", "model-x", "", 8000, now)
	}
	for _, tc := range []string{"lightweight", "worker", "supervisor", "vision", ""} {
		if !cache.IsLatencyDegraded("ch_a", "model-x", tc) {
			t.Fatalf("unknown 桶应命中任意 taskClass 读取, tc=%q", tc)
		}
	}
	// 反向：lightweight 精确桶记录不命中 worker 读取
	cache2 := newLatencyTestCache()
	for i := 0; i < 3; i++ {
		cache2.RecordSlowEvidence("ch_a", "kh_1", "model-x", "lightweight", 8000, now)
	}
	if cache2.IsLatencyDegraded("ch_a", "model-x", "worker") {
		t.Fatal("精确桶不应外溢到其他 taskClass")
	}
}

func TestLatencyFastEvidenceRecovery(t *testing.T) {
	cache := newLatencyTestCache()
	now := time.Now()
	for i := 0; i < 3; i++ {
		cache.RecordSlowEvidence("ch_a", "kh_1", "model-x", "lightweight", 8000, now)
	}
	if !cache.IsLatencyDegraded("ch_a", "model-x", "lightweight") {
		t.Fatal("前置：应已劣化")
	}
	// 快样本乐观翻转
	if !cache.RecordFastEvidence("ch_a", "kh_1", "model-x", "lightweight", now) {
		t.Fatal("从降权态翻转应返回 true")
	}
	if cache.IsLatencyDegraded("ch_a", "model-x", "lightweight") {
		t.Fatal("快样本后应解除劣化")
	}
	// 无记录时翻转返回 false
	if cache.RecordFastEvidence("ch_z", "kh_9", "model-none", "", now) {
		t.Fatal("无记录翻转应返回 false")
	}
}

func TestLatencyTTLExpiry(t *testing.T) {
	cache := newLatencyTestCache()
	now := time.Now()
	for i := 0; i < 3; i++ {
		cache.RecordSlowEvidence("ch_a", "kh_1", "model-x", "lightweight", 8000, now)
	}
	// 过期后不劣化
	if cache.IsLatencyDegradedAt("ch_a", "model-x", "lightweight", now.Add(latencyPenaltyTTL+time.Minute)) {
		t.Fatal("过期证据不应判劣化")
	}
	// 过期后再记慢证据：streak 重新起算（第 1 次不达阈值）
	cache.RecordSlowEvidence("ch_a", "kh_1", "model-x", "lightweight", 9000, now.Add(latencyPenaltyTTL+2*time.Minute))
	if cache.IsLatencyDegradedAt("ch_a", "model-x", "lightweight", now.Add(latencyPenaltyTTL+3*time.Minute)) {
		t.Fatal("过期重建后 streak=1 不应劣化")
	}
}

func TestLatencyInvalidInputIgnored(t *testing.T) {
	cache := newLatencyTestCache()
	now := time.Now()
	cache.RecordSlowEvidence("", "kh", "m", "", 1, now)
	cache.RecordSlowEvidence("ch", "kh", "", "", 1, now)
	if cache.IsLatencyDegraded("ch", "m", "") {
		t.Fatal("无效输入不应产生记录")
	}
}

func TestLatencyClearSection(t *testing.T) {
	cache := newLatencyTestCache()
	now := time.Now()
	for i := 0; i < 3; i++ {
		cache.RecordSlowEvidence("ch_a", "kh_1", "model-x", "lightweight", 8000, now)
	}
	if cache.ClearLatencyPenalties() != 1 {
		t.Fatal("应清除 1 条")
	}
	if cache.IsLatencyDegraded("ch_a", "model-x", "lightweight") {
		t.Fatal("清除后不应劣化")
	}
	if cache.ClearLatencyPenalties() != 0 {
		t.Fatal("再清应为 0")
	}
}

func TestLatencySnapshot(t *testing.T) {
	cache := newLatencyTestCache()
	now := time.Now()
	for i := 0; i < 3; i++ {
		cache.RecordSlowEvidence("ch_a", "kh_1", "model-x", "lightweight", 8000, now)
	}
	entries := cache.LatencyPenaltySnapshot()
	if len(entries) != 1 {
		t.Fatalf("应 1 条快照, got %d", len(entries))
	}
	e := entries[0]
	if e.ChannelUID != "ch_a" || e.Model != "model-x" || e.TaskClass != "lightweight" || e.SlowStreak != 3 || !e.Degraded || e.LastFirstByteMs != 8000 {
		t.Fatalf("快照字段不符: %+v", e)
	}
}

// 防抖落盘（热路径不同步写盘）：慢/快证据记录后不立即写文件，显式 Flush 后可重载。
func TestLatencyEvidenceFlushDebounced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compat.json")
	cache := NewChannelCompatCacheWithPersistence(path)
	now := time.Now()

	cache.RecordSlowEvidence("ch_a", "kh_1", "model-x", "lightweight", 8000, now)
	cache.RecordFastEvidence("ch_a", "kh_1", "model-x", "lightweight", now)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("latency 证据不应同步落盘（应走防抖窗口）")
	}

	if err := cache.Flush(); err != nil {
		t.Fatalf("显式落盘失败: %v", err)
	}
	reloaded := NewChannelCompatCacheWithPersistence(path)
	entries := reloaded.LatencyPenaltySnapshot()
	if len(entries) != 1 {
		t.Fatalf("重载后应 1 条记录, got %d", len(entries))
	}
	if entries[0].LastFastAt.IsZero() {
		t.Fatal("重载后快样本时间应保留")
	}
}

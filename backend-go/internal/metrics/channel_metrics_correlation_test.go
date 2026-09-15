package metrics

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/types"
)

// 用户请求关联 ID：RecordRequestCorrelationID 附加到 pending 记录后，
// finalize 应写入 SQLite（v9 correlation_id 列），LoadRecords 原样读回。
func TestRecordRequestCorrelationID_PersistedRoundtrip(t *testing.T) {
	store, err := NewSQLiteStore(&SQLiteStoreConfig{
		DBPath:        filepath.Join(t.TempDir(), "metrics.db"),
		RetentionDays: 7,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer func() { _ = store.Close() }()

	m := NewMetricsManagerWithPersistence(100, 0.5, store, "messages")
	defer m.Stop()

	// 同一用户请求的两次尝试（主 + 影子赢家），共享 correlation id
	reqID1 := m.RecordRequestConnectedWithCostContext("https://upstream.example", "sk-a", "messages", "ch-1", "m", "m", "sk-***", RequestCostContext{})
	m.RecordRequestCorrelationID("https://upstream.example", "sk-a", "messages", reqID1, "corr-1")
	m.RecordRequestFinalizeFailureWithClass("https://upstream.example", "sk-a", "messages", reqID1, FailureClassRetryable)

	reqID2 := m.RecordRequestConnectedWithCostContext("https://upstream.example", "sk-b", "messages", "ch-1", "m", "m", "sk-***", RequestCostContext{})
	m.RecordRequestCorrelationID("https://upstream.example", "sk-b", "messages", reqID2, "corr-1")
	m.RecordRequestFinalizeSuccess("https://upstream.example", "sk-b", "messages", reqID2, &types.Usage{InputTokens: 10, OutputTokens: 5})

	// 空关联 ID 不写入（fail-open）
	reqID3 := m.RecordRequestConnectedWithCostContext("https://upstream.example", "sk-c", "messages", "ch-1", "m", "m", "sk-***", RequestCostContext{})
	m.RecordRequestCorrelationID("https://upstream.example", "sk-c", "messages", reqID3, "")
	m.RecordRequestFinalizeSuccess("https://upstream.example", "sk-c", "messages", reqID3, &types.Usage{InputTokens: 1, OutputTokens: 1})

	store.flush()

	records, err := store.LoadRecords(time.Now().Add(-time.Minute), "messages")
	if err != nil {
		t.Fatalf("LoadRecords() err = %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("记录数 = %d, want 3", len(records))
	}
	var withCorr, withoutCorr int
	for _, r := range records {
		switch r.CorrelationID {
		case "corr-1":
			withCorr++
		case "":
			withoutCorr++
		default:
			t.Fatalf("意外 correlation id: %q", r.CorrelationID)
		}
	}
	if withCorr != 2 {
		t.Fatalf("主/影子尝试应共享 correlation id（2 条 corr-1），实际 %d 条", withCorr)
	}
	if withoutCorr != 1 {
		t.Fatalf("空关联 ID 不应写入（1 条空串），实际 %d 条", withoutCorr)
	}
}

// 时间窗口聚合：UserRequestCount = COUNT(DISTINCT correlation_id)，
// 与 RequestCount（上游尝试口径）对照可见竞速/failover 放大倍数。
func TestTimeWindowUserRequestCount(t *testing.T) {
	m := NewMetricsManagerWithConfig(100, 0.5)
	defer m.Stop()

	// 用户请求 1：主失败 + 影子成功（2 次尝试，1 个用户请求）
	id1 := m.RecordRequestConnectedWithCostContext("https://up.example", "sk-a", "messages", "ch-1", "m", "m", "sk-***", RequestCostContext{})
	m.RecordRequestCorrelationID("https://up.example", "sk-a", "messages", id1, "corr-1")
	m.RecordRequestFinalizeFailureWithClass("https://up.example", "sk-a", "messages", id1, FailureClassRetryable)
	id2 := m.RecordRequestConnectedWithCostContext("https://up.example", "sk-b", "messages", "ch-1", "m", "m", "sk-***", RequestCostContext{})
	m.RecordRequestCorrelationID("https://up.example", "sk-b", "messages", id2, "corr-1")
	m.RecordRequestFinalizeSuccess("https://up.example", "sk-b", "messages", id2, &types.Usage{InputTokens: 10, OutputTokens: 5})

	// 用户请求 2：一次成功
	id3 := m.RecordRequestConnectedWithCostContext("https://up.example", "sk-a", "messages", "ch-1", "m", "m", "sk-***", RequestCostContext{})
	m.RecordRequestCorrelationID("https://up.example", "sk-a", "messages", id3, "corr-2")
	m.RecordRequestFinalizeSuccess("https://up.example", "sk-a", "messages", id3, &types.Usage{InputTokens: 5, OutputTokens: 5})

	resp := m.ToResponseMultiURL(0, []string{"https://up.example"}, []string{"sk-a", "sk-b"}, "messages", 0)
	w := resp.TimeWindows["15m"]
	if w.RequestCount != 3 {
		t.Fatalf("RequestCount（尝试口径）= %d, want 3", w.RequestCount)
	}
	if w.UserRequestCount != 2 {
		t.Fatalf("UserRequestCount（用户请求口径）= %d, want 2", w.UserRequestCount)
	}
}

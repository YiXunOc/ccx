package metrics

import (
	"path/filepath"
	"testing"
	"time"
)

func TestManualResumeExcludesOldFailuresButKeepsHistory(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()
	m.UpdateCircuitBreakerConfig(CircuitBreakerParams{WindowSize: 50, FailureThreshold: 0.85, ConsecutiveFailuresThreshold: 10})
	urls := []string{"https://primary.example.com", "https://backup.example.com"}
	keys := []string{"key-a", "key-b"}
	for i := 0; i < 10; i++ {
		m.RecordFailure(urls[i%2], keys[(i/2)%2], "openai", "")
	}
	if got := m.ToResponseMultiURL(0, urls, keys, "openai", 0).CircuitState; got != "open" {
		t.Fatalf("before resume: circuit = %s, want open", got)
	}
	for _, url := range urls {
		for _, key := range keys {
			m.ResetKeyFailureState(url, key, "openai")
		}
	}
	resp := m.ToResponseMultiURL(0, urls, keys, "openai", 0)
	if resp.CircuitState != "closed" || resp.RequestCount != 10 || resp.FailureCount != 10 {
		t.Fatalf("after resume: circuit=%s requests=%d failures=%d, want closed/10/10", resp.CircuitState, resp.RequestCount, resp.FailureCount)
	}
	if !m.IsChannelHealthyMultiURL(urls, keys, "openai", "") {
		t.Fatal("old failures must not immediately exclude the channel from scheduling")
	}
	// New failures in the same second must still reopen the channel.
	for i := 0; i < 10; i++ {
		m.RecordFailure(urls[i%2], keys[(i/2)%2], "openai", "")
	}
	if got := m.ToResponseMultiURL(0, urls, keys, "openai", 0).CircuitState; got != "open" {
		t.Fatalf("new failures must reopen channel, got %s", got)
	}
}

func TestManualResumeBoundarySurvivesRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "metrics.db")
	store, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: dbPath, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	m := NewMetricsManagerWithPersistence(50, 0.85, store, "messages")
	urls := []string{"https://primary.example.com", "https://backup.example.com"}
	keys := []string{"key-a", "key-b"}
	m.UpdateCircuitBreakerConfig(CircuitBreakerParams{WindowSize: 50, FailureThreshold: 0.85, ConsecutiveFailuresThreshold: 10})
	for i := 0; i < 10; i++ {
		m.RecordFailure(urls[i%2], keys[(i/2)%2], "openai", "")
	}
	if got := m.ToResponseMultiURL(0, urls, keys, "openai", 0).CircuitState; got != "open" {
		t.Fatalf("before resume: %s", got)
	}
	for _, url := range urls {
		for _, key := range keys {
			m.ResetKeyFailureState(url, key, "openai")
		}
	}
	if got := m.ToResponseMultiURL(0, urls, keys, "openai", 0).CircuitState; got != "closed" {
		t.Fatalf("after resume: %s", got)
	}
	m.Stop()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(&SQLiteStoreConfig{DBPath: dbPath, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reloaded := NewMetricsManagerWithPersistence(50, 0.85, store, "messages")
	defer reloaded.Stop()
	resp := reloaded.ToResponseMultiURL(0, urls, keys, "openai", 0)
	if resp.CircuitState != "closed" || resp.RequestCount != 10 || resp.FailureCount != 10 {
		t.Fatalf("after reload: circuit=%s requests=%d failures=%d", resp.CircuitState, resp.RequestCount, resp.FailureCount)
	}
	for i := 0; i < 10; i++ {
		reloaded.RecordFailure(urls[i%2], keys[(i/2)%2], "openai", "")
	}
	if got := reloaded.ToResponseMultiURL(0, urls, keys, "openai", 0).CircuitState; got != "open" {
		t.Fatalf("same-second failures after reload must reopen channel, got %s", got)
	}
}

func TestManualResumeClearsAllLegacyMetricVariants(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()
	url, key := "https://example.com", "key-a"
	m.mu.Lock()
	for _, identity := range m.metricsLookupKeys(url, key, "openai") {
		km := m.getOrCreateKeyLocked(url, identity, "masked")
		km.CircuitState = CircuitStateOpen
	}
	m.mu.Unlock()
	m.ResetKeyFailureState(url, key, "openai")
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, km := range m.getMetricsVariantsLocked(url, key, "openai") {
		if km.CircuitState != CircuitStateClosed || km.ResetAt == nil {
			t.Fatalf("variant %s not reset: state=%s boundary=%v", km.MetricsKey, km.CircuitState, km.ResetAt)
		}
	}
}

func TestManualResumeIgnoresPreResetPendingOutcomeForBreaker(t *testing.T) {
	for _, tc := range []struct {
		name    string
		success bool
		want    CircuitState
	}{
		{name: "old success cannot close new open", success: true, want: CircuitStateOpen},
		{name: "old failure cannot reopen new half-open", success: false, want: CircuitStateHalfOpen},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMetricsManager()
			defer m.Stop()
			url, key, service := "https://example.com", "key-a", "openai"
			pending := m.RecordRequestConnected(url, key, service, "model-a")
			m.ResetKeyFailureState(url, key, service)
			if tc.want == CircuitStateOpen {
				m.RecordFailureWithClass(url, key, service, "", FailureClassOverloaded)
			} else {
				m.MoveKeyToHalfOpen(url, key, service)
			}
			m.RecordRequestFinalizeOutcome(url, key, service, pending, tc.success, FailureClassRetryable, nil)
			if got := m.GetKeyCircuitState(url, key, service); got != tc.want {
				t.Fatalf("old pending result changed circuit to %s, want %s", got, tc.want)
			}
			wantCount := int64(1)
			if tc.want == CircuitStateOpen {
				wantCount = 2
			}
			if got := m.ToResponseMultiURL(0, []string{url}, []string{key}, service, 0).RequestCount; got != wantCount {
				t.Fatalf("history count=%d, want %d", got, wantCount)
			}
		})
	}
}

func TestLoadRecordsOrdersNanosecondTimestamps(t *testing.T) {
	store, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: filepath.Join(t.TempDir(), "metrics.db"), RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base := time.Now().Truncate(time.Second)
	late := PersistentRecord{MetricsKey: "test", BaseURL: "https://example.com", KeyMask: "masked", APIType: "messages", Timestamp: base.Add(900 * time.Millisecond), Success: false}
	early := late
	early.Timestamp = base.Add(100 * time.Millisecond)
	early.Success = true
	if err := store.batchInsertRecords([]PersistentRecord{late, early}); err != nil {
		t.Fatal(err)
	}
	records, err := store.LoadRecords(base.Add(-time.Second), "messages")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || !records[0].Timestamp.Equal(early.Timestamp) || !records[1].Timestamp.Equal(late.Timestamp) {
		t.Fatalf("loaded order=%v, want early then late", records)
	}
}

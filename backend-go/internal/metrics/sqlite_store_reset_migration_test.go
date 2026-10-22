package metrics

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestSchemaV9UpgradePreservesRecordsAndCircuitState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
  CREATE TABLE request_records (id INTEGER PRIMARY KEY, metrics_key TEXT NOT NULL, base_url TEXT NOT NULL, key_mask TEXT NOT NULL, timestamp INTEGER NOT NULL, success INTEGER NOT NULL, failure_class TEXT NOT NULL DEFAULT '', api_type TEXT NOT NULL DEFAULT 'messages');
  CREATE TABLE circuit_states (metrics_key TEXT NOT NULL, api_type TEXT NOT NULL, base_url TEXT NOT NULL, key_mask TEXT NOT NULL, circuit_state TEXT NOT NULL DEFAULT 'closed', circuit_opened_at INTEGER, half_open_at INTEGER, next_retry_at INTEGER, backoff_level INTEGER NOT NULL DEFAULT 0, half_open_successes INTEGER NOT NULL DEFAULT 0, consecutive_failures INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL, PRIMARY KEY(metrics_key, api_type));
  INSERT INTO request_records (metrics_key, base_url, key_mask, timestamp, success, failure_class, api_type) VALUES ('old', 'https://example.com', 'masked', ?, 0, 'retryable', 'messages');
  INSERT INTO circuit_states (metrics_key, api_type, base_url, key_mask, circuit_state, updated_at) VALUES ('old', 'messages', 'https://example.com', 'masked', 'open', ?);
  PRAGMA user_version = 9;
 `, time.Now().Unix(), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: path, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	states, err := store.LoadCircuitStates("messages")
	if err != nil {
		t.Fatal(err)
	}
	if states["old"] == nil || states["old"].CircuitState != "open" || states["old"].ResetAt != nil {
		t.Fatalf("legacy circuit state lost: %+v", states["old"])
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM request_records WHERE metrics_key = 'old' AND timestamp_ns IS NULL").Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy record lost: count=%d error=%v", count, err)
	}
}

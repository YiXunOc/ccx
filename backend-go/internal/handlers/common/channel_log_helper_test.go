package common

import (
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/metrics"
)

func TestRecordChannelLog_TruncatesAndMasks(t *testing.T) {
	store := metrics.NewChannelLogStore()
	longError := strings.Repeat("x", 260)

	RecordChannelLog(
		store,
		"test-metrics-key",
		3,
		"model-a",
		"model-orig",
		502,
		123,
		false,
		"sk-test-very-secret",
		"https://example.com",
		longError,
		"Responses",
		true,
	)

	logs := store.Get("test-metrics-key")
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}

	got := logs[0]
	if got.Model != "model-a" {
		t.Fatalf("model = %q, want model-a", got.Model)
	}
	if got.OriginalModel != "model-orig" {
		t.Fatalf("originalModel = %q, want model-orig", got.OriginalModel)
	}
	if got.StatusCode != 502 {
		t.Fatalf("statusCode = %d, want 502", got.StatusCode)
	}
	if got.DurationMs != 123 {
		t.Fatalf("durationMs = %d, want 123", got.DurationMs)
	}
	if got.Success {
		t.Fatalf("success = true, want false")
	}
	if got.BaseURL != "https://example.com" {
		t.Fatalf("baseURL = %q, want https://example.com", got.BaseURL)
	}
	if got.InterfaceType != "Responses" {
		t.Fatalf("interfaceType = %q, want Responses", got.InterfaceType)
	}
	if got.RequestSource != metrics.RequestSourceProxy {
		t.Fatalf("requestSource = %q, want %q", got.RequestSource, metrics.RequestSourceProxy)
	}
	if !got.IsRetry {
		t.Fatalf("isRetry = false, want true")
	}
	if len(got.ErrorInfo) != 200 {
		t.Fatalf("errorInfo len = %d, want 200", len(got.ErrorInfo))
	}
	if got.KeyMask == "sk-test-very-secret" || got.KeyMask == "" {
		t.Fatalf("keyMask = %q, want masked non-empty value", got.KeyMask)
	}
}

func TestRecordChannelLogWithSource_UsesExplicitSource(t *testing.T) {
	store := metrics.NewChannelLogStore()

	RecordChannelLogWithSource(
		store,
		"test-metrics-key-2",
		1,
		"model-b",
		"",
		200,
		45,
		true,
		"sk-test-another-secret",
		"https://example.com",
		"",
		"Messages",
		false,
		metrics.RequestSourceCapabilityTest,
	)

	logs := store.Get("test-metrics-key-2")
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if logs[0].RequestSource != metrics.RequestSourceCapabilityTest {
		t.Fatalf("requestSource = %q, want %q", logs[0].RequestSource, metrics.RequestSourceCapabilityTest)
	}
}

func TestCreatePendingLog_WithSelectionTrace(t *testing.T) {
	store := metrics.NewChannelLogStore()

	CreatePendingLog(
		store,
		"test-metrics-key-selection",
		1,
		"trace-channel",
		"model-a",
		"",
		"",
		"",
		"sk-test-secret",
		"https://example.com",
		"Messages",
		"",
		metrics.RequestSourceProxy,
		nil,
		"",
		WithChannelSelectionTrace("priority_order", "stages=active_model_filter:1 selected=1:trace-channel/priority_order"),
	)

	logs := store.Get("test-metrics-key-selection")
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if logs[0].SelectionReason != "priority_order" {
		t.Fatalf("selectionReason = %q, want priority_order", logs[0].SelectionReason)
	}
	if !strings.Contains(logs[0].SelectionTraceSummary, "selected=1:trace-channel/priority_order") {
		t.Fatalf("selectionTraceSummary = %q, want selected channel summary", logs[0].SelectionTraceSummary)
	}
}

func TestCompleteLog_MapsClientCanceledToCancelledStatus(t *testing.T) {
	store := metrics.NewChannelLogStore()
	requestID := CreatePendingLog(store, "test-metrics-key-3", 0, "test-channel", "model-a", "", "", "", "sk-test-secret", "https://example.com", "Responses", "edits", metrics.RequestSourceProxy, nil, "")

	CompleteLog(store, "test-metrics-key-3", requestID, 200, false, "client canceled", false)

	logs := store.Get("test-metrics-key-3")
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if logs[0].Operation != "edits" {
		t.Fatalf("operation = %q, want edits", logs[0].Operation)
	}
	if logs[0].Status != metrics.StatusCancelled {
		t.Fatalf("status = %q, want %q", logs[0].Status, metrics.StatusCancelled)
	}
	if logs[0].Success {
		t.Fatalf("success = true, want false")
	}
}

func TestCompleteLog_LeavesRealFailuresAsFailed(t *testing.T) {
	store := metrics.NewChannelLogStore()
	requestID := CreatePendingLog(store, "test-metrics-key-4", 0, "test-channel", "model-a", "", "", "", "sk-test-secret", "https://example.com", "Responses", "", metrics.RequestSourceProxy, nil, "")

	CompleteLog(store, "test-metrics-key-4", requestID, 502, false, "upstream timeout", false)

	logs := store.Get("test-metrics-key-4")
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if logs[0].Status != metrics.StatusFailed {
		t.Fatalf("status = %q, want %q", logs[0].Status, metrics.StatusFailed)
	}
}

func TestCompleteLog_MapsRacingSupersededToRacingLostStatus(t *testing.T) {
	store := metrics.NewChannelLogStore()
	requestID := CreatePendingLog(store, "test-metrics-key-6", 0, "test-channel", "model-a", "", "", "", "sk-test-secret", "https://example.com", "Messages", "", metrics.RequestSourceProxy, nil, "")

	CompleteLog(store, "test-metrics-key-6", requestID, 0, false, "racing superseded", false)

	logs := store.Get("test-metrics-key-6")
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if logs[0].Status != metrics.StatusRacingLost {
		t.Fatalf("status = %q, want %q", logs[0].Status, metrics.StatusRacingLost)
	}
	if !strings.HasPrefix(logs[0].ErrorInfo, "竞速败出：") {
		t.Fatalf("errorInfo = %q, want racing lost display text", logs[0].ErrorInfo)
	}
}

func TestRecordChannelLogWithSource_MapsRacingSupersededToRacingLostStatus(t *testing.T) {
	store := metrics.NewChannelLogStore()

	RecordChannelLogWithSource(
		store,
		"test-metrics-key-7",
		1,
		"model-b",
		"",
		0,
		45,
		false,
		"sk-test-another-secret",
		"https://example.com",
		"racing superseded",
		"Messages",
		false,
		metrics.RequestSourceProxy,
	)

	logs := store.Get("test-metrics-key-7")
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if logs[0].Status != metrics.StatusRacingLost {
		t.Fatalf("status = %q, want %q", logs[0].Status, metrics.StatusRacingLost)
	}
}

func TestCompleteLog_NormalizesEmptyStreamErrorInfo(t *testing.T) {
	store := metrics.NewChannelLogStore()
	requestID := CreatePendingLog(store, "test-metrics-key-5", 0, "test-channel", "model-a", "", "", "", "sk-test-secret", "https://example.com", "Messages", "", metrics.RequestSourceProxy, nil, "")

	CompleteLog(store, "test-metrics-key-5", requestID, 200, false, "upstream returned empty stream response: 检测到空流，但未匹配到明确类别", false)

	logs := store.Get("test-metrics-key-5")
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if !strings.HasPrefix(logs[0].ErrorInfo, "空流响应：") {
		t.Fatalf("errorInfo = %q, want empty stream display text", logs[0].ErrorInfo)
	}
	if strings.Contains(logs[0].ErrorInfo, "断流") {
		t.Fatalf("errorInfo = %q, should not classify empty stream as stalled stream", logs[0].ErrorInfo)
	}
	if !strings.Contains(logs[0].ErrorInfo, "检测到空流") {
		t.Fatalf("errorInfo = %q, want diagnostic preserved", logs[0].ErrorInfo)
	}
}

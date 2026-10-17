package observability

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCaptureModeParsingDefaultsClosed(t *testing.T) {
	cases := []struct {
		raw  string
		want CaptureMode
	}{
		{"", CaptureModeOff},
		{"off", CaptureModeOff},
		{"summary", CaptureModeSummary},
		{"failed", CaptureModeFailed},
		{"all", CaptureModeAll},
		{"unknown", CaptureModeOff},
	}
	for _, tc := range cases {
		if got := ParseCaptureMode(tc.raw); got != tc.want {
			t.Fatalf("ParseCaptureMode(%q)=%q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestCaptureContextHasStableAttemptIdentity(t *testing.T) {
	ctx := NewContext("corr_1", "rt_1", "attempt_1", "ch_1", "responses", "responses", "gpt-6.1-sol", "sk-***")
	if ctx.CorrelationID != "corr_1" || ctx.TraceUID != "rt_1" || ctx.AttemptID != "attempt_1" {
		t.Fatalf("unexpected context: %+v", ctx)
	}
	if ctx.CaptureID == "" || ctx.TransportTryID != "" {
		t.Fatalf("generated IDs missing: %+v", ctx)
	}
	if ctx.AssociationStatus() != "incomplete" {
		t.Fatalf("association status=%q", ctx.AssociationStatus())
	}
}

func TestSummaryRecordsRequestAndResponseBoundariesWithoutPayload(t *testing.T) {
	ctx := NewContext("corr_1", "rt_1", "attempt_1", "ch_1", "responses", "responses", "gpt-6.1-sol", "sk-***")
	s := NewSummary(ctx)
	s.ClientBodyBytes = 100
	s.ConvertedBodyBytes = 120
	s.OutboundBodyBytes = 120
	s.ContentLength = 120
	s.GetBodyReplayBytes = 120
	s.UpstreamStatus = 200
	s.HeaderCommitted = false
	s.EventCounts["response.metadata"] = 1
	s.EventCounts["response.failed"] = 1
	s.Terminal = "response.failed"
	s.Outcome = "preflight_empty"
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, secret := range []string{"prompt", "PRIVATE", "Authorization"} {
		if strings.Contains(text, secret) {
			t.Fatalf("summary contains payload-like data %q: %s", secret, text)
		}
	}
	for _, field := range []string{"corr_1", "attempt_1", "clientBodyBytes", "convertedBodyBytes", "getBodyReplayBytes", "response.failed"} {
		if !strings.Contains(text, field) {
			t.Fatalf("summary missing %q: %s", field, text)
		}
	}
}

func TestSummaryAssociationMissingIsExplicit(t *testing.T) {
	ctx := NewContext("", "", "", "", "responses", "responses", "", "")
	s := NewSummary(ctx)
	if s.AssociationStatus != "incomplete" {
		t.Fatalf("association status=%q", s.AssociationStatus)
	}
}

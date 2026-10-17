package config

import "testing"

func TestNewEnvConfigPayloadCaptureModeDefaultsOff(t *testing.T) {
	t.Setenv("CCX_PAYLOAD_CAPTURE_MODE", "")
	if got := NewEnvConfig().PayloadCaptureMode; got != "off" {
		t.Fatalf("mode=%q, want off", got)
	}
}

func TestNewEnvConfigPayloadCaptureModeAllowsSummaryAndFailed(t *testing.T) {
	for _, want := range []string{"summary", "failed", "all"} {
		t.Setenv("CCX_PAYLOAD_CAPTURE_MODE", want)
		if got := NewEnvConfig().PayloadCaptureMode; got != want {
			t.Fatalf("mode=%q, want %q", got, want)
		}
	}
}

func TestNewEnvConfigPayloadCaptureModeRejectsUnknown(t *testing.T) {
	t.Setenv("CCX_PAYLOAD_CAPTURE_MODE", "raw-wire")
	if got := NewEnvConfig().PayloadCaptureMode; got != "off" {
		t.Fatalf("mode=%q, want off", got)
	}
}

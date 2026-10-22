package common

import (
	"github.com/BenedictKing/ccx/internal/config"
	"testing"
)

func TestManualAttemptBodyPreparesActualModelBeforeProviderConversion(t *testing.T) {
	body, err := prepareManualAttemptBody([]byte(`{"model":"A","max_tokens":100}`), "B")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != `{"model":"B","max_tokens":100}` {
		t.Fatalf("pre-provider model = %s", got)
	}
	gemini, err := prepareManualAttemptBody([]byte(`{"contents":[{"parts":[{"text":"hi"}]}]}`), "B")
	if err != nil || string(gemini) != `{"contents":[{"parts":[{"text":"hi"}]}]}` {
		t.Fatalf("Gemini body changed: %s, %v", gemini, err)
	}
}

func TestManualAttemptModelSingleHop(t *testing.T) {
	up := &config.UpstreamConfig{ModelMapping: map[string]string{"A": "B", "B": "C"}}
	if got := manualAttemptModel(up, "A"); got != "B" {
		t.Fatalf("got %q", got)
	}
	if got := manualAttemptModel(up, "a"); got != "a" {
		t.Fatalf("case mismatch got %q", got)
	}
}

package scheduler

import (
	"context"
	"github.com/BenedictKing/ccx/internal/config"
	"testing"
)

func TestManualMappingPreservesOriginalAdmissionAndPinsActualModel(t *testing.T) {
	s := &ChannelScheduler{}
	up := &config.UpstreamConfig{SupportedModels: []string{"A"}, ModelMapping: map[string]string{"A": "B", "B": "C"}}
	supported, actual, _ := s.resolveModelSupportWithActual(context.Background(), ChannelKindMessages, up, "A")
	if !supported || actual != "B" {
		t.Fatalf("A -> supported=%v actual=%q", supported, actual)
	}
	supported, _, _ = s.resolveModelSupportWithActual(context.Background(), ChannelKindMessages, up, "a")
	if supported {
		t.Fatal("case-insensitive admission")
	}
}

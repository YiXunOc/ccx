package scheduler

import (
	"context"
	"github.com/BenedictKing/ccx/internal/config"
	"testing"
)

func TestManualMappingSixIngressKinds(t *testing.T) {
	kinds := []ChannelKind{ChannelKindMessages, ChannelKindChat, ChannelKindResponses, ChannelKindGemini, ChannelKindImages, ChannelKindVectors}
	s := &ChannelScheduler{}
	up := &config.UpstreamConfig{SupportedModels: []string{"A"}, ModelMapping: map[string]string{"A": "B"}}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			supported, actual, _ := s.resolveModelSupportWithActual(context.Background(), kind, up, "A")
			if !supported || actual != "B" {
				t.Fatalf("support=%v actual=%q", supported, actual)
			}
		})
	}
}

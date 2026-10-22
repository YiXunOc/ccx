package autopilot

import (
	"github.com/BenedictKing/ccx/internal/config"
	"testing"
)

func TestSmartRouterManualMappingManagedAndNoChaining(t *testing.T) {
	router := &SmartRouter{}
	profile := &RequestProfile{Model: "A", ChannelKind: "messages"}
	up := &config.UpstreamConfig{ChannelUID: "ch_test", AutoManaged: true, ModelMapping: map[string]string{"A": "B", "B": "C"}, SupportedModels: []string{"A"}}
	got := router.resolveChannelModel(profile, up, nil)
	if !got.Supported || got.ActualModel != "B" || got.MappingSource != "manual_mapping" {
		t.Fatalf("resolution=%+v", got)
	}
	rows := router.resolveChannelModels(profile, up, nil)
	if len(rows) != 1 || rows[0].ActualModel != "B" {
		t.Fatalf("rows=%+v", rows)
	}
}

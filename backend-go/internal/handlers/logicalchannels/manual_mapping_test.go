package logicalchannels

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestLogicalProtocolRoutesPreservePhysicalModelMappings(t *testing.T) {
	cfg := config.Config{
		Upstream:     []config.UpstreamConfig{{ChannelUID: "messages-1", ModelMapping: map[string]string{"A": "B"}}},
		ChatUpstream: []config.UpstreamConfig{{ChannelUID: "chat-1", ModelMapping: map[string]string{"A": "C"}}},
	}
	logical := config.LogicalChannel{Protocols: []config.LogicalChannelProtocol{
		{Kind: "messages", ChannelUID: "messages-1"},
		{Kind: "chat", ChannelUID: "chat-1"},
	}}
	routes := buildLogicalProtocolRoutes(cfg, logical)
	if len(routes) != 2 {
		t.Fatalf("routes: %v", routes)
	}
	for _, route := range routes {
		mapping, ok := route["modelMapping"].(map[string]string)
		if !ok {
			t.Fatalf("missing mapping on route: %v", route)
		}
		want := "B"
		if route["kind"] == "chat" {
			want = "C"
		}
		if mapping["A"] != want {
			t.Fatalf("physical route %v has mapping %v, want %s", route["kind"], mapping, want)
		}
	}
}

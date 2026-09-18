package scheduler

import (
	"context"
	"github.com/BenedictKing/ccx/internal/config"
)

// The binding and route identity always come from the same request snapshot.
func protocolPreferenceAllows(cfg *config.Config, route ChannelRouteRef, requestModel, actualModel string) bool {
	var upstreams []config.UpstreamConfig
	switch ChannelKind(route.Kind) {
	case ChannelKindMessages:
		upstreams = cfg.Upstream
	case ChannelKindChat:
		upstreams = cfg.ChatUpstream
	case ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	case ChannelKindImages:
		upstreams = cfg.ImagesUpstream
	case ChannelKindVectors:
		upstreams = cfg.VectorsUpstream
	}
	for i, u := range upstreams {
		if (route.ChannelUID != "" && route.ChannelUID != u.ChannelUID) || (route.ChannelUID == "" && route.Index != i) {
			continue
		}
		if actualModel == "" {
			actualModel = config.RedirectModel(requestModel, &u)
		}
		for _, model := range []string{requestModel, actualModel} {
			bound := cfg.ProtocolForLogicalModel(u.LogicalChannelUID, model)
			if bound != "" && bound != route.Kind {
				return false
			}
		}
		return true
	}
	return false
}

func filterProtocolPreferences(cfg *config.Config, channels []ChannelInfo, kind ChannelKind, model string) []ChannelInfo {
	out := make([]ChannelInfo, 0, len(channels))
	for _, ch := range channels {
		if protocolPreferenceAllows(cfg, normalizedChannelRoute(ch, kind), model, ch.ActualModel) {
			out = append(out, ch)
		}
	}
	return out
}

// Add only explicitly bound siblings, never unrelated account routes. Conversion
// support remains the existing chat/responses execution boundary; each supported
// client protocol can use it when the corresponding handler conversion exists.
func (s *ChannelScheduler) addBoundProtocolCandidates(ctx context.Context, cfg *config.Config, kind ChannelKind, model string, channels []ChannelInfo, trace *SelectionTrace) []ChannelInfo {
	var sources []config.UpstreamConfig
	switch kind {
	case ChannelKindMessages:
		sources = cfg.Upstream
	case ChannelKindChat:
		sources = cfg.ChatUpstream
	case ChannelKindResponses:
		sources = cfg.ResponsesUpstream
	default:
		return channels
	}
	logicalSources := map[string]bool{}
	for _, u := range sources {
		if u.LogicalChannelUID != "" {
			logicalSources[u.LogicalChannelUID] = true
		}
	}
	seen := map[ChannelRouteKey]bool{}
	for _, ch := range channels {
		seen[ch.Route.Key()] = true
	}
	for _, target := range []ChannelKind{ChannelKindChat, ChannelKindResponses} {
		if target == kind {
			continue
		}
		for _, ch := range s.getActiveChannelsWithTrace(ctx, target, model, trace, cfg) {
			var us []config.UpstreamConfig
			if target == ChannelKindChat {
				us = cfg.ChatUpstream
			} else {
				us = cfg.ResponsesUpstream
			}
			if ch.Index < 0 || ch.Index >= len(us) {
				continue
			}
			u := &us[ch.Index]
			actual := config.RedirectModel(model, u)
			bound := cfg.ProtocolForLogicalModel(u.LogicalChannelUID, actual)
			if bound == "" {
				bound = cfg.ProtocolForLogicalModel(u.LogicalChannelUID, model)
			}
			if !logicalSources[u.LogicalChannelUID] || bound != string(target) || seen[ch.Route.Key()] {
				continue
			}
			ch.ActualModel = actual
			ch.ProtocolFidelity = "converted"
			ch.ConversionPenalty = protocolFederationConversionPenalty
			channels = append(channels, ch)
			seen[ch.Route.Key()] = true
		}
	}
	return channels
}

package config

import (
	"fmt"
	"sort"
	"strings"
)

// ProtocolModelPreferences is owned exclusively by a logical channel.
type ProtocolModelPreferences map[string][]string

func (p ProtocolModelPreferences) Clone() ProtocolModelPreferences {
	if p == nil {
		return nil
	}
	out := make(ProtocolModelPreferences, len(p))
	for k, v := range p {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func (p ProtocolModelPreferences) Validate() error {
	for kind, models := range p {
		switch kind {
		case "messages", "chat", "responses", "gemini", "images", "vectors":
		default:
			return fmt.Errorf("protocolModelPreferences: unknown protocol %q", kind)
		}
		seen := map[string]bool{}
		for _, model := range models {
			if model == "" || strings.TrimSpace(model) != model {
				return fmt.Errorf("protocolModelPreferences: model must be nonblank without surrounding whitespace")
			}
			if seen[model] {
				return fmt.Errorf("protocolModelPreferences: model %q is duplicated for protocol %s", model, kind)
			}
			seen[model] = true
		}
	}
	return nil
}

func (p ProtocolModelPreferences) HasModelForProtocol(protocol, model string) bool {
	for _, candidate := range p[protocol] {
		if candidate == model {
			return true
		}
	}
	return false
}

// ProtocolsForModel returns every explicitly bound protocol in deterministic order.
func (p ProtocolModelPreferences) ProtocolsForModel(model string) []string {
	var protocols []string
	for protocol := range p {
		if p.HasModelForProtocol(protocol, model) {
			protocols = append(protocols, protocol)
		}
	}
	sort.Strings(protocols)
	return protocols
}

func mergeProtocolModelPreferences(a, b ProtocolModelPreferences) (ProtocolModelPreferences, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	out := a.Clone()
	if out == nil {
		out = ProtocolModelPreferences{}
	}
	for kind, models := range b {
		for _, model := range models {
			if !out.HasModelForProtocol(kind, model) {
				out[kind] = append(out[kind], model)
			}
		}
	}
	return out, nil
}

// ProtocolsForLogicalModel resolves bindings using the caller's configuration snapshot.
// No binding means automatic routing, not an empty set of eligible protocols.
func (c *Config) ProtocolsForLogicalModel(uid, model string) []string {
	if c == nil || uid == "" || model == "" {
		return nil
	}
	for _, l := range c.LogicalChannels {
		if l.LogicalChannelUID == uid {
			return l.ProtocolModelPreferences.ProtocolsForModel(model)
		}
	}
	return nil
}

func validateProtocolModelPreferences(c *Config) error {
	for _, l := range c.LogicalChannels {
		if err := l.ProtocolModelPreferences.Validate(); err != nil {
			return fmt.Errorf("logical channel %s: %w", l.LogicalChannelUID, err)
		}
	}
	// Validate the existing account convergence before it can discard an entity.
	byUID := map[string]ProtocolModelPreferences{}
	for _, l := range c.LogicalChannels {
		byUID[l.LogicalChannelUID] = l.ProtocolModelPreferences
	}
	accountBindings := map[string]ProtocolModelPreferences{}
	check := func(u UpstreamConfig) error {
		u.AccountUID = strings.TrimSpace(u.AccountUID)
		u.LogicalChannelUID = strings.TrimSpace(u.LogicalChannelUID)
		if u.AccountUID == "" {
			return nil
		}
		merged, err := mergeProtocolModelPreferences(accountBindings[u.AccountUID], byUID[u.LogicalChannelUID])
		if err != nil {
			return fmt.Errorf("protocolModelPreferences: account convergence conflict: %w", err)
		}
		accountBindings[u.AccountUID] = merged
		return nil
	}
	for _, e := range collectAllPhysicalChannelsWithSlice(c) {
		if err := check(e.channel); err != nil {
			return err
		}
	}
	if len(collectAllPhysicalChannelsWithSlice(c)) == 0 {
		for _, ch := range c.ChannelsV3 {
			for _, p := range ch.Protocols {
				if err := check(p.Upstream); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

package config

import (
	"fmt"
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
	seen := map[string]string{}
	for kind, models := range p {
		switch kind {
		case "messages", "chat", "responses", "gemini", "images", "vectors":
		default:
			return fmt.Errorf("protocolModelPreferences: unknown protocol %q", kind)
		}
		for _, model := range models {
			if model == "" || strings.TrimSpace(model) != model {
				return fmt.Errorf("protocolModelPreferences: model must be nonblank without surrounding whitespace")
			}
			if previous, ok := seen[model]; ok {
				return fmt.Errorf("protocolModelPreferences: model %q is bound more than once (%s, %s)", model, previous, kind)
			}
			seen[model] = kind
		}
	}
	return nil
}

func (p ProtocolModelPreferences) ProtocolForModel(model string) string {
	for kind, models := range p {
		for _, candidate := range models {
			if candidate == model {
				return kind
			}
		}
	}
	return ""
}

func mergeProtocolModelPreferences(a, b ProtocolModelPreferences) (ProtocolModelPreferences, error) {
	out := a.Clone()
	if out == nil {
		out = ProtocolModelPreferences{}
	}
	for kind, models := range b {
		for _, model := range models {
			if existing := out.ProtocolForModel(model); existing != "" {
				if existing != kind {
					return nil, fmt.Errorf("model %q has conflicting protocols", model)
				}
				continue
			}
			out[kind] = append(out[kind], model)
		}
	}
	return out, nil
}

// ProtocolForLogicalModel resolves a binding using the caller's configuration snapshot.
func (c *Config) ProtocolForLogicalModel(uid, model string) string {
	if uid == "" || model == "" {
		return ""
	}
	for _, l := range c.LogicalChannels {
		if l.LogicalChannelUID == uid {
			return l.ProtocolModelPreferences.ProtocolForModel(model)
		}
	}
	return ""
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

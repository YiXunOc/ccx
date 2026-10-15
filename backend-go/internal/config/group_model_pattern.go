package config

import (
	"fmt"
	"strings"
)

// IsAPIKeyGroupModelDisabled resolves the current quota group for a channel key.
// Restoring a rule uses expression identity, not this matcher, so overlaps stay independent.
func (u *UpstreamConfig) IsAPIKeyGroupModelDisabled(apiKey, model string) bool {
	if len(u.DisabledGroupModels) == 0 {
		return false
	}
	group, found := quotaGroupForAPIKey(u, apiKey)
	return found && u.IsGroupModelDisabled(apiKey, group, model)
}

// Only edge stars are supported; this is deliberately not a general glob parser.
func validateGroupModelPattern(pattern string) error {
	pattern = strings.TrimSpace(pattern)
	body := strings.TrimSuffix(strings.TrimPrefix(pattern, "*"), "*")
	if pattern == "" || strings.ContainsAny(pattern, "?[]!^") ||
		strings.Contains(body, "*") || (body == "" && pattern != "*") {
		return fmt.Errorf("model must be exact, prefix*, *suffix, *contains*, or *")
	}
	return nil
}

func matchesGroupModelPattern(pattern, model string) bool {
	if validateGroupModelPattern(pattern) != nil {
		return false
	}
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(pattern, "*"), "*")
	switch {
	case pattern == "*":
		return true
	case strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*"):
		return strings.Contains(model, body)
	case strings.HasPrefix(pattern, "*"):
		return strings.HasSuffix(model, body)
	case strings.HasSuffix(pattern, "*"):
		return strings.HasPrefix(model, body)
	default:
		return model == pattern
	}
}

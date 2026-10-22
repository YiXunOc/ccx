package config

import (
	"fmt"
	"strings"
)

// validateChannelModelMapping keeps the physical-channel API contract aligned
// with the Web/Desktop editors: exact, nonempty, single-hop model identifiers.
func validateChannelModelMapping(mapping map[string]string) error {
	for source, target := range mapping {
		if !validChannelModelID(source) || !validChannelModelID(target) {
			return fmt.Errorf("invalid channel model mapping %q -> %q: expected exact model IDs without wildcards", source, target)
		}
	}
	return nil
}

func validChannelModelID(id string) bool {
	if id == "" || strings.TrimSpace(id) != id || strings.ContainsAny(id, "*\r\n\t ") || strings.Contains(id, "=>") {
		return false
	}
	return true
}

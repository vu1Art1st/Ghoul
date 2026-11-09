// Package envcompat centralizes the transition from legacy ARTEX_* environment
// variables to the canonical GHOUL_* names.
package envcompat

import (
	"os"
	"strings"
)

// Get returns the primary environment variable when it is non-empty, otherwise
// it falls back to the legacy name. This keeps existing deployments working
// while new configuration uses the Ghoul prefix.
func Get(primary, legacy string) string {
	if v := os.Getenv(primary); strings.TrimSpace(v) != "" {
		return v
	}
	return os.Getenv(legacy)
}

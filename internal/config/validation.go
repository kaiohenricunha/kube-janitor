package config

import (
	"fmt"
	"time"
)

// Validate checks that the config is safe and consistent.
// Returns a descriptive error for any unsafe or invalid configuration.
func (c *Config) Validate() error {
	if c.ScanInterval < 30*time.Second {
		return fmt.Errorf("scan-interval %s is too short (minimum 30s) — would cause excessive API load", c.ScanInterval)
	}

	if c.DefaultDeleteConfidenceThreshold < 0.7 {
		return fmt.Errorf(
			"default-delete-confidence-threshold %.2f is too low (minimum 0.7) — "+
				"low thresholds dramatically increase false-positive deletion risk",
			c.DefaultDeleteConfidenceThreshold,
		)
	}

	if c.DefaultDeleteConfidenceThreshold > 1.0 {
		return fmt.Errorf("default-delete-confidence-threshold %.2f exceeds 1.0 (maximum)", c.DefaultDeleteConfidenceThreshold)
	}

	if c.MetricsAddr == "" {
		return fmt.Errorf("metrics-bind-address must not be empty")
	}

	return nil
}

// ProtectedNamespaceSet returns the protected namespaces as a map for O(1) lookup.
func (c *Config) ProtectedNamespaceSet() map[string]struct{} {
	m := make(map[string]struct{}, len(c.ProtectedNamespaces))
	for _, ns := range c.ProtectedNamespaces {
		m[ns] = struct{}{}
	}
	return m
}

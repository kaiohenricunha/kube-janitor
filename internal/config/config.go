// Package config defines the controller manager configuration.
// Configuration comes from CLI flags (no config file in v1 — keep it simple).
package config

import (
	"time"
)

// Config holds all controller-level configuration.
// Values are set from CLI flags and validated at startup.
type Config struct {
	// MetricsAddr is the bind address for the Prometheus metrics endpoint.
	MetricsAddr string

	// ProbeAddr is the bind address for health/readiness probes.
	ProbeAddr string

	// EnableLeaderElection enables leader election for HA deployments.
	EnableLeaderElection bool

	// DryRun, when true, prevents any resource deletion regardless of policy.
	// This is the global safety override. Default: true.
	DryRun bool

	// OTelEndpoint is the OpenTelemetry collector gRPC endpoint.
	// Empty string disables tracing.
	OTelEndpoint string

	// ScanInterval is how often reconcilers re-evaluate all resources.
	// This ensures TTL-based expiry is caught even without resource changes.
	ScanInterval time.Duration

	// ProtectedNamespaces is the immutable list of namespaces that kube-janitor
	// will never touch, regardless of any policy configuration.
	ProtectedNamespaces []string

	// DefaultDeleteConfidenceThreshold is the minimum confidence required
	// for auto-deletion when a policy does not specify its own threshold.
	DefaultDeleteConfidenceThreshold float64

	// Development enables development logging mode (colorized, caller info).
	Development bool
}

// Default returns a Config with safe production defaults.
func Default() *Config {
	return &Config{
		MetricsAddr:                      ":8080",
		ProbeAddr:                        ":8081",
		EnableLeaderElection:             false,
		DryRun:                           true, // safe default
		ScanInterval:                     5 * time.Minute,
		DefaultDeleteConfidenceThreshold: 0.9,
		ProtectedNamespaces: []string{
			"kube-system",
			"kube-public",
			"default",
			"kube-node-lease",
		},
		Development: false,
	}
}

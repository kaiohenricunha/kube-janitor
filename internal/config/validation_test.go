package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kaiohenricunha/kube-janitor/internal/config"
)

func TestValidate_DefaultConfigIsValid(t *testing.T) {
	require.NoError(t, config.Default().Validate())
}

func TestValidate_RejectsDryRunFalse(t *testing.T) {
	// Safety spec (ADR-0002, migration Phase 0): the controller is frozen.
	// Deletion must be impossible, so --dry-run=false is refused at startup.
	cfg := config.Default()
	cfg.DryRun = false

	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ADR-0002")
	assert.Contains(t, err.Error(), "dry-run")
}

func TestValidate_ScanIntervalBoundary(t *testing.T) {
	cfg := config.Default()

	cfg.ScanInterval = 30 * time.Second
	require.NoError(t, cfg.Validate(), "30s is the minimum and must be accepted")

	cfg.ScanInterval = 29 * time.Second
	require.Error(t, cfg.Validate(), "below 30s must be rejected")
}

func TestValidate_ConfidenceThresholdBoundaries(t *testing.T) {
	cases := []struct {
		threshold float64
		wantErr   bool
	}{
		{0.69, true},
		{0.7, false},
		{1.0, false},
		{1.01, true},
	}
	for _, c := range cases {
		cfg := config.Default()
		cfg.DefaultDeleteConfidenceThreshold = c.threshold
		err := cfg.Validate()
		if c.wantErr {
			assert.Error(t, err, "threshold %.2f", c.threshold)
		} else {
			assert.NoError(t, err, "threshold %.2f", c.threshold)
		}
	}
}

func TestValidate_RejectsEmptyMetricsAddr(t *testing.T) {
	cfg := config.Default()
	cfg.MetricsAddr = ""
	require.Error(t, cfg.Validate())
}

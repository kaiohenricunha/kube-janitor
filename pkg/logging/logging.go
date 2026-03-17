// Package logging provides structured logging setup for kube-janitor.
// All logging uses the logr interface backed by zap for production use.
package logging

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/log"

	zapr "github.com/go-logr/zapr"
)

// Init initializes the global controller-runtime logger with a production zap logger.
// level: 0=info, 1=debug, 2=trace (passed as zapcore.Level offset).
func Init(development bool, level int) error {
	var cfg zap.Config
	if development {
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	} else {
		cfg = zap.NewProductionConfig()
		cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	}

	// Adjust log level — controller-runtime uses V(n) for debug levels.
	cfg.Level = zap.NewAtomicLevelAt(zapcore.Level(-level))

	zapLog, err := cfg.Build(zap.AddCallerSkip(1))
	if err != nil {
		return err
	}

	// Set the controller-runtime global logger.
	log.SetLogger(zapr.NewLogger(zapLog))
	return nil
}

// StandardFields returns common logr.Logger fields that should be present
// on every log line in a reconcile context.
//
// Usage:
//
//	log := logger.WithValues(logging.StandardFields("namespace", req.Name, "abc-123")...)
func StandardFields(controller, objectName, reconcileID string) []interface{} {
	return []interface{}{
		"controller", controller,
		"object", objectName,
		"reconcileID", reconcileID,
	}
}

// Package tracing provides OpenTelemetry tracing setup for kube-janitor.
// Tracing is optional: if the OTLP endpoint is empty, a no-op tracer is used.
//
// Spans to instrument (in order):
//   - <controller>.reconcile   — root span per reconcile call
//   - classifier.classify       — classification strategy chain
//   - resolver.resolve          — per-resolver call
//   - policy.evaluate           — policy evaluation
//   - action.execute            — action execution (delete or report)
//
// Common span attributes:
//   - k8s.resource.kind
//   - k8s.namespace.name
//   - k8s.resource.name
//   - janitor.class
//   - janitor.confidence
//   - janitor.action
//   - janitor.dry_run
//   - janitor.reconcile_id
package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/kaiohenricunha/kube-janitor"

// Init initializes the global OpenTelemetry tracer with an OTLP gRPC exporter.
// Returns a shutdown function that must be deferred to flush spans before exit.
// If endpoint is empty, no-op tracing is used and shutdown is a no-op.
func Init(ctx context.Context, endpoint string) (shutdown func(context.Context) error, err error) {
	if endpoint == "" {
		// No-op: return a noop tracer.
		return func(_ context.Context) error { return nil }, nil
	}

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("creating OTLP exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("kube-janitor"),
		),
		resource.WithFromEnv(),
		resource.WithProcess(),
	)
	if err != nil {
		return nil, fmt.Errorf("creating OTel resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}

// Tracer returns a named tracer from the global provider.
// Use this in each package that needs to create spans:
//
//	tracer := tracing.Tracer()
//	ctx, span := tracer.Start(ctx, "classifier.classify")
//	defer span.End()
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

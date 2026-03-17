// Package metrics provides Prometheus metrics for kube-janitor.
// All metrics follow the naming convention: janitor_<noun>_<verb>_<unit>.
// Labels are kept low-cardinality: kind, class, action — never per-resource labels.
package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Recorder holds all kube-janitor Prometheus metrics.
type Recorder struct {
	// ResourcesScanned counts the total number of resources scanned per kind.
	ResourcesScanned *prometheus.CounterVec
	// ResourcesClassified counts resources by their classification outcome.
	ResourcesClassified *prometheus.CounterVec
	// ActionsTaken counts actions taken (report, delete, dryrun-delete) by kind.
	ActionsTaken *prometheus.CounterVec
	// ReconcileDuration observes how long each reconcile loop takes.
	ReconcileDuration *prometheus.HistogramVec
	// ReconcileErrors counts reconcile errors by controller.
	ReconcileErrors *prometheus.CounterVec
	// PolicyEvaluations counts policy evaluation outcomes by policy name and action.
	PolicyEvaluations *prometheus.CounterVec
}

var (
	defaultRecorder *Recorder
	once            sync.Once
)

// Register registers all metrics with the controller-runtime metrics registry.
// Must be called once before starting the manager. Subsequent calls are no-ops.
func Register() {
	once.Do(func() {
		factory := promauto.With(ctrlmetrics.Registry)

		defaultRecorder = &Recorder{
			ResourcesScanned: factory.NewCounterVec(
				prometheus.CounterOpts{
					Name: "janitor_resources_scanned_total",
					Help: "Total number of resources scanned by kube-janitor, by kind.",
				},
				[]string{"kind"},
			),
			ResourcesClassified: factory.NewCounterVec(
				prometheus.CounterOpts{
					Name: "janitor_resources_classified_total",
					Help: "Total number of resources classified, by kind and class.",
				},
				[]string{"kind", "class"},
			),
			ActionsTaken: factory.NewCounterVec(
				prometheus.CounterOpts{
					Name: "janitor_actions_total",
					Help: "Total number of actions taken by kube-janitor, by kind, action, and dry_run mode.",
				},
				[]string{"kind", "action", "dry_run"},
			),
			ReconcileDuration: factory.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "janitor_reconcile_duration_seconds",
					Help:    "Duration of reconcile loops in seconds, by controller.",
					Buckets: prometheus.DefBuckets,
				},
				[]string{"controller"},
			),
			ReconcileErrors: factory.NewCounterVec(
				prometheus.CounterOpts{
					Name: "janitor_reconcile_errors_total",
					Help: "Total number of reconcile errors by controller.",
				},
				[]string{"controller", "kind"},
			),
			PolicyEvaluations: factory.NewCounterVec(
				prometheus.CounterOpts{
					Name: "janitor_policy_evaluations_total",
					Help: "Total number of policy evaluations, by policy name and resulting action.",
				},
				[]string{"policy", "action"},
			),
		}
	})
}

// DefaultRecorder returns the global Recorder. Panics if Register() was not called.
func DefaultRecorder() *Recorder {
	if defaultRecorder == nil {
		panic("metrics.Register() must be called before DefaultRecorder()")
	}
	return defaultRecorder
}

// RecordScan increments the scanned counter for a resource kind.
func (r *Recorder) RecordScan(kind string) {
	r.ResourcesScanned.WithLabelValues(kind).Inc()
}

// RecordClassification increments the classified counter.
func (r *Recorder) RecordClassification(kind, class string) {
	r.ResourcesClassified.WithLabelValues(kind, class).Inc()
}

// RecordAction increments the actions counter.
func (r *Recorder) RecordAction(kind, action string, dryRun bool) {
	dryRunLabel := "false"
	if dryRun {
		dryRunLabel = "true"
	}
	r.ActionsTaken.WithLabelValues(kind, action, dryRunLabel).Inc()
}

// RecordError increments the reconcile error counter.
func (r *Recorder) RecordError(kind string) {
	r.ReconcileErrors.WithLabelValues("unknown", kind).Inc()
}

// RecordReconcileError increments the reconcile error counter with a controller label.
func (r *Recorder) RecordReconcileError(controller, kind string) {
	r.ReconcileErrors.WithLabelValues(controller, kind).Inc()
}

// RecordPolicyEvaluation increments the policy evaluation counter.
func (r *Recorder) RecordPolicyEvaluation(policyName, action string) {
	if policyName == "" {
		policyName = "<default>"
	}
	r.PolicyEvaluations.WithLabelValues(policyName, action).Inc()
}

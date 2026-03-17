// main is the entrypoint for the kube-janitor controller manager.
// It initializes all dependencies and starts the controller manager.
package main

import (
	"context"
	"flag"
	"os"
	"time"

	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	janitorv1alpha1 "github.com/kaiohenricunha/kube-janitor/api/v1alpha1"
	"github.com/kaiohenricunha/kube-janitor/internal/action"
	"github.com/kaiohenricunha/kube-janitor/internal/classifier"
	"github.com/kaiohenricunha/kube-janitor/internal/config"
	"github.com/kaiohenricunha/kube-janitor/internal/controller"
	"github.com/kaiohenricunha/kube-janitor/internal/policy"
	"github.com/kaiohenricunha/kube-janitor/internal/report"
	"github.com/kaiohenricunha/kube-janitor/internal/resolver"
	"github.com/kaiohenricunha/kube-janitor/pkg/metrics"
	"github.com/kaiohenricunha/kube-janitor/pkg/tracing"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(janitorv1alpha1.AddToScheme(scheme))
}

func main() {
	cfg := config.Default()

	flag.StringVar(&cfg.MetricsAddr, "metrics-bind-address", cfg.MetricsAddr,
		"The address the Prometheus metrics endpoint binds to.")
	flag.StringVar(&cfg.ProbeAddr, "health-probe-bind-address", cfg.ProbeAddr,
		"The address the health probe endpoint binds to.")
	flag.BoolVar(&cfg.EnableLeaderElection, "leader-elect", cfg.EnableLeaderElection,
		"Enable leader election for high-availability deployments.")
	flag.BoolVar(&cfg.DryRun, "dry-run", cfg.DryRun,
		"If true (default), no resources will be deleted. Must be explicitly set to false to enable deletion.")
	flag.StringVar(&cfg.OTelEndpoint, "otel-endpoint", cfg.OTelEndpoint,
		"OpenTelemetry collector gRPC endpoint (e.g., localhost:4317). Empty disables tracing.")
	flag.DurationVar(&cfg.ScanInterval, "scan-interval", cfg.ScanInterval,
		"How often reconcilers re-evaluate all resources for TTL-based expiry.")
	flag.BoolVar(&cfg.Development, "development", cfg.Development,
		"Enable development logging mode (human-readable output).")

	zapOpts := zap.Options{
		Development: cfg.Development,
		TimeEncoder: zapcore.ISO8601TimeEncoder,
	}
	zapOpts.BindFlags(flag.CommandLine)
	flag.Parse()

	// Validate configuration before starting.
	if err := cfg.Validate(); err != nil {
		_, _ = os.Stderr.WriteString("invalid configuration: " + err.Error() + "\n")
		os.Exit(1)
	}

	// Initialize logging.
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))
	setupLog.Info("starting kube-janitor",
		"dry-run", cfg.DryRun,
		"scan-interval", cfg.ScanInterval,
		"protected-namespaces", cfg.ProtectedNamespaces,
	)

	// Initialize distributed tracing (no-op if endpoint is empty).
	if cfg.OTelEndpoint != "" {
		shutdown, err := tracing.Init(context.Background(), cfg.OTelEndpoint)
		if err != nil {
			setupLog.Error(err, "failed to initialize OpenTelemetry tracing")
			os.Exit(1)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdown(ctx); err != nil {
				setupLog.Error(err, "failed to shutdown tracing provider")
			}
		}()
		setupLog.Info("tracing initialized", "endpoint", cfg.OTelEndpoint)
	}

	// Create the controller manager.
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: cfg.MetricsAddr,
		},
		HealthProbeBindAddress: cfg.ProbeAddr,
		LeaderElection:         cfg.EnableLeaderElection,
		LeaderElectionID:       "kube-janitor-leader-election.janitor.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	// Register Prometheus metrics.
	metrics.Register()
	m := metrics.DefaultRecorder()

	// Build the resolver registry.
	reg := resolver.NewRegistry()
	reg.Register(resolver.NewOwnerRefResolver())
	reg.Register(resolver.NewConfigMapResolver())
	reg.Register(resolver.NewNamespaceResolver("janitor.io/pr-state", time.Hour))

	// Build the classifier (strategy chain, priority order).
	protectedNS := cfg.ProtectedNamespaces
	clf := classifier.New(
		mgr.GetAPIReader(),
		ctrllog.Log.WithName("classifier"),
		classifier.NewProtectionStrategy("janitor.io/protected", protectedNS...),
		classifier.NewFinalizerStrategy(),
		classifier.NewTTLStrategy("janitor.io/ttl", 0),
		classifier.NewExpiresAtStrategy("janitor.io/expires-at"),
		classifier.NewOwnerRefStrategy(),
		classifier.NewResolverStrategy(reg),
	)

	// Build the policy engine.
	policyEngine := policy.NewDefaultEngine(cfg.DryRun, ctrllog.Log.WithName("policy"))

	// Build the action executor.
	exec := action.NewDefaultExecutor(
		mgr.GetClient(),
		mgr.GetAPIReader(),
		ctrllog.Log.WithName("executor"),
		m,
		cfg.ProtectedNamespaces,
	)

	// Build the reporter.
	reporter := report.NewEventReporter(
		mgr.GetEventRecorderFor("kube-janitor"),
		mgr.GetScheme(),
		ctrllog.Log.WithName("reporter"),
		true,
	)

	// Shared deps for all reconcilers.
	deps := controller.SharedDeps{
		Client:       mgr.GetClient(),
		APIReader:    mgr.GetAPIReader(),
		Scheme:       mgr.GetScheme(),
		Log:          ctrl.Log,
		Recorder:     mgr.GetEventRecorderFor("kube-janitor"),
		Classifier:   clf,
		PolicyEngine: policyEngine,
		Executor:     exec,
		Reporter:     reporter,
		Metrics:      m,
		Config:       cfg,
	}

	// Register reconcilers.
	if err := (&controller.NamespaceReconciler{SharedDeps: deps}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Namespace")
		os.Exit(1)
	}
	if err := (&controller.ConfigMapReconciler{SharedDeps: deps}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ConfigMap")
		os.Exit(1)
	}

	// Health checks.
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// Package controller contains all Kubernetes reconcilers for kube-janitor.
// Each reconciler handles one resource kind. They share a common reconcile pipeline:
// classify → evaluate policy → execute action → report.
package controller

import (
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kaiohenricunha/kube-janitor/internal/action"
	"github.com/kaiohenricunha/kube-janitor/internal/classifier"
	"github.com/kaiohenricunha/kube-janitor/internal/config"
	"github.com/kaiohenricunha/kube-janitor/internal/policy"
	"github.com/kaiohenricunha/kube-janitor/internal/report"
	"github.com/kaiohenricunha/kube-janitor/pkg/metrics"
)

// SharedDeps holds dependencies common to all reconcilers.
// Embed this in each reconciler struct.
type SharedDeps struct {
	Client       client.Client
	APIReader    client.Reader // direct API server reads, bypasses informer cache
	Scheme       *runtime.Scheme
	Log          logr.Logger
	Recorder     record.EventRecorder
	Classifier   classifier.Classifier
	PolicyEngine policy.Engine
	Executor     action.Executor
	Reporter     report.Reporter
	Metrics      *metrics.Recorder
	Config       *config.Config
}

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ConfigMapReconciler reconciles ConfigMap resources against JanitorPolicies.
//
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=janitor.io,resources=janitorpolicies,verbs=get;list;watch
type ConfigMapReconciler struct {
	SharedDeps
}

// SetupWithManager registers the reconciler.
func (r *ConfigMapReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		WithEventFilter(predicate.Or(
			predicate.GenerationChangedPredicate{},
			predicate.LabelChangedPredicate{},
			predicate.AnnotationChangedPredicate{},
		)).
		Complete(r)
}

// Reconcile implements reconcile.Reconciler.
func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	start := time.Now()
	log := r.Log.WithValues("configmap", req.NamespacedName, "controller", "configmap")
	defer func() {
		r.Metrics.ReconcileDuration.WithLabelValues("configmap").Observe(time.Since(start).Seconds())
	}()

	var cm corev1.ConfigMap
	if err := r.Client.Get(ctx, req.NamespacedName, &cm); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		r.Metrics.RecordReconcileError("configmap", "ConfigMap")
		return ctrl.Result{}, fmt.Errorf("getting configmap %s: %w", req.NamespacedName, err)
	}

	r.Metrics.RecordScan("ConfigMap")

	finding, err := r.Classifier.Classify(ctx, &cm)
	if err != nil {
		r.Metrics.RecordReconcileError("configmap", "ConfigMap")
		return ctrl.Result{}, fmt.Errorf("classifying configmap %s: %w", req.NamespacedName, err)
	}
	finding.ReconcileID = req.NamespacedName.String()
	r.Metrics.RecordClassification("ConfigMap", string(finding.Class))

	log.V(1).Info("classified", "class", finding.Class, "confidence", finding.Confidence)

	policies, err := listMatchingPoliciesForKind(ctx, r.Client, "ConfigMap")
	if err != nil {
		r.Metrics.RecordReconcileError("configmap", "ConfigMap")
		return ctrl.Result{}, fmt.Errorf("listing policies for configmap %s: %w", req.NamespacedName, err)
	}

	decision, err := r.PolicyEngine.Evaluate(ctx, &cm, finding, policies)
	if err != nil {
		r.Metrics.RecordReconcileError("configmap", "ConfigMap")
		return ctrl.Result{}, fmt.Errorf("evaluating policy for configmap %s: %w", req.NamespacedName, err)
	}
	r.Metrics.RecordPolicyEvaluation(decision.PolicyRef, string(decision.Action))

	if err := r.Executor.Execute(ctx, &cm, finding, decision); err != nil {
		r.Metrics.RecordReconcileError("configmap", "ConfigMap")
		return ctrl.Result{}, fmt.Errorf("executing action for configmap %s: %w", req.NamespacedName, err)
	}

	if decision.Action != "" && finding.Class != "active" && finding.Class != "unknown" {
		if err := r.Reporter.Report(ctx, &cm, finding, decision); err != nil {
			log.Error(err, "failed to emit report event — continuing")
		}
	}

	return ctrl.Result{RequeueAfter: r.Config.ScanInterval}, nil
}

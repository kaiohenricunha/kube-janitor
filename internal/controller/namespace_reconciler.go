package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	janitorv1alpha1 "github.com/kaiohenricunha/kube-janitor/api/v1alpha1"
)

// NamespaceReconciler reconciles Namespace resources against JanitorPolicies.
//
// Reconcile loop:
//  1. Fetch the Namespace; skip if not found.
//  2. Classify using the classifier strategy chain.
//  3. List JanitorPolicies that target the Namespace kind.
//  4. Evaluate the finding against policies.
//  5. Execute the ActionDecision (dry-run or real).
//  6. Emit Kubernetes Event and record metrics.
//  7. Requeue after ScanInterval for TTL re-evaluation.
//
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=janitor.io,resources=janitorpolicies,verbs=get;list;watch
type NamespaceReconciler struct {
	SharedDeps
}

// SetupWithManager registers the reconciler with the controller manager.
func (r *NamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		WithEventFilter(predicate.Or(
			predicate.GenerationChangedPredicate{},
			predicate.LabelChangedPredicate{},
			predicate.AnnotationChangedPredicate{},
		)).
		Complete(r)
}

// Reconcile implements reconcile.Reconciler.
func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	start := time.Now()
	log := r.Log.WithValues("namespace", req.Name, "controller", "namespace")
	defer func() {
		r.Metrics.ReconcileDuration.WithLabelValues("namespace").Observe(time.Since(start).Seconds())
	}()

	// Fetch the Namespace.
	var ns corev1.Namespace
	if err := r.Client.Get(ctx, req.NamespacedName, &ns); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		r.Metrics.RecordReconcileError("namespace", "Namespace")
		return ctrl.Result{}, fmt.Errorf("getting namespace %s: %w", req.Name, err)
	}

	// Skip namespaces being terminated — no action possible.
	if ns.Status.Phase == corev1.NamespaceTerminating {
		log.V(1).Info("namespace is terminating — skipping")
		return ctrl.Result{RequeueAfter: r.Config.ScanInterval}, nil
	}

	r.Metrics.RecordScan("Namespace")

	// Classify the namespace.
	finding, err := r.Classifier.Classify(ctx, &ns)
	if err != nil {
		r.Metrics.RecordReconcileError("namespace", "Namespace")
		return ctrl.Result{}, fmt.Errorf("classifying namespace %s: %w", req.Name, err)
	}
	finding.ReconcileID = req.Name
	r.Metrics.RecordClassification("Namespace", string(finding.Class))

	log.V(1).Info("classified", "class", finding.Class, "confidence", finding.Confidence)

	// List policies that target Namespace kind.
	policies, err := r.listMatchingPolicies(ctx, "Namespace")
	if err != nil {
		r.Metrics.RecordReconcileError("namespace", "Namespace")
		return ctrl.Result{}, fmt.Errorf("listing policies for namespace %s: %w", req.Name, err)
	}

	// Evaluate policy.
	decision, err := r.PolicyEngine.Evaluate(ctx, &ns, finding, policies)
	if err != nil {
		r.Metrics.RecordReconcileError("namespace", "Namespace")
		return ctrl.Result{}, fmt.Errorf("evaluating policy for namespace %s: %w", req.Name, err)
	}
	r.Metrics.RecordPolicyEvaluation(decision.PolicyRef, string(decision.Action))

	// Execute action.
	if err := r.Executor.Execute(ctx, &ns, finding, decision); err != nil {
		r.Metrics.RecordReconcileError("namespace", "Namespace")
		return ctrl.Result{}, fmt.Errorf("executing action for namespace %s: %w", req.Name, err)
	}

	// Report actionable non-trivial findings.
	if decision.Action != "" && finding.Class != "active" && finding.Class != "unknown" {
		if err := r.Reporter.Report(ctx, &ns, finding, decision); err != nil {
			log.Error(err, "failed to emit report event — continuing")
		}
	}

	return ctrl.Result{RequeueAfter: r.Config.ScanInterval}, nil
}

// listMatchingPolicies returns all JanitorPolicies targeting the given resource kind.
func (r *NamespaceReconciler) listMatchingPolicies(ctx context.Context, kind string) ([]janitorv1alpha1.JanitorPolicy, error) {
	var policyList janitorv1alpha1.JanitorPolicyList
	if err := r.Client.List(ctx, &policyList); err != nil {
		return nil, fmt.Errorf("listing janitor policies: %w", err)
	}
	var matching []janitorv1alpha1.JanitorPolicy
	for _, p := range policyList.Items {
		for _, k := range p.Spec.Selector.Kinds {
			if k.Kind == kind {
				matching = append(matching, p)
				break
			}
		}
	}
	return matching, nil
}

// listMatchingPoliciesForKind is a shared helper used by all reconcilers.
func listMatchingPoliciesForKind(ctx context.Context, c client.Client, kind string) ([]janitorv1alpha1.JanitorPolicy, error) {
	var policyList janitorv1alpha1.JanitorPolicyList
	if err := c.List(ctx, &policyList); err != nil {
		return nil, fmt.Errorf("listing janitor policies: %w", err)
	}
	var matching []janitorv1alpha1.JanitorPolicy
	for _, p := range policyList.Items {
		for _, k := range p.Spec.Selector.Kinds {
			if k.Kind == kind {
				matching = append(matching, p)
				break
			}
		}
	}
	return matching, nil
}

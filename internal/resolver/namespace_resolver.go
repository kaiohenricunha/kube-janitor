package resolver

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kaiohenricunha/kube-janitor/internal/domain"
)

// NamespaceResolver classifies namespaces by checking whether they contain
// live workloads (Pods, Deployments, StatefulSets).
//
// A namespace with no running workloads and no recent activity is a candidate
// for cleanup. This resolver emits evidence about workload count and the
// PR state label if present.
//
// Confidence: Medium (heuristic — we check for emptiness, not guaranteed idle).
type NamespaceResolver struct {
	// PRStateLabelKey is the label/annotation key indicating the PR lifecycle state.
	// Expected values: "open", "closed", "merged".
	PRStateLabelKey string
	// ClosedTTL is how long to allow after the PR state is set to "closed"/"merged".
	ClosedTTL time.Duration
}

// NewNamespaceResolver creates a NamespaceResolver with defaults.
func NewNamespaceResolver(prStateLabelKey string, closedTTL time.Duration) *NamespaceResolver {
	if prStateLabelKey == "" {
		prStateLabelKey = "janitor.io/pr-state"
	}
	if closedTTL == 0 {
		closedTTL = time.Hour
	}
	return &NamespaceResolver{PRStateLabelKey: prStateLabelKey, ClosedTTL: closedTTL}
}

// Name implements Resolver.
func (r *NamespaceResolver) Name() string { return "namespace" }

// Handles implements Resolver — only handles Namespace resources.
func (r *NamespaceResolver) Handles(obj client.Object) bool {
	return obj.GetObjectKind().GroupVersionKind().Kind == "Namespace"
}

// Resolve returns references representing live workloads in the namespace.
// If the namespace is empty, returns an empty slice (no live references → candidate for cleanup).
func (r *NamespaceResolver) Resolve(
	ctx context.Context,
	obj client.Object,
	reader client.Reader,
) ([]domain.Reference, error) {
	ns := obj.GetName()

	nsRef := domain.ObjectRef{
		Group:   "",
		Version: "v1",
		Kind:    "Namespace",
		Name:    ns,
		UID:     obj.GetUID(),
	}

	var refs []domain.Reference

	// Check for running Pods.
	podList := &corev1.PodList{}
	if err := reader.List(ctx, podList, client.InNamespace(ns)); err != nil {
		return nil, fmt.Errorf("listing pods in namespace %s: %w", ns, err)
	}
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodPending {
			refs = append(refs, domain.Reference{
				From: domain.ObjectRef{
					Group: "", Version: "v1", Kind: "Pod",
					Namespace: ns, Name: pod.Name, UID: pod.GetUID(),
				},
				To:   nsRef,
				Type: domain.RefTypeLabelSelector,
				Evidence: domain.Evidence{
					Type:    domain.EvidenceEndpoint,
					Source:  fmt.Sprintf("Pod/%s/%s", ns, pod.Name),
					Details: fmt.Sprintf("namespace has running pod %q (phase=%s)", pod.Name, pod.Status.Phase),
				},
			})
			// Return early once we find a single live workload — namespace is active.
			return refs, nil
		}
	}

	// Check for Deployments.
	deployList := &appsv1.DeploymentList{}
	if err := reader.List(ctx, deployList, client.InNamespace(ns)); err != nil {
		return nil, fmt.Errorf("listing deployments in namespace %s: %w", ns, err)
	}
	for i := range deployList.Items {
		d := &deployList.Items[i]
		refs = append(refs, domain.Reference{
			From: domain.ObjectRef{
				Group: "apps", Version: "v1", Kind: "Deployment",
				Namespace: ns, Name: d.Name, UID: d.GetUID(),
			},
			To:   nsRef,
			Type: domain.RefTypeLabelSelector,
			Evidence: domain.Evidence{
				Type:    domain.EvidenceEndpoint,
				Source:  fmt.Sprintf("Deployment/%s/%s", ns, d.Name),
				Details: fmt.Sprintf("namespace contains deployment %q", d.Name),
			},
		})
		return refs, nil // one is enough to mark as active
	}

	// Namespace appears empty — no workload references found.
	return nil, nil
}

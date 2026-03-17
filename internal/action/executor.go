// Package action implements the action executor.
// It carries out ActionDecisions produced by the policy engine,
// performing deletions (or simulating them in dry-run mode) with
// pre-flight safety checks.
package action

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kaiohenricunha/kube-janitor/internal/domain"
	"github.com/kaiohenricunha/kube-janitor/pkg/metrics"
)

// Executor carries out ActionDecisions.
type Executor interface {
	Execute(ctx context.Context, obj client.Object, finding domain.Finding, decision domain.ActionDecision) error
}

// DefaultExecutor performs deletions with pre-flight safety checks.
// Safety checks performed before every deletion:
//  1. Re-GET the object (using the API reader, not cache) to ensure it still exists.
//  2. Verify the UID matches — catches the case where the object was recreated.
//  3. Verify no live ownerReferences exist (double-check against stale cache).
//  4. Verify no blocking finalizers exist.
//  5. Verify the object is not in a protected namespace.
type DefaultExecutor struct {
	client           client.Client
	apiReader        client.Reader // direct API server reads, bypasses cache
	log              logr.Logger
	metrics          *metrics.Recorder
	protectedNS      map[string]struct{}
}

// NewDefaultExecutor creates a DefaultExecutor.
func NewDefaultExecutor(
	c client.Client,
	apiReader client.Reader,
	log logr.Logger,
	m *metrics.Recorder,
	protectedNamespaces []string,
) *DefaultExecutor {
	ns := make(map[string]struct{}, len(protectedNamespaces))
	for _, n := range protectedNamespaces {
		ns[n] = struct{}{}
	}
	return &DefaultExecutor{
		client:      c,
		apiReader:   apiReader,
		log:         log,
		metrics:     m,
		protectedNS: ns,
	}
}

// Execute carries out the ActionDecision for the given object.
func (e *DefaultExecutor) Execute(
	ctx context.Context,
	obj client.Object,
	finding domain.Finding,
	decision domain.ActionDecision,
) error {
	log := e.log.WithValues(
		"object", finding.ObjectRef.String(),
		"class", finding.Class,
		"action", decision.Action,
		"dryRun", decision.DryRun,
		"reconcileID", decision.ReconcileID,
	)

	switch decision.Action {
	case domain.ActionNone:
		return nil

	case domain.ActionReport:
		log.Info("finding reported", "reason", decision.Reason)
		e.metrics.RecordAction(finding.ObjectRef.Kind, string(domain.ActionReport), decision.DryRun)
		return nil

	case domain.ActionDelete:
		if decision.DryRun {
			log.Info("dry-run delete: would delete resource", "reason", decision.Reason)
			e.metrics.RecordAction(finding.ObjectRef.Kind, "dryrun-delete", true)
			return nil
		}
		return e.delete(ctx, obj, finding, decision, log)

	default:
		return fmt.Errorf("unknown action %q", decision.Action)
	}
}

// delete performs the actual deletion with pre-flight safety checks.
func (e *DefaultExecutor) delete(
	ctx context.Context,
	obj client.Object,
	finding domain.Finding,
	decision domain.ActionDecision,
	log logr.Logger,
) error {
	ref := finding.ObjectRef

	// Pre-flight check 1: protected namespace.
	if _, ok := e.protectedNS[ref.Namespace]; ok {
		return fmt.Errorf("SAFETY: refusing to delete %s: namespace %q is protected", ref, ref.Namespace)
	}
	if ref.Namespace == "" {
		if _, ok := e.protectedNS[ref.Name]; ok {
			return fmt.Errorf("SAFETY: refusing to delete namespace %q: it is in the protected namespace list", ref.Name)
		}
	}

	// Pre-flight check 2: re-GET the object from the API server (not cache).
	fresh := obj.DeepCopyObject().(client.Object)
	err := e.apiReader.Get(ctx, types.NamespacedName{
		Namespace: ref.Namespace,
		Name:      ref.Name,
	}, fresh)
	if errors.IsNotFound(err) {
		log.Info("object already deleted — skipping", "object", ref)
		return nil
	}
	if err != nil {
		return fmt.Errorf("re-fetching object before delete: %w", err)
	}

	// Pre-flight check 3: UID must match.
	if fresh.GetUID() != ref.UID {
		return fmt.Errorf("SAFETY: UID mismatch for %s (expected %s, got %s) — object may have been recreated; skipping deletion",
			ref, ref.UID, fresh.GetUID())
	}

	// Pre-flight check 4: no blocking finalizers.
	for _, f := range fresh.GetFinalizers() {
		if f != "janitor.io/cleanup" {
			return fmt.Errorf("SAFETY: object %s has finalizer %q — not deleting", ref, f)
		}
	}

	// Pre-flight check 5: no live ownerReferences.
	if len(fresh.GetOwnerReferences()) > 0 {
		return fmt.Errorf("SAFETY: object %s has ownerReferences — not deleting (owner should handle GC)", ref)
	}

	log.Info("deleting resource",
		"object", ref,
		"reason", decision.Reason,
		"policy", decision.PolicyRef,
		"confidence", finding.Confidence,
		"timestamp", time.Now().UTC(),
	)

	if err := e.client.Delete(ctx, fresh); err != nil {
		if errors.IsNotFound(err) {
			log.Info("object already deleted by another process — skipping")
			return nil
		}
		e.metrics.RecordError(ref.Kind)
		return fmt.Errorf("deleting %s: %w", ref, err)
	}

	e.metrics.RecordAction(ref.Kind, string(domain.ActionDelete), false)
	log.Info("deleted resource successfully", "object", ref)
	return nil
}

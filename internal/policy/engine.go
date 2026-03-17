// Package policy implements the policy evaluation engine.
// It translates a Finding into an ActionDecision by consulting JanitorPolicy CRDs,
// per-resource annotation overrides, and built-in safety defaults.
//
// Policy hierarchy (highest to lowest priority):
//  1. Per-resource annotation override (e.g., janitor.io/action: skip)
//  2. Matching JanitorPolicy CRDs (most specific selector wins)
//  3. Built-in defaults (DryRun mode, no action for unknown class)
package policy

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"

	janitorv1alpha1 "github.com/kaiohenricunha/kube-janitor/api/v1alpha1"
	"github.com/kaiohenricunha/kube-janitor/internal/domain"
)

const (
	// annotationActionOverride allows per-resource action override.
	// Values: "skip", "delete", "report"
	annotationActionOverride = "janitor.io/action"

	// defaultDeleteConfidenceThreshold is the minimum confidence required for deletion
	// when no policy specifies a threshold.
	defaultDeleteConfidenceThreshold = 0.9
	// defaultReportConfidenceThreshold is the minimum confidence for report-only actions.
	defaultReportConfidenceThreshold = 0.6
)

// Engine evaluates a Finding against applicable JanitorPolicies
// and returns an ActionDecision.
type Engine interface {
	Evaluate(ctx context.Context, obj AnnotationReader, finding domain.Finding, policies []janitorv1alpha1.JanitorPolicy) (domain.ActionDecision, error)
}

// AnnotationReader is a minimal interface to read annotations from the target object.
// Satisfied by any client.Object (corev1.Namespace, corev1.ConfigMap, etc.).
type AnnotationReader interface {
	GetAnnotations() map[string]string
}

// DefaultEngine is the standard policy evaluation engine.
type DefaultEngine struct {
	// GlobalDryRun overrides all policies to DryRun when true.
	// Set from the --dry-run controller flag.
	GlobalDryRun bool
	log          logr.Logger
}

// NewDefaultEngine creates a DefaultEngine.
func NewDefaultEngine(globalDryRun bool, log logr.Logger) *DefaultEngine {
	return &DefaultEngine{GlobalDryRun: globalDryRun, log: log}
}

// Evaluate returns an ActionDecision for the given finding.
func (e *DefaultEngine) Evaluate(
	ctx context.Context,
	obj AnnotationReader,
	finding domain.Finding,
	policies []janitorv1alpha1.JanitorPolicy,
) (domain.ActionDecision, error) {
	now := time.Now().UTC()
	reconcileID := finding.ReconcileID

	// Safety: protected resources never receive a delete action, regardless of policy.
	if finding.Class == domain.ClassProtected {
		return domain.ActionDecision{
			Action:      domain.ActionNone,
			DryRun:      e.GlobalDryRun,
			Reason:      "resource is protected — no action taken",
			DecidedAt:   now,
			ReconcileID: reconcileID,
		}, nil
	}

	// Safety: unknown classification receives no action.
	if finding.Class == domain.ClassUnknown {
		return domain.ActionDecision{
			Action:      domain.ActionNone,
			DryRun:      e.GlobalDryRun,
			Reason:      "classification is unknown — no action taken",
			DecidedAt:   now,
			ReconcileID: reconcileID,
		}, nil
	}

	// Safety: blocked resources receive no delete action.
	if finding.Class == domain.ClassBlocked {
		return domain.ActionDecision{
			Action:      domain.ActionReport,
			DryRun:      e.GlobalDryRun,
			Reason:      "resource has blocking finalizers — reporting only, no deletion",
			DecidedAt:   now,
			ReconcileID: reconcileID,
		}, nil
	}

	// Check per-resource annotation override.
	if decision, ok := e.annotationOverride(obj, finding, now, reconcileID); ok {
		return decision, nil
	}

	// Find the best matching policy.
	policy, found := e.findMatchingPolicy(finding, policies)
	if !found {
		// No policy applies — use safe default (report for actionable classes, none otherwise).
		return e.defaultDecision(finding, now, reconcileID), nil
	}

	return e.evaluatePolicy(finding, policy, now, reconcileID), nil
}

func (e *DefaultEngine) annotationOverride(
	obj AnnotationReader,
	finding domain.Finding,
	now time.Time,
	reconcileID string,
) (domain.ActionDecision, bool) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return domain.ActionDecision{}, false
	}
	override, ok := annotations[annotationActionOverride]
	if !ok {
		return domain.ActionDecision{}, false
	}

	switch override {
	case "skip":
		return domain.ActionDecision{
			Action:      domain.ActionNone,
			DryRun:      e.GlobalDryRun,
			Reason:      fmt.Sprintf("per-resource annotation %s=skip", annotationActionOverride),
			DecidedAt:   now,
			ReconcileID: reconcileID,
		}, true
	case "report":
		return domain.ActionDecision{
			Action:      domain.ActionReport,
			DryRun:      e.GlobalDryRun,
			Reason:      fmt.Sprintf("per-resource annotation %s=report", annotationActionOverride),
			DecidedAt:   now,
			ReconcileID: reconcileID,
		}, true
	case "delete":
		if e.GlobalDryRun {
			return domain.ActionDecision{
				Action:      domain.ActionDelete,
				DryRun:      true,
				Reason:      fmt.Sprintf("per-resource annotation %s=delete (global dry-run active)", annotationActionOverride),
				DecidedAt:   now,
				ReconcileID: reconcileID,
			}, true
		}
		return domain.ActionDecision{
			Action:      domain.ActionDelete,
			DryRun:      false,
			Reason:      fmt.Sprintf("per-resource annotation %s=delete", annotationActionOverride),
			DecidedAt:   now,
			ReconcileID: reconcileID,
		}, true
	}
	return domain.ActionDecision{}, false
}

func (e *DefaultEngine) findMatchingPolicy(
	finding domain.Finding,
	policies []janitorv1alpha1.JanitorPolicy,
) (janitorv1alpha1.JanitorPolicy, bool) {
	for _, p := range policies {
		if e.policyMatchesFinding(p, finding) {
			return p, true
		}
	}
	return janitorv1alpha1.JanitorPolicy{}, false
}

func (e *DefaultEngine) policyMatchesFinding(
	policy janitorv1alpha1.JanitorPolicy,
	finding domain.Finding,
) bool {
	for _, kind := range policy.Spec.Selector.Kinds {
		if kind.Kind == finding.ObjectRef.Kind {
			return true
		}
	}
	return false
}

func (e *DefaultEngine) evaluatePolicy(
	finding domain.Finding,
	policy janitorv1alpha1.JanitorPolicy,
	now time.Time,
	reconcileID string,
) domain.ActionDecision {
	mode := policy.Spec.Action.Mode
	threshold := e.confidenceThreshold(policy, mode)
	policyRef := policy.Name

	// Global dry-run overrides even an explicit Delete policy.
	dryRun := e.GlobalDryRun || mode == janitorv1alpha1.ActionModeDryRun

	switch mode {
	case janitorv1alpha1.ActionModeDryRun:
		if float64(finding.Confidence) >= threshold {
			return domain.ActionDecision{
				Action:      domain.ActionDelete,
				DryRun:      true,
				Reason:      fmt.Sprintf("policy %s: mode=DryRun, would delete (confidence=%.2f >= threshold=%.2f)", policyRef, finding.Confidence, threshold),
				PolicyRef:   policyRef,
				DecidedAt:   now,
				ReconcileID: reconcileID,
			}
		}
		return domain.ActionDecision{
			Action:      domain.ActionReport,
			DryRun:      true,
			Reason:      fmt.Sprintf("policy %s: mode=DryRun, reporting (confidence=%.2f < threshold=%.2f)", policyRef, finding.Confidence, threshold),
			PolicyRef:   policyRef,
			DecidedAt:   now,
			ReconcileID: reconcileID,
		}

	case janitorv1alpha1.ActionModeReport:
		if float64(finding.Confidence) >= threshold {
			return domain.ActionDecision{
				Action:      domain.ActionReport,
				DryRun:      dryRun,
				Reason:      fmt.Sprintf("policy %s: mode=Report (confidence=%.2f >= threshold=%.2f)", policyRef, finding.Confidence, threshold),
				PolicyRef:   policyRef,
				DecidedAt:   now,
				ReconcileID: reconcileID,
			}
		}
		return domain.ActionDecision{
			Action:      domain.ActionNone,
			DryRun:      dryRun,
			Reason:      fmt.Sprintf("policy %s: mode=Report, below threshold (confidence=%.2f < threshold=%.2f)", policyRef, finding.Confidence, threshold),
			PolicyRef:   policyRef,
			DecidedAt:   now,
			ReconcileID: reconcileID,
		}

	case janitorv1alpha1.ActionModeDelete:
		if float64(finding.Confidence) >= threshold {
			return domain.ActionDecision{
				Action:      domain.ActionDelete,
				DryRun:      dryRun, // still respects GlobalDryRun
				Reason:      fmt.Sprintf("policy %s: mode=Delete (confidence=%.2f >= threshold=%.2f)", policyRef, finding.Confidence, threshold),
				PolicyRef:   policyRef,
				DecidedAt:   now,
				ReconcileID: reconcileID,
			}
		}
		// Below delete threshold — fall back to report.
		return domain.ActionDecision{
			Action:      domain.ActionReport,
			DryRun:      dryRun,
			Reason:      fmt.Sprintf("policy %s: mode=Delete but below threshold, reporting (confidence=%.2f < threshold=%.2f)", policyRef, finding.Confidence, threshold),
			PolicyRef:   policyRef,
			DecidedAt:   now,
			ReconcileID: reconcileID,
		}
	}

	return e.defaultDecision(finding, now, reconcileID)
}

func (e *DefaultEngine) confidenceThreshold(policy janitorv1alpha1.JanitorPolicy, mode janitorv1alpha1.ActionMode) float64 {
	if policy.Spec.Action.ConfidenceThreshold != nil {
		return *policy.Spec.Action.ConfidenceThreshold
	}
	switch mode {
	case janitorv1alpha1.ActionModeDelete:
		return defaultDeleteConfidenceThreshold
	default:
		return defaultReportConfidenceThreshold
	}
}

func (e *DefaultEngine) defaultDecision(finding domain.Finding, now time.Time, reconcileID string) domain.ActionDecision {
	// For actionable classes without a matching policy: report if confidence is sufficient.
	actionableClasses := map[domain.ResourceClass]bool{
		domain.ClassExpired:   true,
		domain.ClassOrphaned:  true,
		domain.ClassAbandoned: true,
		domain.ClassEphemeral: true,
	}
	if actionableClasses[finding.Class] && float64(finding.Confidence) >= defaultReportConfidenceThreshold {
		return domain.ActionDecision{
			Action:      domain.ActionReport,
			DryRun:      e.GlobalDryRun,
			Reason:      fmt.Sprintf("no matching policy; reporting by default (class=%s, confidence=%.2f)", finding.Class, finding.Confidence),
			DecidedAt:   now,
			ReconcileID: reconcileID,
		}
	}
	return domain.ActionDecision{
		Action:      domain.ActionNone,
		DryRun:      e.GlobalDryRun,
		Reason:      fmt.Sprintf("no matching policy and below report threshold (class=%s, confidence=%.2f)", finding.Class, finding.Confidence),
		DecidedAt:   now,
		ReconcileID: reconcileID,
	}
}

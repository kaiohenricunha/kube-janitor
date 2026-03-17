// Package domain defines the core types shared across kube-janitor subsystems.
// These types are intentionally decoupled from Kubernetes API machinery to keep
// the domain model clean and independently testable.
package domain

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// ObjectRef is an immutable, stable reference to a Kubernetes object.
// It carries enough information to uniquely identify the object and describe
// it in logs, events, and audit records.
type ObjectRef struct {
	Group     string    `json:"group"`
	Version   string    `json:"version"`
	Kind      string    `json:"kind"`
	Namespace string    `json:"namespace,omitempty"`
	Name      string    `json:"name"`
	UID       types.UID `json:"uid"`
}

// String returns a human-readable representation for logs and events.
func (r ObjectRef) String() string {
	if r.Namespace != "" {
		return fmt.Sprintf("%s/%s/%s", r.Kind, r.Namespace, r.Name)
	}
	return fmt.Sprintf("%s/%s", r.Kind, r.Name)
}

// ResourceClass is the classification outcome for a resource.
// Every resource processed by kube-janitor is assigned exactly one class.
type ResourceClass string

const (
	// ClassActive means the resource is confirmed to be actively referenced or in use.
	ClassActive ResourceClass = "active"
	// ClassEphemeral means the resource was created for a short-lived purpose (e.g., PR preview).
	ClassEphemeral ResourceClass = "ephemeral"
	// ClassExpired means the resource's TTL or expires-at timestamp has elapsed.
	ClassExpired ResourceClass = "expired"
	// ClassOrphaned means no live ownerReference points to this resource (structural orphan).
	ClassOrphaned ResourceClass = "orphaned"
	// ClassAbandoned means nothing semantically references this resource (semantic orphan).
	// Lower confidence than ClassOrphaned.
	ClassAbandoned ResourceClass = "abandoned"
	// ClassProtected means this resource is explicitly marked as protected and must not be deleted.
	ClassProtected ResourceClass = "protected"
	// ClassBlocked means deletion-blocking finalizers prevent cleanup.
	ClassBlocked ResourceClass = "blocked"
	// ClassUnknown means classification could not be determined. Triggers no action.
	ClassUnknown ResourceClass = "unknown"
)

// Confidence is a score in [0.0, 1.0] expressing certainty in a classification.
// Higher values indicate more evidence supporting the classification.
type Confidence float64

const (
	// ConfidenceLow is used for heuristic or partial findings.
	ConfidenceLow Confidence = 0.3
	// ConfidenceMedium is used when multiple independent signals agree.
	ConfidenceMedium Confidence = 0.6
	// ConfidenceHigh is used when evidence is conclusive (e.g., verified by API lookup).
	ConfidenceHigh Confidence = 0.9
)

// EvidenceType identifies the kind of evidence supporting a finding.
type EvidenceType string

const (
	EvidenceOwnerRef      EvidenceType = "ownerRef"
	EvidenceLabelSelector EvidenceType = "labelSelector"
	EvidenceAnnotation    EvidenceType = "annotation"
	EvidenceTTL           EvidenceType = "ttl"
	EvidenceExpiresAt     EvidenceType = "expiresAt"
	EvidenceVolumeMount   EvidenceType = "volumeMount"
	EvidenceEndpoint      EvidenceType = "endpoint"
	EvidenceAge           EvidenceType = "age"
	EvidenceFinalizer     EvidenceType = "finalizer"
	EvidencePRState       EvidenceType = "prState"
)

// Evidence is a single piece of information supporting a finding.
// Evidence is the primary mechanism for making classification decisions explainable.
type Evidence struct {
	// Type identifies what kind of evidence this is.
	Type EvidenceType `json:"type"`
	// Source is a human-readable identifier for where this evidence came from
	// (e.g., "Pod/default/my-pod", "annotation:janitor.io/ttl").
	Source string `json:"source"`
	// Details is a human-readable explanation of the evidence.
	Details string `json:"details"`
}

// Reason explains one aspect of why a resource received its classification.
// Multiple Reasons can exist for a single Finding.
type Reason struct {
	// Code is a machine-readable identifier for the reason (e.g., "ttl-expired").
	Code string `json:"code"`
	// Message is a human-readable explanation.
	Message string `json:"message"`
	// Evidence is the supporting evidence for this reason.
	Evidence []Evidence `json:"evidence,omitempty"`
}

// Finding is the central result of classifying a single Kubernetes resource.
// It carries everything needed for policy evaluation, action execution, and reporting.
type Finding struct {
	// ObjectRef identifies the classified resource.
	ObjectRef ObjectRef `json:"objectRef"`
	// Class is the classification outcome.
	Class ResourceClass `json:"class"`
	// Confidence expresses how certain the classification is.
	Confidence Confidence `json:"confidence"`
	// Reasons explains why this classification was assigned.
	Reasons []Reason `json:"reasons"`
	// ClassifiedAt records when this finding was produced.
	ClassifiedAt time.Time `json:"classifiedAt"`
	// ReconcileID links this finding to a specific reconcile loop for distributed tracing.
	ReconcileID string `json:"reconcileId,omitempty"`
}

// Action represents the possible outcomes of policy evaluation.
type Action string

const (
	// ActionNone means take no action on this resource.
	ActionNone Action = "none"
	// ActionReport means emit an event and update status, but do not delete.
	ActionReport Action = "report"
	// ActionDelete means delete the resource. Requires DryRun=false AND high confidence.
	ActionDelete Action = "delete"
)

// ActionDecision is the policy engine's verdict for a Finding.
// It records not just what to do, but why — for auditability.
type ActionDecision struct {
	// Action is what should happen to the resource.
	Action Action `json:"action"`
	// DryRun means the action should be simulated only. Even if Action=delete, no deletion occurs.
	DryRun bool `json:"dryRun"`
	// Reason explains why this decision was made.
	Reason string `json:"reason"`
	// PolicyRef is the name of the JanitorPolicy that triggered this decision.
	// Empty if the decision came from a built-in default.
	PolicyRef string `json:"policyRef,omitempty"`
	// DecidedAt records when the decision was made.
	DecidedAt time.Time `json:"decidedAt"`
	// ReconcileID links this decision to a specific reconcile loop.
	ReconcileID string `json:"reconcileId,omitempty"`
}

// ReferenceType describes the nature of a reference between two resources.
type ReferenceType string

const (
	RefTypeOwnerRef      ReferenceType = "ownerRef"
	RefTypeLabelSelector ReferenceType = "labelSelector"
	RefTypeVolumeMount   ReferenceType = "volumeMount"
	RefTypeEnvFrom       ReferenceType = "envFrom"
	RefTypeServiceAccount ReferenceType = "serviceAccount"
	RefTypeImagePullSecret ReferenceType = "imagePullSecret"
	RefTypeEndpoint      ReferenceType = "endpoint"
)

// Reference represents a directional relationship between two Kubernetes objects.
// Resolvers produce References to build the resource reference graph.
type Reference struct {
	// From is the resource that holds or creates the reference.
	From ObjectRef `json:"from"`
	// To is the resource being referenced.
	To ObjectRef `json:"to"`
	// Type describes the nature of the reference.
	Type ReferenceType `json:"type"`
	// Evidence is the supporting information for this reference.
	Evidence Evidence `json:"evidence"`
}

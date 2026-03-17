package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ActionMode defines how the policy acts on matching findings.
// +kubebuilder:validation:Enum=DryRun;Report;Delete
type ActionMode string

const (
	// ActionModeDryRun logs what would happen but takes no action.
	ActionModeDryRun ActionMode = "DryRun"
	// ActionModeReport emits Kubernetes Events and updates status but does not delete.
	ActionModeReport ActionMode = "Report"
	// ActionModeDelete deletes resources that meet the confidence threshold.
	// Requires --dry-run=false on the controller and confidence ≥ threshold.
	ActionModeDelete ActionMode = "Delete"
)

// ResourceKind identifies a Kubernetes resource type.
type ResourceKind struct {
	// Group is the API group (e.g., "" for core, "apps" for deployments).
	Group string `json:"group"`
	// Version is the API version (e.g., "v1").
	Version string `json:"version"`
	// Kind is the resource kind (e.g., "ConfigMap").
	Kind string `json:"kind"`
}

// ResourceSelector determines which resources a policy applies to.
type ResourceSelector struct {
	// Kinds lists the resource types this policy targets.
	// +kubebuilder:validation:MinItems=1
	Kinds []ResourceKind `json:"kinds"`

	// NamespaceSelector limits resources to namespaces matching this selector.
	// An empty selector matches all namespaces.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// LabelSelector further filters resources within selected namespaces.
	// +optional
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`
}

// TTLConfig configures time-to-live based expiry.
type TTLConfig struct {
	// Default is the TTL applied to resources that do not carry an explicit TTL annotation.
	// Uses Go duration format: "72h", "30m", "168h".
	// +optional
	Default *metav1.Duration `json:"default,omitempty"`

	// Annotation is the annotation key read for per-resource TTL values.
	// Defaults to "janitor.io/ttl".
	// +kubebuilder:default="janitor.io/ttl"
	// +optional
	Annotation string `json:"annotation,omitempty"`

	// ExpiresAtAnnotation is the annotation key for absolute expiry timestamps (RFC3339).
	// Defaults to "janitor.io/expires-at".
	// +kubebuilder:default="janitor.io/expires-at"
	// +optional
	ExpiresAtAnnotation string `json:"expiresAtAnnotation,omitempty"`
}

// ProtectionConfig defines resources that must never be cleaned up.
type ProtectionConfig struct {
	// Labels: any resource carrying all of these label key=value pairs is protected.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// AnnotationKey: resources with this annotation set to "true" are protected.
	// Defaults to "janitor.io/protected".
	// +kubebuilder:default="janitor.io/protected"
	// +optional
	AnnotationKey string `json:"annotationKey,omitempty"`

	// Namespaces lists additional namespaces that are always protected beyond the
	// controller-level defaults (kube-system, kube-public, default, kube-node-lease).
	// +optional
	Namespaces []string `json:"namespaces,omitempty"`
}

// OrphanConfig configures orphan detection for resources without live owners.
type OrphanConfig struct {
	// Enabled turns on orphan detection for this policy.
	Enabled bool `json:"enabled"`

	// MinAge is the minimum resource age before it can be considered orphaned.
	// Prevents false positives on newly-created resources.
	// +optional
	MinAge *metav1.Duration `json:"minAge,omitempty"`

	// ConfidenceThreshold overrides the action-level threshold specifically for orphan findings.
	// Useful when you want orphan detection to be report-only at lower confidence.
	// +optional
	ConfidenceThreshold *float64 `json:"confidenceThreshold,omitempty"`
}

// PRNamespaceConfig configures detection and cleanup of PR/preview namespaces.
// This does NOT call Git provider APIs; it relies on labels applied by CI/CD pipelines.
type PRNamespaceConfig struct {
	// Enabled turns on PR namespace detection.
	Enabled bool `json:"enabled"`

	// LabelKey identifies a namespace as a PR namespace (e.g., "janitor.io/pr").
	// The label value is the PR number or identifier.
	// +kubebuilder:default="janitor.io/pr"
	LabelKey string `json:"labelKey"`

	// StateAnnotation holds the PR state (e.g., "open", "closed", "merged").
	// Set by your CI/CD pipeline. Defaults to "janitor.io/pr-state".
	// +kubebuilder:default="janitor.io/pr-state"
	// +optional
	StateAnnotation string `json:"stateAnnotation,omitempty"`

	// ClosedTTL is how long to keep a PR namespace after its state is set to "closed" or "merged".
	// Defaults to 1h.
	// +optional
	ClosedTTL *metav1.Duration `json:"closedTTL,omitempty"`
}

// ActionConfig defines what should happen when a finding matches this policy.
type ActionConfig struct {
	// Mode is the action mode.
	// +kubebuilder:default=DryRun
	Mode ActionMode `json:"mode"`

	// ConfidenceThreshold is the minimum confidence score [0.0, 1.0] required to act.
	// Defaults to 0.9 for Delete mode, 0.6 for Report mode, ignored for DryRun.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	// +optional
	ConfidenceThreshold *float64 `json:"confidenceThreshold,omitempty"`
}

// ReportingConfig controls how findings are surfaced.
type ReportingConfig struct {
	// Events controls whether Kubernetes Events are emitted for each finding.
	// +kubebuilder:default=true
	Events bool `json:"events"`

	// Annotations controls whether finding summaries are written as annotations on the resource.
	// +optional
	Annotations bool `json:"annotations,omitempty"`
}

// JanitorPolicySpec defines the desired state of JanitorPolicy.
type JanitorPolicySpec struct {
	// Selector determines which resources this policy applies to.
	Selector ResourceSelector `json:"selector"`

	// TTL configures time-to-live based expiry.
	// +optional
	TTL *TTLConfig `json:"ttl,omitempty"`

	// Protection configures resources that must never be cleaned up.
	// +optional
	Protection *ProtectionConfig `json:"protection,omitempty"`

	// Orphan configures structural and semantic orphan detection.
	// +optional
	Orphan *OrphanConfig `json:"orphan,omitempty"`

	// PRNamespace configures PR/preview namespace cleanup.
	// Only effective when Selector targets Namespace kind.
	// +optional
	PRNamespace *PRNamespaceConfig `json:"prNamespace,omitempty"`

	// Action defines what should happen to resources matching this policy.
	Action ActionConfig `json:"action"`

	// Reporting configures how findings are surfaced.
	// +optional
	Reporting *ReportingConfig `json:"reporting,omitempty"`
}

// PolicyStats captures aggregate counts from the last scan.
type PolicyStats struct {
	TotalScanned  int32 `json:"totalScanned"`
	Active        int32 `json:"active"`
	Ephemeral     int32 `json:"ephemeral"`
	Expired       int32 `json:"expired"`
	Orphaned      int32 `json:"orphaned"`
	Abandoned     int32 `json:"abandoned"`
	Protected     int32 `json:"protected"`
	Blocked       int32 `json:"blocked"`
	Deleted       int32 `json:"deleted"`
	DryRunDeleted int32 `json:"dryRunDeleted"`
	Errors        int32 `json:"errors"`
}

// JanitorPolicyStatus defines the observed state of JanitorPolicy.
type JanitorPolicyStatus struct {
	// ObservedGeneration is the generation of the policy last processed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the current state of the policy.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastScanTime is when the policy last completed a resource scan.
	// +optional
	LastScanTime *metav1.Time `json:"lastScanTime,omitempty"`

	// Stats provides aggregate counts from the last scan.
	// +optional
	Stats *PolicyStats `json:"stats,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=jp,categories=janitor
// +kubebuilder:printcolumn:name="Mode",type="string",JSONPath=".spec.action.mode"
// +kubebuilder:printcolumn:name="Last Scan",type="date",JSONPath=".status.lastScanTime"
// +kubebuilder:printcolumn:name="Scanned",type="integer",JSONPath=".status.stats.totalScanned"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// JanitorPolicy defines a cleanup policy for Kubernetes resources.
// It is cluster-scoped so it can govern resources across namespaces
// and target namespaces themselves.
type JanitorPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   JanitorPolicySpec   `json:"spec,omitempty"`
	Status JanitorPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// JanitorPolicyList contains a list of JanitorPolicy.
type JanitorPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []JanitorPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&JanitorPolicy{}, &JanitorPolicyList{})
}

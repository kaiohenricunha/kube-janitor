// Package resolver defines the Resolver interface and provides the resolver registry.
// Resolvers discover references between Kubernetes resources, supporting the classifier's
// reference-graph-based classification.
//
// Design: each Resolver handles a specific set of resource kinds and discovers
// whether those resources are referenced by other live resources. Resolvers emit
// typed Evidence that flows into Finding.Reasons for auditability.
package resolver

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kaiohenricunha/kube-janitor/internal/domain"
)

// Resolver discovers references from or to a specific Kubernetes resource.
// Multiple resolvers are combined via the Registry to build a complete picture.
//
// What counts as "proven" vs "heuristic":
//   - Proven: a live Pod has a volumeMount referencing this ConfigMap by exact name.
//   - Heuristic: a namespace has zero running workloads (could be transitional).
//
// Resolvers should emit references only when they can produce concrete Evidence.
// A resolver that finds nothing returns an empty slice (not an error).
type Resolver interface {
	// Name returns a stable identifier for this resolver (used in logs and traces).
	Name() string
	// Handles returns true if this resolver can process the given object kind.
	// Only resolvers that handle the object are called by the Registry.
	Handles(obj client.Object) bool
	// Resolve finds all live references to or from obj.
	// Returns empty slice (nil) when no references are found.
	// Returns error only for API failures, not for "found nothing".
	Resolve(ctx context.Context, obj client.Object, reader client.Reader) ([]domain.Reference, error)
}

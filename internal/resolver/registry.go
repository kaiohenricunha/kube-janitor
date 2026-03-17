package resolver

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kaiohenricunha/kube-janitor/internal/domain"
)

// Registry holds all registered resolvers and dispatches calls to those
// that handle the given object kind.
type Registry struct {
	resolvers []Resolver
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a resolver to the registry. Resolvers are called in registration order.
func (r *Registry) Register(resolver Resolver) {
	r.resolvers = append(r.resolvers, resolver)
}

// Len returns the number of registered resolvers.
func (r *Registry) Len() int { return len(r.resolvers) }

// ResolveAll calls every resolver that handles obj and aggregates their results.
// Errors from individual resolvers are accumulated; other resolvers continue.
// Returns combined error if any resolver failed.
func (r *Registry) ResolveAll(ctx context.Context, obj client.Object, reader client.Reader) ([]domain.Reference, error) {
	var (
		allRefs []domain.Reference
		errs    []error
	)

	for _, res := range r.resolvers {
		if !res.Handles(obj) {
			continue
		}
		refs, err := res.Resolve(ctx, obj, reader)
		if err != nil {
			errs = append(errs, fmt.Errorf("resolver %s: %w", res.Name(), err))
			continue
		}
		allRefs = append(allRefs, refs...)
	}

	if len(errs) > 0 {
		return allRefs, fmt.Errorf("resolver errors: %v", errs)
	}
	return allRefs, nil
}

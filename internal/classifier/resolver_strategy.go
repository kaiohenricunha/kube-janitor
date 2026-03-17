package classifier

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kaiohenricunha/kube-janitor/internal/domain"
	"github.com/kaiohenricunha/kube-janitor/internal/resolver"
)

// ResolverStrategy classifies a resource as active or abandoned based on whether
// any registered resolver finds a live reference to it. This is the lowest-priority
// strategy — it runs only when all structural checks (protection, finalizer, TTL,
// ownerRef) have returned ClassUnknown.
//
// The confidence for abandonment is intentionally lower than for structural orphans
// because resolver checks are heuristic, not proof.
type ResolverStrategy struct {
	registry *resolver.Registry
}

// NewResolverStrategy creates a ResolverStrategy backed by the given resolver registry.
func NewResolverStrategy(registry *resolver.Registry) *ResolverStrategy {
	return &ResolverStrategy{registry: registry}
}

// Name implements Strategy.
func (s *ResolverStrategy) Name() string { return "resolver" }

// Classify implements Strategy.
func (s *ResolverStrategy) Classify(
	ctx context.Context,
	obj client.Object,
	reader client.Reader,
) (domain.ResourceClass, domain.Confidence, []domain.Reason, error) {
	refs, err := s.registry.ResolveAll(ctx, obj, reader)
	if err != nil {
		return domain.ClassUnknown, 0, nil, fmt.Errorf("resolving references: %w", err)
	}

	if len(refs) > 0 {
		evidence := make([]domain.Evidence, 0, len(refs))
		for _, ref := range refs {
			evidence = append(evidence, ref.Evidence)
		}
		return domain.ClassActive, domain.ConfidenceHigh, []domain.Reason{{
			Code:     "referenced",
			Message:  fmt.Sprintf("resource has %d live reference(s)", len(refs)),
			Evidence: evidence,
		}}, nil
	}

	// No references found — classify as abandoned with medium confidence.
	// Medium (not high) because "no reference found" is absence of evidence,
	// not evidence of absence. Resolvers may not cover all reference types.
	return domain.ClassAbandoned, domain.ConfidenceMedium, []domain.Reason{{
		Code:    "no-references",
		Message: "no live references to this resource were found by any resolver",
		Evidence: []domain.Evidence{{
			Type:    domain.EvidenceAge,
			Source:  "resolver-registry",
			Details: fmt.Sprintf("checked %d resolver(s), found 0 references", s.registry.Len()),
		}},
	}}, nil
}

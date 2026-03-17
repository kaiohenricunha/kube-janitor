package classifier

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kaiohenricunha/kube-janitor/internal/domain"
)

// OwnerRefStrategy classifies a resource based on its ownerReferences.
//
//   - If a live (existing) owner is found → ClassActive (the owner GC will handle cleanup).
//   - If all owners are dead (owner objects not found) → ClassOrphaned (structural orphan).
//   - If there are no ownerReferences → ClassUnknown (defer to resolver strategy).
//
// This strategy performs API lookups for each ownerReference to verify liveness.
// Uses the APIReader (not the cache) to avoid stale data for safety-critical decisions.
type OwnerRefStrategy struct{}

// NewOwnerRefStrategy creates an OwnerRefStrategy.
func NewOwnerRefStrategy() *OwnerRefStrategy { return &OwnerRefStrategy{} }

// Name implements Strategy.
func (s *OwnerRefStrategy) Name() string { return "owner-ref" }

// Classify implements Strategy.
func (s *OwnerRefStrategy) Classify(
	ctx context.Context,
	obj client.Object,
	reader client.Reader,
) (domain.ResourceClass, domain.Confidence, []domain.Reason, error) {
	owners := obj.GetOwnerReferences()
	if len(owners) == 0 {
		return domain.ClassUnknown, 0, nil, nil
	}

	var liveOwners, deadOwners []string

	for _, owner := range owners {
		gv, err := schema.ParseGroupVersion(owner.APIVersion)
		if err != nil {
			// Malformed ownerRef — treat as unknown owner.
			deadOwners = append(deadOwners, fmt.Sprintf("%s/%s (parse error: %v)", owner.Kind, owner.Name, err))
			continue
		}

		// Use an unstructured Get to avoid needing every type registered in the scheme.
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   gv.Group,
			Version: gv.Version,
			Kind:    owner.Kind,
		})

		err = reader.Get(ctx, types.NamespacedName{
			Namespace: obj.GetNamespace(),
			Name:      owner.Name,
		}, u)

		switch {
		case err == nil && u.GetUID() == owner.UID:
			// Owner exists and UID matches — live owner.
			liveOwners = append(liveOwners, fmt.Sprintf("%s/%s", owner.Kind, owner.Name))
		case err == nil && u.GetUID() != owner.UID:
			// Same name but different UID — the original owner was deleted and a new resource
			// was created with the same name. Treat as dead owner.
			deadOwners = append(deadOwners, fmt.Sprintf("%s/%s (UID mismatch: expected %s, got %s)",
				owner.Kind, owner.Name, owner.UID, u.GetUID()))
		case errors.IsNotFound(err):
			deadOwners = append(deadOwners, fmt.Sprintf("%s/%s (not found)", owner.Kind, owner.Name))
		default:
			return domain.ClassUnknown, 0, nil, fmt.Errorf("checking owner %s/%s: %w", owner.Kind, owner.Name, err)
		}
	}

	if len(liveOwners) > 0 {
		return domain.ClassActive, domain.ConfidenceHigh, []domain.Reason{{
			Code:    "live-owner",
			Message: fmt.Sprintf("resource has %d live owner(s): %v", len(liveOwners), liveOwners),
			Evidence: []domain.Evidence{{
				Type:    domain.EvidenceOwnerRef,
				Source:  "metadata.ownerReferences",
				Details: fmt.Sprintf("live owners: %v", liveOwners),
			}},
		}}, nil
	}

	// All owners are dead — structural orphan.
	return domain.ClassOrphaned, domain.ConfidenceHigh, []domain.Reason{{
		Code:    "dead-owners",
		Message: fmt.Sprintf("resource has ownerReferences but all %d owner(s) are gone: %v", len(deadOwners), deadOwners),
		Evidence: []domain.Evidence{{
			Type:    domain.EvidenceOwnerRef,
			Source:  "metadata.ownerReferences",
			Details: fmt.Sprintf("dead owners: %v", deadOwners),
		}},
	}}, nil
}

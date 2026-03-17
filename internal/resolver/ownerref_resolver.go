package resolver

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

// OwnerRefResolver discovers whether a resource's ownerReferences point to live objects.
// It performs API lookups (bypassing cache) to verify owner liveness by UID.
//
// This resolver is used by the OwnerRefStrategy in the classifier to determine
// whether ownerRef-bearing resources are active or structurally orphaned.
type OwnerRefResolver struct{}

// NewOwnerRefResolver creates an OwnerRefResolver.
func NewOwnerRefResolver() *OwnerRefResolver { return &OwnerRefResolver{} }

// Name implements Resolver.
func (r *OwnerRefResolver) Name() string { return "ownerref" }

// Handles implements Resolver — applies to all resources that might have ownerReferences.
func (r *OwnerRefResolver) Handles(obj client.Object) bool {
	return len(obj.GetOwnerReferences()) > 0
}

// Resolve checks all ownerReferences and returns References for live owners only.
func (r *OwnerRefResolver) Resolve(
	ctx context.Context,
	obj client.Object,
	reader client.Reader,
) ([]domain.Reference, error) {
	owners := obj.GetOwnerReferences()
	var refs []domain.Reference

	for _, owner := range owners {
		gv, err := schema.ParseGroupVersion(owner.APIVersion)
		if err != nil {
			continue // malformed ownerRef — skip
		}

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
		case errors.IsNotFound(err):
			continue // dead owner — not a live reference
		case err != nil:
			return nil, fmt.Errorf("checking owner %s/%s: %w", owner.Kind, owner.Name, err)
		case u.GetUID() != owner.UID:
			continue // UID mismatch — replaced resource, not the original owner
		}

		// Live owner confirmed.
		refs = append(refs, domain.Reference{
			From: domain.ObjectRef{
				Kind:      owner.Kind,
				Namespace: obj.GetNamespace(),
				Name:      owner.Name,
				UID:       owner.UID,
			},
			To: domain.ObjectRef{
				Kind:      obj.GetObjectKind().GroupVersionKind().Kind,
				Namespace: obj.GetNamespace(),
				Name:      obj.GetName(),
				UID:       obj.GetUID(),
			},
			Type: domain.RefTypeOwnerRef,
			Evidence: domain.Evidence{
				Type:    domain.EvidenceOwnerRef,
				Source:  "metadata.ownerReferences",
				Details: fmt.Sprintf("live owner %s/%s (uid=%s) confirmed via API", owner.Kind, owner.Name, owner.UID),
			},
		})
	}

	return refs, nil
}

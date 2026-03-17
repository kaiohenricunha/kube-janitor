// Package classifier implements the resource classification engine.
// It applies a prioritized chain of classification strategies to determine
// the class and confidence level for a given Kubernetes resource.
//
// Strategy chain (priority order — first non-Unknown class wins):
//  1. ProtectionStrategy  — exit early if resource is protected
//  2. FinalizerStrategy   — blocked if deletion-blocking finalizers exist
//  3. TTLStrategy         — expired if ttl annotation has elapsed
//  4. ExpiresAtStrategy   — expired if expires-at timestamp is past
//  5. OwnerRefStrategy    — active if a live ownerReference exists
//  6. ResolverStrategy    — orphaned/active based on resolver reference graph
package classifier

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kaiohenricunha/kube-janitor/internal/domain"
)

// Classifier classifies a Kubernetes object into one of the known ResourceClasses.
type Classifier interface {
	Classify(ctx context.Context, obj client.Object) (domain.Finding, error)
}

// DefaultClassifier applies a prioritized chain of strategies.
// The first strategy to return a non-Unknown class wins.
type DefaultClassifier struct {
	strategies []Strategy
	reader     client.Reader
	log        logr.Logger
}

// New creates a DefaultClassifier with the given strategies applied in priority order.
// Callers are responsible for providing strategies in the correct order (see package doc).
func New(reader client.Reader, log logr.Logger, strategies ...Strategy) *DefaultClassifier {
	return &DefaultClassifier{
		strategies: strategies,
		reader:     reader,
		log:        log,
	}
}

// Classify applies each strategy in order and returns the first non-Unknown finding.
// If no strategy produces a classification, ClassUnknown is returned.
func (c *DefaultClassifier) Classify(ctx context.Context, obj client.Object) (domain.Finding, error) {
	log := c.log.WithValues(
		"kind", obj.GetObjectKind().GroupVersionKind().Kind,
		"namespace", obj.GetNamespace(),
		"name", obj.GetName(),
	)

	for _, s := range c.strategies {
		class, confidence, reasons, err := s.Classify(ctx, obj, c.reader)
		if err != nil {
			return domain.Finding{}, fmt.Errorf("strategy %s: %w", s.Name(), err)
		}
		if class == domain.ClassUnknown {
			log.V(2).Info("strategy did not match", "strategy", s.Name())
			continue
		}

		log.V(1).Info("classified",
			"strategy", s.Name(),
			"class", class,
			"confidence", confidence,
		)
		return domain.Finding{
			ObjectRef:    objectRefFrom(obj),
			Class:        class,
			Confidence:   confidence,
			Reasons:      reasons,
			ClassifiedAt: time.Now().UTC(),
		}, nil
	}

	return domain.Finding{
		ObjectRef:    objectRefFrom(obj),
		Class:        domain.ClassUnknown,
		Confidence:   domain.ConfidenceLow,
		ClassifiedAt: time.Now().UTC(),
		Reasons: []domain.Reason{{
			Code:    "no-strategy-matched",
			Message: "no classification strategy could determine the class of this resource",
		}},
	}, nil
}

func objectRefFrom(obj client.Object) domain.ObjectRef {
	gvk := obj.GetObjectKind().GroupVersionKind()
	return domain.ObjectRef{
		Group:     gvk.Group,
		Version:   gvk.Version,
		Kind:      gvk.Kind,
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
		UID:       obj.GetUID(),
	}
}

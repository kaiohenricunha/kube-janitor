package classifier

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kaiohenricunha/kube-janitor/internal/domain"
)

// ProtectionStrategy classifies a resource as Protected if it carries the
// protection annotation, any of the protection labels, or lives in a
// protected namespace. This strategy has the highest priority in the chain.
type ProtectionStrategy struct {
	// AnnotationKey is the annotation that marks a resource as protected.
	// A value of "true" (case-insensitive) triggers protection.
	AnnotationKey string
	// Labels are key=value pairs; any resource carrying all of them is protected.
	Labels map[string]string
	// Namespaces is the set of namespace names that are always protected.
	Namespaces map[string]struct{}
}

// NewProtectionStrategy creates a ProtectionStrategy with standard defaults.
// annotationKey defaults to "janitor.io/protected".
// protectedNamespaces should include at minimum: kube-system, kube-public, default, kube-node-lease.
func NewProtectionStrategy(annotationKey string, protectedNamespaces ...string) *ProtectionStrategy {
	if annotationKey == "" {
		annotationKey = "janitor.io/protected"
	}
	ns := make(map[string]struct{}, len(protectedNamespaces))
	for _, n := range protectedNamespaces {
		ns[n] = struct{}{}
	}
	return &ProtectionStrategy{
		AnnotationKey: annotationKey,
		Namespaces:    ns,
	}
}

// Name implements Strategy.
func (s *ProtectionStrategy) Name() string { return "protection" }

// Classify implements Strategy.
func (s *ProtectionStrategy) Classify(
	_ context.Context,
	obj client.Object,
	_ client.Reader,
) (domain.ResourceClass, domain.Confidence, []domain.Reason, error) {
	// Check protected namespace first (highest priority).
	if _, ok := s.Namespaces[obj.GetNamespace()]; ok {
		return domain.ClassProtected, domain.ConfidenceHigh, []domain.Reason{{
			Code:    "protected-namespace",
			Message: fmt.Sprintf("resource is in a protected namespace %q", obj.GetNamespace()),
			Evidence: []domain.Evidence{{
				Type:    domain.EvidenceAnnotation,
				Source:  "controller-config",
				Details: fmt.Sprintf("namespace %q is in the protected namespace list", obj.GetNamespace()),
			}},
		}}, nil
	}

	// For cluster-scoped resources (e.g., Namespace), check if the resource
	// name itself is in the protected namespace list.
	if obj.GetNamespace() == "" {
		if _, ok := s.Namespaces[obj.GetName()]; ok {
			return domain.ClassProtected, domain.ConfidenceHigh, []domain.Reason{{
				Code:    "protected-namespace",
				Message: fmt.Sprintf("namespace %q is in the protected namespace list", obj.GetName()),
				Evidence: []domain.Evidence{{
					Type:    domain.EvidenceAnnotation,
					Source:  "controller-config",
					Details: fmt.Sprintf("namespace %q is in the protected namespace list", obj.GetName()),
				}},
			}}, nil
		}
	}

	// Check protection annotation.
	annotations := obj.GetAnnotations()
	if annotations[s.AnnotationKey] == "true" {
		return domain.ClassProtected, domain.ConfidenceHigh, []domain.Reason{{
			Code:    "protection-annotation",
			Message: fmt.Sprintf("resource has protection annotation %s=true", s.AnnotationKey),
			Evidence: []domain.Evidence{{
				Type:    domain.EvidenceAnnotation,
				Source:  fmt.Sprintf("annotation:%s", s.AnnotationKey),
				Details: fmt.Sprintf("annotation %s is set to %q", s.AnnotationKey, annotations[s.AnnotationKey]),
			}},
		}}, nil
	}

	// Check protection labels.
	if len(s.Labels) > 0 {
		labels := obj.GetLabels()
		allMatch := true
		for k, v := range s.Labels {
			if labels[k] != v {
				allMatch = false
				break
			}
		}
		if allMatch {
			return domain.ClassProtected, domain.ConfidenceHigh, []domain.Reason{{
				Code:    "protection-label",
				Message: "resource has protection labels",
				Evidence: []domain.Evidence{{
					Type:    domain.EvidenceLabelSelector,
					Source:  "controller-config",
					Details: fmt.Sprintf("resource matches protection labels %v", s.Labels),
				}},
			}}, nil
		}
	}

	return domain.ClassUnknown, 0, nil, nil
}

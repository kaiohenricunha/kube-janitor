package classifier

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kaiohenricunha/kube-janitor/internal/domain"
)

// ExpiresAtStrategy classifies a resource as Expired when an absolute expiry
// timestamp annotation (RFC3339) is present and has elapsed.
type ExpiresAtStrategy struct {
	// AnnotationKey is the annotation holding the RFC3339 timestamp.
	AnnotationKey string
	// now is injectable for testing.
	now func() time.Time
}

// NewExpiresAtStrategy creates an ExpiresAtStrategy.
func NewExpiresAtStrategy(annotationKey string) *ExpiresAtStrategy {
	return &ExpiresAtStrategy{
		AnnotationKey: annotationKey,
		now:           time.Now,
	}
}

// Name implements Strategy.
func (s *ExpiresAtStrategy) Name() string { return "expires-at" }

// Classify implements Strategy.
func (s *ExpiresAtStrategy) Classify(
	_ context.Context,
	obj client.Object,
	_ client.Reader,
) (domain.ResourceClass, domain.Confidence, []domain.Reason, error) {
	v, ok := obj.GetAnnotations()[s.AnnotationKey]
	if !ok || v == "" {
		return domain.ClassUnknown, 0, nil, nil
	}

	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return domain.ClassUnknown, 0, nil, fmt.Errorf("annotation %s=%q is not a valid RFC3339 timestamp: %w", s.AnnotationKey, v, err)
	}

	now := s.now().UTC()
	if now.Before(t) {
		return domain.ClassUnknown, 0, nil, nil
	}

	overdue := now.Sub(t)
	return domain.ClassExpired, domain.ConfidenceHigh, []domain.Reason{{
		Code:    "expires-at-elapsed",
		Message: fmt.Sprintf("resource expiry timestamp %s has elapsed (overdue by: %s)", t.Format(time.RFC3339), overdue.Round(time.Second)),
		Evidence: []domain.Evidence{{
			Type:    domain.EvidenceExpiresAt,
			Source:  fmt.Sprintf("annotation:%s", s.AnnotationKey),
			Details: fmt.Sprintf("expires-at=%s, now=%s", t.Format(time.RFC3339), now.Format(time.RFC3339)),
		}},
	}}, nil
}

package classifier

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kaiohenricunha/kube-janitor/internal/domain"
)

// TTLStrategy classifies a resource as Expired when its TTL annotation has elapsed.
// The TTL is measured from the resource's creation timestamp.
type TTLStrategy struct {
	// AnnotationKey is the annotation that holds the TTL duration string.
	// E.g., "janitor.io/ttl" with value "72h".
	AnnotationKey string
	// DefaultTTL is applied to all resources matched by the policy if they don't
	// carry an explicit TTL annotation. Zero means no default TTL.
	DefaultTTL time.Duration
	// now is injectable for testing.
	now func() time.Time
}

// NewTTLStrategy creates a TTLStrategy.
func NewTTLStrategy(annotationKey string, defaultTTL time.Duration) *TTLStrategy {
	return &TTLStrategy{
		AnnotationKey: annotationKey,
		DefaultTTL:    defaultTTL,
		now:           time.Now,
	}
}

// Name implements Strategy.
func (s *TTLStrategy) Name() string { return "ttl" }

// Classify implements Strategy.
func (s *TTLStrategy) Classify(
	_ context.Context,
	obj client.Object,
	_ client.Reader,
) (domain.ResourceClass, domain.Confidence, []domain.Reason, error) {
	ttl, source, err := s.resolveTTL(obj)
	if err != nil {
		return domain.ClassUnknown, 0, nil, fmt.Errorf("parsing TTL: %w", err)
	}
	if ttl == 0 {
		return domain.ClassUnknown, 0, nil, nil
	}

	age := s.now().UTC().Sub(obj.GetCreationTimestamp().UTC())
	if age <= ttl {
		return domain.ClassUnknown, 0, nil, nil
	}

	overdue := age - ttl
	return domain.ClassExpired, domain.ConfidenceHigh, []domain.Reason{{
		Code:    "ttl-expired",
		Message: fmt.Sprintf("resource TTL of %s has elapsed (age: %s, overdue by: %s)", ttl, age.Round(time.Second), overdue.Round(time.Second)),
		Evidence: []domain.Evidence{{
			Type:    domain.EvidenceTTL,
			Source:  source,
			Details: fmt.Sprintf("TTL=%s, created=%s, age=%s", ttl, obj.GetCreationTimestamp().UTC().Format(time.RFC3339), age.Round(time.Second)),
		}},
	}}, nil
}

func (s *TTLStrategy) resolveTTL(obj client.Object) (time.Duration, string, error) {
	// Per-resource annotation takes precedence over policy default.
	if v, ok := obj.GetAnnotations()[s.AnnotationKey]; ok {
		d, err := time.ParseDuration(v)
		if err != nil {
			return 0, "", fmt.Errorf("annotation %s=%q is not a valid duration: %w", s.AnnotationKey, v, err)
		}
		return d, fmt.Sprintf("annotation:%s", s.AnnotationKey), nil
	}
	if s.DefaultTTL > 0 {
		return s.DefaultTTL, "policy-default-ttl", nil
	}
	return 0, "", nil
}

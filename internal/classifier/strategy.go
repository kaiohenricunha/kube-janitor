package classifier

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kaiohenricunha/kube-janitor/internal/domain"
)

// Strategy is a single classification rule.
// Strategies are applied in priority order by DefaultClassifier.
// A strategy that cannot determine the class returns ClassUnknown.
type Strategy interface {
	// Name returns a stable identifier for this strategy (used in logs and traces).
	Name() string
	// Classify attempts to classify obj. Returns ClassUnknown if this strategy
	// cannot make a determination. Never returns an error for "didn't match" —
	// only for actual failures (API errors, parse errors).
	Classify(ctx context.Context, obj client.Object, reader client.Reader) (
		class domain.ResourceClass,
		confidence domain.Confidence,
		reasons []domain.Reason,
		err error,
	)
}

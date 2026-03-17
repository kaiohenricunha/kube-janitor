package classifier

import (
	"context"
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kaiohenricunha/kube-janitor/internal/domain"
)

// janitorFinalizer is the finalizer kube-janitor manages itself.
// All other finalizers are considered "foreign" and block deletion.
const janitorFinalizer = "janitor.io/cleanup"

// FinalizerStrategy classifies a resource as Blocked if it carries
// any deletion-blocking finalizers other than kube-janitor's own.
// kube-janitor never forcibly removes foreign finalizers.
type FinalizerStrategy struct{}

// NewFinalizerStrategy creates a FinalizerStrategy.
func NewFinalizerStrategy() *FinalizerStrategy { return &FinalizerStrategy{} }

// Name implements Strategy.
func (s *FinalizerStrategy) Name() string { return "finalizer" }

// Classify implements Strategy.
func (s *FinalizerStrategy) Classify(
	_ context.Context,
	obj client.Object,
	_ client.Reader,
) (domain.ResourceClass, domain.Confidence, []domain.Reason, error) {
	finalizers := obj.GetFinalizers()
	if len(finalizers) == 0 {
		return domain.ClassUnknown, 0, nil, nil
	}

	// Only block on foreign finalizers — not our own.
	var foreign []string
	for _, f := range finalizers {
		if f != janitorFinalizer {
			foreign = append(foreign, f)
		}
	}

	if len(foreign) == 0 {
		return domain.ClassUnknown, 0, nil, nil
	}

	return domain.ClassBlocked, domain.ConfidenceHigh, []domain.Reason{{
		Code:    "foreign-finalizers",
		Message: fmt.Sprintf("resource has %d foreign finalizer(s) blocking deletion", len(foreign)),
		Evidence: []domain.Evidence{{
			Type:    domain.EvidenceFinalizer,
			Source:  "metadata.finalizers",
			Details: fmt.Sprintf("finalizers: [%s]", strings.Join(foreign, ", ")),
		}},
	}}, nil
}

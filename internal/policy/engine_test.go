package policy_test

// TDD: Policy engine tests define the expected behavior for all decision paths.
// Written before (or alongside) the implementation to spec the contract.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	janitorv1alpha1 "github.com/kaiohenricunha/kube-janitor/api/v1alpha1"
	"github.com/kaiohenricunha/kube-janitor/internal/domain"
	"github.com/kaiohenricunha/kube-janitor/internal/policy"
)

// annotationsMap is a test helper that satisfies policy.AnnotationReader.
type annotationsMap map[string]string

func (a annotationsMap) GetAnnotations() map[string]string { return a }

func TestEngine_ProtectedAlwaysNone(t *testing.T) {
	// Safety spec: Protected class must never produce a Delete action.
	engine := policy.NewDefaultEngine(false, log.FromContext(context.Background()))

	finding := domain.Finding{
		Class:      domain.ClassProtected,
		Confidence: 1.0,
	}
	deletePolicy := makeDeletePolicy("delete-all", 0.0)

	decision, err := engine.Evaluate(context.Background(), annotationsMap{}, finding, []janitorv1alpha1.JanitorPolicy{deletePolicy})
	require.NoError(t, err)

	assert.Equal(t, domain.ActionNone, decision.Action,
		"protected resources must never receive a delete action")
}

func TestEngine_UnknownAlwaysNone(t *testing.T) {
	// Safety spec: Unknown classification must never produce any action.
	engine := policy.NewDefaultEngine(false, log.FromContext(context.Background()))

	finding := domain.Finding{
		Class:      domain.ClassUnknown,
		Confidence: 1.0,
	}

	decision, err := engine.Evaluate(context.Background(), annotationsMap{}, finding, nil)
	require.NoError(t, err)

	assert.Equal(t, domain.ActionNone, decision.Action)
}

func TestEngine_GlobalDryRunOverridesDelete(t *testing.T) {
	// Safety spec: GlobalDryRun=true must prevent actual deletion even when
	// the policy mode is Delete and confidence is at max.
	engine := policy.NewDefaultEngine(true, log.FromContext(context.Background())) // dry-run enabled

	finding := domain.Finding{
		ObjectRef:  domain.ObjectRef{Kind: "ConfigMap"},
		Class:      domain.ClassExpired,
		Confidence: 1.0,
	}
	deletePolicy := makeDeletePolicy("expire-and-delete", 0.9)

	decision, err := engine.Evaluate(context.Background(), annotationsMap{}, finding, []janitorv1alpha1.JanitorPolicy{deletePolicy})
	require.NoError(t, err)

	assert.Equal(t, domain.ActionDelete, decision.Action, "action should be delete")
	assert.True(t, decision.DryRun, "global dry-run must force DryRun=true even for Delete policies")
}

func TestEngine_DryRunModeReturnsSimulatedDelete(t *testing.T) {
	// Spec: DryRun policy mode should report what would happen, not delete.
	engine := policy.NewDefaultEngine(false, log.FromContext(context.Background()))

	finding := domain.Finding{
		ObjectRef:  domain.ObjectRef{Kind: "ConfigMap"},
		Class:      domain.ClassExpired,
		Confidence: 0.95,
	}
	dryRunPolicy := makeDryRunPolicy("dryrun-policy", 0.9)

	decision, err := engine.Evaluate(context.Background(), annotationsMap{}, finding, []janitorv1alpha1.JanitorPolicy{dryRunPolicy})
	require.NoError(t, err)

	assert.Equal(t, domain.ActionDelete, decision.Action)
	assert.True(t, decision.DryRun, "DryRun policy must set DryRun=true")
}

func TestEngine_BelowConfidenceThresholdFallsToReport(t *testing.T) {
	// Spec: a resource below the delete threshold should be reported, not deleted.
	engine := policy.NewDefaultEngine(false, log.FromContext(context.Background()))

	finding := domain.Finding{
		ObjectRef:  domain.ObjectRef{Kind: "ConfigMap"},
		Class:      domain.ClassExpired,
		Confidence: 0.7, // below 0.9 threshold
	}
	deletePolicy := makeDeletePolicy("delete-policy", 0.9)

	decision, err := engine.Evaluate(context.Background(), annotationsMap{}, finding, []janitorv1alpha1.JanitorPolicy{deletePolicy})
	require.NoError(t, err)

	assert.Equal(t, domain.ActionReport, decision.Action,
		"below delete threshold should fall back to report")
}

func TestEngine_AnnotationSkipOverridesPolicy(t *testing.T) {
	// Spec: janitor.io/action=skip annotation must override any policy.
	engine := policy.NewDefaultEngine(false, log.FromContext(context.Background()))

	finding := domain.Finding{
		ObjectRef:  domain.ObjectRef{Kind: "ConfigMap"},
		Class:      domain.ClassExpired,
		Confidence: 1.0,
	}
	deletePolicy := makeDeletePolicy("delete-all", 0.0)
	annotations := annotationsMap{"janitor.io/action": "skip"}

	decision, err := engine.Evaluate(context.Background(), annotations, finding, []janitorv1alpha1.JanitorPolicy{deletePolicy})
	require.NoError(t, err)

	assert.Equal(t, domain.ActionNone, decision.Action,
		"janitor.io/action=skip annotation must prevent any action")
}

func TestEngine_BlockedClassGetsReport(t *testing.T) {
	// Spec: blocked resources (has finalizers) should be reported but never deleted.
	engine := policy.NewDefaultEngine(false, log.FromContext(context.Background()))

	finding := domain.Finding{
		ObjectRef:  domain.ObjectRef{Kind: "ConfigMap"},
		Class:      domain.ClassBlocked,
		Confidence: 1.0,
	}
	deletePolicy := makeDeletePolicy("delete-all", 0.0)

	decision, err := engine.Evaluate(context.Background(), annotationsMap{}, finding, []janitorv1alpha1.JanitorPolicy{deletePolicy})
	require.NoError(t, err)

	assert.NotEqual(t, domain.ActionDelete, decision.Action,
		"blocked resources must not receive a delete action")
}

func TestEngine_NoPolicyDefaultsToReport(t *testing.T) {
	// Spec: expired resource with high confidence but no matching policy should
	// still be reported (safe default — don't delete, do surface).
	engine := policy.NewDefaultEngine(false, log.FromContext(context.Background()))

	finding := domain.Finding{
		Class:      domain.ClassExpired,
		Confidence: 0.95,
	}

	decision, err := engine.Evaluate(context.Background(), annotationsMap{}, finding, nil)
	require.NoError(t, err)

	assert.Equal(t, domain.ActionReport, decision.Action)
}

// Helpers

func makeDeletePolicy(name string, threshold float64) janitorv1alpha1.JanitorPolicy {
	return janitorv1alpha1.JanitorPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: janitorv1alpha1.JanitorPolicySpec{
			Selector: janitorv1alpha1.ResourceSelector{
				Kinds: []janitorv1alpha1.ResourceKind{{Group: "", Version: "v1", Kind: "ConfigMap"}},
			},
			Action: janitorv1alpha1.ActionConfig{
				Mode:                janitorv1alpha1.ActionModeDelete,
				ConfidenceThreshold: &threshold,
			},
		},
	}
}

func makeDryRunPolicy(name string, threshold float64) janitorv1alpha1.JanitorPolicy {
	return janitorv1alpha1.JanitorPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: janitorv1alpha1.JanitorPolicySpec{
			Selector: janitorv1alpha1.ResourceSelector{
				Kinds: []janitorv1alpha1.ResourceKind{{Group: "", Version: "v1", Kind: "ConfigMap"}},
			},
			Action: janitorv1alpha1.ActionConfig{
				Mode:                janitorv1alpha1.ActionModeDryRun,
				ConfidenceThreshold: &threshold,
			},
		},
	}
}

var _ = time.Now // suppress unused import warning in test file

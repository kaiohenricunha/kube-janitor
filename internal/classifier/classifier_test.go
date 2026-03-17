package classifier_test

// TDD: These tests define the expected behavior of the classifier BEFORE or
// alongside the implementation. Each test case maps directly to a spec example
// in docs/specs/classifier-spec.md.
//
// Test structure per spec example:
//   - Arrange: build a fake Kubernetes object
//   - Act: call Classify()
//   - Assert: check class, confidence, and reason codes

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/kaiohenricunha/kube-janitor/internal/classifier"
	"github.com/kaiohenricunha/kube-janitor/internal/domain"
	"github.com/kaiohenricunha/kube-janitor/internal/resolver"
)

func TestClassifier_ProtectedByAnnotation(t *testing.T) {
	// Spec: a resource with annotation "janitor.io/protected=true" must be classified
	// as Protected with high confidence, regardless of TTL or orphan status.
	// Uses "test-ns" (not in protected namespace list) so namespace check doesn't fire first.
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "protected-cm",
			Namespace: "test-ns",
			Annotations: map[string]string{
				"janitor.io/protected": "true",
			},
		},
	}

	c := newTestClassifier(t, obj)
	finding, err := c.Classify(context.Background(), obj)
	require.NoError(t, err)

	assert.Equal(t, domain.ClassProtected, finding.Class)
	assert.GreaterOrEqual(t, float64(finding.Confidence), 0.9)
	require.NotEmpty(t, finding.Reasons)
	assert.Equal(t, "protection-annotation", finding.Reasons[0].Code)
}

func TestClassifier_ProtectedByNamespace(t *testing.T) {
	// Spec: a resource in "kube-system" must always be Protected regardless of annotations.
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "coredns-config",
			Namespace: "kube-system",
		},
	}

	c := newTestClassifier(t, obj)
	finding, err := c.Classify(context.Background(), obj)
	require.NoError(t, err)

	assert.Equal(t, domain.ClassProtected, finding.Class)
	assert.Equal(t, "protected-namespace", finding.Reasons[0].Code)
}

func TestClassifier_ExpiredByTTL(t *testing.T) {
	// Spec: a resource created 25h ago with janitor.io/ttl=24h must be Expired.
	createdAt := time.Now().Add(-25 * time.Hour)
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "old-cm",
			Namespace: "test-ns", // not in protected namespace list
			Annotations: map[string]string{
				"janitor.io/ttl": "24h",
			},
			CreationTimestamp: metav1.NewTime(createdAt),
		},
	}

	c := newTestClassifier(t, obj)
	finding, err := c.Classify(context.Background(), obj)
	require.NoError(t, err)

	assert.Equal(t, domain.ClassExpired, finding.Class)
	assert.GreaterOrEqual(t, float64(finding.Confidence), 0.9)
	assert.Equal(t, "ttl-expired", finding.Reasons[0].Code)
}

func TestClassifier_NotExpiredByTTL(t *testing.T) {
	// Spec: a resource created 1h ago with janitor.io/ttl=24h must NOT be Expired.
	createdAt := time.Now().Add(-1 * time.Hour)
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "new-cm",
			Namespace: "test-ns",
			Annotations: map[string]string{
				"janitor.io/ttl": "24h",
			},
			CreationTimestamp: metav1.NewTime(createdAt),
		},
	}

	c := newTestClassifier(t, obj)
	finding, err := c.Classify(context.Background(), obj)
	require.NoError(t, err)

	// Not expired — should fall through to resolver/abandoned
	assert.NotEqual(t, domain.ClassExpired, finding.Class)
}

func TestClassifier_ExpiredByExpiresAt(t *testing.T) {
	// Spec: a resource with an expires-at timestamp in the past must be Expired.
	past := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "deadline-cm",
			Namespace: "test-ns",
			Annotations: map[string]string{
				"janitor.io/expires-at": past,
			},
		},
	}

	c := newTestClassifier(t, obj)
	finding, err := c.Classify(context.Background(), obj)
	require.NoError(t, err)

	assert.Equal(t, domain.ClassExpired, finding.Class)
	assert.Equal(t, "expires-at-elapsed", finding.Reasons[0].Code)
}

func TestClassifier_FinalizerBlocked(t *testing.T) {
	// Spec: a resource with a foreign finalizer must be classified as Blocked,
	// even if it is expired (finalizer takes priority in the chain).
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "finalizer-cm",
			Namespace: "test-ns",
			Finalizers: []string{"some-other-controller/cleanup"},
			Annotations: map[string]string{
				"janitor.io/ttl": "1h",
			},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-48 * time.Hour)),
		},
	}

	c := newTestClassifier(t, obj)
	finding, err := c.Classify(context.Background(), obj)
	require.NoError(t, err)

	assert.Equal(t, domain.ClassBlocked, finding.Class)
	assert.Equal(t, "foreign-finalizers", finding.Reasons[0].Code)
}

func TestClassifier_JanitorFinalizerNotBlocked(t *testing.T) {
	// Spec: a resource with ONLY the janitor.io/cleanup finalizer must NOT be Blocked.
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "janitor-finalized-cm",
			Namespace:  "test-ns",
			Finalizers: []string{"janitor.io/cleanup"},
			Annotations: map[string]string{
				"janitor.io/ttl": "1h",
			},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-48 * time.Hour)),
		},
	}

	c := newTestClassifier(t, obj)
	finding, err := c.Classify(context.Background(), obj)
	require.NoError(t, err)

	// Should be expired (TTL elapsed), not blocked.
	assert.NotEqual(t, domain.ClassBlocked, finding.Class)
	assert.Equal(t, domain.ClassExpired, finding.Class)
}

func TestClassifier_ProtectionBeforeExpiry(t *testing.T) {
	// Safety spec: protection annotation must override TTL expiry.
	// A protected resource must never be classified as Expired.
	createdAt := time.Now().Add(-72 * time.Hour)
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "protected-and-old",
			Namespace: "test-ns",
			Annotations: map[string]string{
				"janitor.io/protected": "true",
				"janitor.io/ttl":       "24h",
			},
			CreationTimestamp: metav1.NewTime(createdAt),
		},
	}

	c := newTestClassifier(t, obj)
	finding, err := c.Classify(context.Background(), obj)
	require.NoError(t, err)

	// Protection must win over expiry.
	assert.Equal(t, domain.ClassProtected, finding.Class,
		"protected resource must not be classified as expired regardless of TTL")
}

func TestClassifier_InvalidTTLAnnotation(t *testing.T) {
	// Edge case: invalid TTL annotation value must return an error, not silently ignore.
	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bad-ttl-cm",
			Namespace: "test-ns",
			Annotations: map[string]string{
				"janitor.io/ttl": "not-a-duration",
			},
		},
	}

	c := newTestClassifier(t, obj)
	_, err := c.Classify(context.Background(), obj)
	assert.Error(t, err, "invalid TTL annotation must return an error")
}

// newTestClassifier creates a classifier with standard strategies for testing.
func newTestClassifier(t *testing.T, objs ...runtime.Object) *classifier.DefaultClassifier {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		Build()

	reg := resolver.NewRegistry()

	return classifier.New(
		fakeClient,
		log.FromContext(context.Background()),
		classifier.NewProtectionStrategy("janitor.io/protected",
			"kube-system", "kube-public", "default", "kube-node-lease"),
		classifier.NewFinalizerStrategy(),
		classifier.NewTTLStrategy("janitor.io/ttl", 0),
		classifier.NewExpiresAtStrategy("janitor.io/expires-at"),
		classifier.NewOwnerRefStrategy(),
		classifier.NewResolverStrategy(reg),
	)
}

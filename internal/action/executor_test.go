package action_test

// TDD: Executor tests focus on safety-critical behavior:
// dry-run must never call client.Delete(), pre-flight checks must block unsafe deletions.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/kaiohenricunha/kube-janitor/internal/action"
	"github.com/kaiohenricunha/kube-janitor/internal/domain"
	"github.com/kaiohenricunha/kube-janitor/pkg/metrics"
)

func TestExecutor_DryRunDoesNotDelete(t *testing.T) {
	// Safety spec: DryRun=true must never call client.Delete().
	// Even with ActionDelete, the resource must remain after Execute().
	metrics.Register()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "target-cm",
			Namespace: "default",
			UID:       types.UID("test-uid-1"),
		},
	}

	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(cm).Build()

	exec := action.NewDefaultExecutor(
		fakeClient,
		fakeClient, // use same client as apiReader in tests
		log.FromContext(context.Background()),
		metrics.DefaultRecorder(),
		[]string{"kube-system", "kube-public", "kube-node-lease"},
	)

	finding := domain.Finding{
		ObjectRef: domain.ObjectRef{
			Kind:      "ConfigMap",
			Namespace: "default",
			Name:      "target-cm",
			UID:       "test-uid-1",
		},
		Class:      domain.ClassExpired,
		Confidence: 0.95,
	}
	decision := domain.ActionDecision{
		Action: domain.ActionDelete,
		DryRun: true, // dry-run!
		Reason: "ttl expired",
	}

	err := exec.Execute(context.Background(), cm, finding, decision)
	require.NoError(t, err)

	// Verify the ConfigMap still exists.
	var verify corev1.ConfigMap
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "target-cm", Namespace: "default"}, &verify)
	require.NoError(t, err, "resource must still exist after dry-run delete")
}

func TestExecutor_ProtectedNamespaceBlocksDeletion(t *testing.T) {
	// Safety spec: resources in protected namespaces must never be deleted.
	metrics.Register()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "system-config",
			Namespace: "kube-system",
			UID:       types.UID("sys-uid"),
		},
	}

	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(cm).Build()

	exec := action.NewDefaultExecutor(
		fakeClient,
		fakeClient,
		log.FromContext(context.Background()),
		metrics.DefaultRecorder(),
		[]string{"kube-system", "kube-public", "default"},
	)

	finding := domain.Finding{
		ObjectRef: domain.ObjectRef{
			Kind:      "ConfigMap",
			Namespace: "kube-system",
			Name:      "system-config",
			UID:       "sys-uid",
		},
		Class:      domain.ClassExpired,
		Confidence: 1.0,
	}
	decision := domain.ActionDecision{
		Action: domain.ActionDelete,
		DryRun: false, // not dry-run — would normally delete
		Reason: "ttl expired",
	}

	err := exec.Execute(context.Background(), cm, finding, decision)
	assert.Error(t, err, "deletion in a protected namespace must return an error")
	assert.Contains(t, err.Error(), "SAFETY")

	// ConfigMap must still exist.
	var verify corev1.ConfigMap
	err = fakeClient.Get(context.Background(), types.NamespacedName{Name: "system-config", Namespace: "kube-system"}, &verify)
	require.NoError(t, err, "resource must still exist after blocked deletion attempt")
}

func TestExecutor_ActionNoneDoesNothing(t *testing.T) {
	// Spec: ActionNone must be a complete no-op.
	metrics.Register()

	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm", Namespace: "test"}}
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(cm).Build()

	exec := action.NewDefaultExecutor(fakeClient, fakeClient,
		log.FromContext(context.Background()), metrics.DefaultRecorder(), nil)

	err := exec.Execute(context.Background(), cm, domain.Finding{}, domain.ActionDecision{Action: domain.ActionNone})
	require.NoError(t, err)
}

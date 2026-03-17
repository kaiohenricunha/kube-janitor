package resolver

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kaiohenricunha/kube-janitor/internal/domain"
)

// ConfigMapResolver discovers whether a ConfigMap is referenced by any live Pod
// in the same namespace via volumeMounts or envFrom.
//
// Scope: proves Pod->ConfigMap references only. Does not cover Deployments or
// StatefulSets directly — those references go through Pod templates, which
// are covered when the Pods themselves exist.
//
// Confidence: High for direct Pod references (proven by API lookup).
type ConfigMapResolver struct{}

// NewConfigMapResolver creates a ConfigMapResolver.
func NewConfigMapResolver() *ConfigMapResolver { return &ConfigMapResolver{} }

// Name implements Resolver.
func (r *ConfigMapResolver) Name() string { return "configmap" }

// Handles implements Resolver — only handles ConfigMap resources.
func (r *ConfigMapResolver) Handles(obj client.Object) bool {
	return obj.GetObjectKind().GroupVersionKind().Kind == "ConfigMap"
}

// Resolve finds all Pods in the same namespace that reference this ConfigMap.
func (r *ConfigMapResolver) Resolve(
	ctx context.Context,
	obj client.Object,
	reader client.Reader,
) ([]domain.Reference, error) {
	cmName := obj.GetName()
	ns := obj.GetNamespace()

	podList := &corev1.PodList{}
	if err := reader.List(ctx, podList, client.InNamespace(ns)); err != nil {
		return nil, fmt.Errorf("listing pods in namespace %s: %w", ns, err)
	}

	cmRef := domain.ObjectRef{
		Group:     "",
		Version:   "v1",
		Kind:      "ConfigMap",
		Namespace: ns,
		Name:      cmName,
		UID:       obj.GetUID(),
	}

	var refs []domain.Reference
	for i := range podList.Items {
		pod := &podList.Items[i]
		if ref, ok := r.podReferencesConfigMap(pod, cmRef); ok {
			refs = append(refs, ref)
		}
	}

	return refs, nil
}

func (r *ConfigMapResolver) podReferencesConfigMap(pod *corev1.Pod, cm domain.ObjectRef) (domain.Reference, bool) {
	podRef := domain.ObjectRef{
		Group:     "",
		Version:   "v1",
		Kind:      "Pod",
		Namespace: pod.Namespace,
		Name:      pod.Name,
		UID:       pod.GetUID(),
	}

	// Check volumes.
	for _, vol := range pod.Spec.Volumes {
		if vol.ConfigMap != nil && vol.ConfigMap.Name == cm.Name {
			return domain.Reference{
				From: podRef,
				To:   cm,
				Type: domain.RefTypeVolumeMount,
				Evidence: domain.Evidence{
					Type:   domain.EvidenceVolumeMount,
					Source: fmt.Sprintf("Pod/%s/%s", pod.Namespace, pod.Name),
					Details: fmt.Sprintf("pod volume %q references ConfigMap %q",
						vol.Name, cm.Name),
				},
			}, true
		}
	}

	// Check envFrom on all containers (including init containers).
	allContainers := append(pod.Spec.InitContainers, pod.Spec.Containers...)
	for _, c := range allContainers {
		for _, envFrom := range c.EnvFrom {
			if envFrom.ConfigMapRef != nil && envFrom.ConfigMapRef.Name == cm.Name {
				return domain.Reference{
					From: podRef,
					To:   cm,
					Type: domain.RefTypeEnvFrom,
					Evidence: domain.Evidence{
						Type:   domain.EvidenceVolumeMount,
						Source: fmt.Sprintf("Pod/%s/%s container=%s", pod.Namespace, pod.Name, c.Name),
						Details: fmt.Sprintf("container %q envFrom references ConfigMap %q",
							c.Name, cm.Name),
					},
				}, true
			}
		}
		// Check individual env vars with configMapKeyRef.
		for _, env := range c.Env {
			if env.ValueFrom != nil && env.ValueFrom.ConfigMapKeyRef != nil &&
				env.ValueFrom.ConfigMapKeyRef.Name == cm.Name {
				return domain.Reference{
					From: podRef,
					To:   cm,
					Type: domain.RefTypeEnvFrom,
					Evidence: domain.Evidence{
						Type:   domain.EvidenceVolumeMount,
						Source: fmt.Sprintf("Pod/%s/%s container=%s", pod.Namespace, pod.Name, c.Name),
						Details: fmt.Sprintf("container %q env var %q references ConfigMap %q",
							c.Name, env.Name, cm.Name),
					},
				}, true
			}
		}
	}

	return domain.Reference{}, false
}

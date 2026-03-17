# ADR-0001: Use controller-runtime as the controller framework

**Status**: Accepted
**Date**: 2025-07-17
**Deciders**: Initial project setup

---

## Context

kube-janitor needs a framework for building Kubernetes controllers. Options:
1. Raw `client-go` (informers, work queues, listers)
2. `controller-runtime` (higher-level abstraction, used by Kubebuilder and Operator SDK)
3. Custom framework (full control, high effort)

## Decision

Use `controller-runtime` with Kubebuilder-style conventions.

## Rationale

- **Standard ecosystem**: controller-runtime is the de facto standard for OSS operators; contributors are familiar with it.
- **Batteries included**: leader election, metrics, health probes, scheme registration, event recording, and informer caches are provided out of the box.
- **Testability**: `sigs.k8s.io/controller-runtime/pkg/client/fake` and envtest provide first-class testing support.
- **Informer cache**: reduces API server load compared to raw client calls for list/watch patterns.
- **Extensibility**: adding new reconcilers for new resource kinds is straightforward (implement `Reconcile`, call `SetupWithManager`).

## Trade-offs

- **Informer cache staleness**: cache-backed reads may return stale data. Mitigated by using `APIReader` (direct API server) for safety-critical decisions (owner verification, pre-deletion re-GET).
- **Framework lock-in**: switching away from controller-runtime would require significant rewrite. Acceptable — it is mature and stable.
- **Kubebuilder ceremony**: CRD generation, RBAC markers, etc. add some upfront complexity but pay off in correctness.

## Consequences

- All reconcilers implement `reconcile.Reconciler` and register via `SetupWithManager`.
- Safety-critical reads use `mgr.GetAPIReader()` (bypasses cache).
- CRD types live in `api/v1alpha1/` with controller-gen markers.
- Metrics exposed via the controller-runtime metrics server (`/metrics`).

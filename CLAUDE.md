# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

kube-janitor is a Kubernetes cleanup controller written in Go. It watches cluster resources, classifies them, and applies policy-driven cleanup with strong safety controls. Module path: `github.com/kaiohenricunha/kube-janitor`. Go 1.26, controller-runtime v0.19.3.

**Direction change (ADR-0002).** The Go controller is deprecated and frozen in report-only mode: `config.Validate()` refuses `--dry-run=false`. Do not add features to it. New cleanup work goes into `charts/kube-janitor-policies/` (Kyverno `DeletingPolicy` pack). Read `docs/adr/0002-kyverno-native-cleanup.md`, `docs/specs/kyverno-policy-pack.md`, and `docs/plans/kyverno-migration.md` before any change.

## Build & Development Commands

```bash
make build                          # build controller binary
make test                           # unit tests with race detector
make test-single PKG=./internal/classifier/... TEST=TestClassifier_ProtectedByAnnotation
make lint                           # golangci-lint
make generate                       # controller-gen (DeepCopy + RBAC markers)
make manifests                      # generate CRD YAML
make run                            # run locally against current cluster (dry-run=true)
make docker-buildx                  # build linux/amd64 + linux/arm64 images
make install                        # install CRDs into current cluster
make tools                          # install controller-gen, golangci-lint, setup-envtest

go test -race -run TestName ./internal/package/...   # single test
go build ./...                      # verify compilation
go mod tidy                         # after adding/removing dependencies
```

## Architecture

### Reconcile pipeline (all controllers share this flow)

```
Watch(resource) → Classify → Evaluate policy → Execute + Report
```

### Classification strategy chain (`internal/classifier/`) — priority order, first match wins

1. `ProtectionStrategy` — annotation `janitor.io/protected=true`, protection labels, or protected namespace → `ClassProtected` (exits early, no further strategies run)
2. `FinalizerStrategy` — foreign finalizers (not `janitor.io/cleanup`) → `ClassBlocked`
3. `TTLStrategy` — `janitor.io/ttl` annotation or policy default TTL elapsed → `ClassExpired`
4. `ExpiresAtStrategy` — `janitor.io/expires-at` RFC3339 timestamp past → `ClassExpired`
5. `OwnerRefStrategy` — live ownerRef found → `ClassActive`; all dead → `ClassOrphaned`
6. `ResolverStrategy` — consults resolver registry; live refs → `ClassActive`; none → `ClassAbandoned` (confidence=0.6, heuristic)

`ClassUnknown` means no strategy matched — triggers no action.

### Package layout

- `api/v1alpha1/` — `JanitorPolicy` CRD types + DeepCopy (cluster-scoped)
- `internal/domain/` — pure Go domain types: `Finding`, `ActionDecision`, `ObjectRef`, `Reference`, `ResourceClass`, `Confidence` (no k8s API machinery — independently testable)
- `internal/classifier/` — `Classifier` interface + `DefaultClassifier` + all strategies
- `internal/policy/` — `Engine` interface + `DefaultEngine` (policy hierarchy: per-resource annotation > JanitorPolicy CRD > built-in default)
- `internal/resolver/` — `Resolver` interface + `Registry` + concrete resolvers (ownerref, configmap, namespace)
- `internal/action/` — `Executor` interface + `DefaultExecutor` with pre-flight safety checks
- `internal/report/` — `Reporter` interface + `EventReporter` (Kubernetes Events)
- `internal/config/` — `Config` struct + `Validate()`
- `internal/controller/` — `SharedDeps` struct + `NamespaceReconciler` + `ConfigMapReconciler`
- `pkg/metrics/` — Prometheus `Recorder` (call `metrics.Register()` once at startup)
- `pkg/tracing/` — OpenTelemetry init (no-op when `--otel-endpoint` is empty)
- `pkg/logging/` — zapr-backed logr setup

### Key interfaces

```go
// classifier/classifier.go
type Classifier interface {
    Classify(ctx context.Context, obj client.Object) (domain.Finding, error)
}

// policy/engine.go
type Engine interface {
    Evaluate(ctx context.Context, obj AnnotationReader, finding domain.Finding, policies []v1alpha1.JanitorPolicy) (domain.ActionDecision, error)
}

// resolver/resolver.go
type Resolver interface {
    Name() string
    Handles(obj client.Object) bool
    Resolve(ctx context.Context, obj client.Object, reader client.Reader) ([]domain.Reference, error)
}

// action/executor.go
type Executor interface {
    Execute(ctx context.Context, obj client.Object, finding domain.Finding, decision domain.ActionDecision) error
}
```

### Safety invariants (never break these)

- `ClassProtected` → `ActionNone` always, regardless of policy
- `DryRun=true` (global flag) → no `client.Delete()` calls ever
- Pre-flight before every real delete: re-GET by UID (via `APIReader`, not cache), check finalizers, check ownerRefs
- Protected namespaces (kube-system, kube-public, default, kube-node-lease) → blocked at executor level, not just classifier
- Foreign finalizers → never force-removed; resource gets `ClassBlocked`

### Confidence thresholds

- Auto-delete requires confidence ≥ 0.9 (default, configurable per policy)
- Report-only requires confidence ≥ 0.6
- `ClassAbandoned` (resolver-based) is always capped at confidence=0.6 (heuristic)

## JanitorPolicy CRD

Cluster-scoped. Key fields:
- `spec.selector.kinds` — which resource kinds this policy targets
- `spec.ttl.default` — default TTL if no per-resource annotation
- `spec.protection.annotationKey` — defaults to `janitor.io/protected`
- `spec.action.mode` — `DryRun` | `Report` | `Delete`
- `spec.action.confidenceThreshold` — overrides global default

## TDD/SDD Workflow

1. Write spec in `docs/specs/<feature>-spec.md` (copy `docs/specs/template.md`)
2. Write failing tests mapping to each spec example
3. Implement to make tests pass
4. Add ADR in `docs/adr/` if architectural decision was made

## Adding a new resolver

1. Create `internal/resolver/<kind>_resolver.go` implementing `resolver.Resolver`
2. Register in `cmd/manager/main.go`: `reg.Register(resolver.New<Kind>Resolver(...))`
3. Write contract tests asserting `Evidence` fields are populated
4. Update `docs/specs/<kind>-resolver-spec.md`

## Observability

Metrics registered via `metrics.Register()` at startup. All metrics prefixed `janitor_`:
- `janitor_resources_scanned_total{kind}`
- `janitor_resources_classified_total{kind,class}`
- `janitor_actions_total{kind,action,dry_run}`
- `janitor_reconcile_duration_seconds{controller}`
- `janitor_reconcile_errors_total{controller,kind}`
- `janitor_policy_evaluations_total{policy,action}`

OpenTelemetry tracing is no-op when `--otel-endpoint` is empty. Spans: `<controller>.reconcile`, `classifier.classify`, `policy.evaluate`, `action.execute`.

## CI/CD

- `.github/workflows/ci.yml` — lint + test + build (blocks merge)
- `.github/workflows/security.yml` — govulncheck + gosec + trivy + dependency-review
- `.github/workflows/release.yml` — builds and cosign-signs the deprecated controller image on `v*` tags (GoReleaser and chart-releaser were removed in Phase 3)
- `.github/workflows/release-policies.yml` — packages, pushes, and signs the `kube-janitor-policies` chart on `kube-janitor-policies-v*` tags

## Deployment

Helm chart in `charts/kube-janitor/`. Key values: `controller.dryRun` (default `true`), `controller.scanInterval` (default `5m`), `metrics.serviceMonitor.enabled`.

## Roadmap

Source: `docs/plans/kyverno-migration.md` section 4.

- **Controller Phases 0–1** ✓ — bootstrap and dry-run reporting (historical; the controller is now frozen).
- **Migration Phase 0** — freeze the Go controller: refuse `--dry-run=false`, deprecation notes.
- **Migration Phase 1** — `kube-janitor-policies` chart skeleton + `ephemeralPods` pack, metadata guard, preflight, policy/Helm/e2e tests.
- **Migration Phase 2** — `previewEnvironments`, `temporaryConfigMaps`, `completedJobsRetention`; full Kubernetes 1.34–1.37 matrix; GitOps examples.
- **Migration Phase 3** — release pipeline (OCI + cosign), Artifact Hub, GitOps e2e; deprecate `charts/kube-janitor`; v0.1.0.
- **Migration Phase 4** (optional) — report-only analyzer from `internal/resolver` + `internal/domain`; remove the controller.
- **Migration Phase 5** — v0.2 catalog.

The old controller roadmap (Phase 2 safe deletion, Phase 3 resolver expansion, Phase 4 Git integration) is cancelled.

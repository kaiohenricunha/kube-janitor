# Implementation Gaps & Remaining Work

Snapshot taken after Phase 0 + Phase 1 completion (2026-03-17).
Use this as the starting checklist for Phase 2 onward.

---

## Test Coverage Gaps (Phase 1 — existing code, no tests yet)

### `internal/resolver/`
All four resolvers compile and are wired into `cmd/manager/main.go`, but have zero test coverage.

- [ ] `Registry` — `Register()` deduplication, `ResolveAll()` aggregation and error continuation
- [ ] `OwnerRefResolver` — live owner returns `ClassActive`; dead owner (UID mismatch) returns `ClassOrphaned`
- [ ] `ConfigMapResolver` — Pod referencing CM via volume/envFrom/env returns active ref; unrelated Pod returns empty
- [ ] `NamespaceResolver` — namespace with running Pods returns active ref; empty namespace returns empty

### `internal/config/`
- [ ] `Validate()` — rejects `ScanInterval < 30s`, `ConfidenceThreshold < 0.7`, `ConfidenceThreshold > 1.0`; accepts valid config

### `internal/report/`
- [ ] `EventReporter.Report()` — emits a Kubernetes Event with the correct reason and message for each `ActionDecision`

### `internal/controller/`
Reconcilers need envtest (or a fake-client integration harness) to test the full pipeline.

- [ ] `NamespaceReconciler` — reconcile loop: classify → evaluate → execute → report, including skip-if-terminating guard
- [ ] `ConfigMapReconciler` — same pipeline; verify `listMatchingPoliciesForKind()` filters correctly
- [ ] `SharedDeps` wiring — ensure all dependencies are correctly injected and propagated

---

## Phase 2 — Safe Deletion

Goal: graduate from dry-run-only reporting to confidence-gated real deletion with hardened pre-flight checks.

- [ ] **Real deletion path** — `DefaultExecutor.Execute()` calls `client.Delete()` when `DryRun=false` and `Action=Delete`
- [ ] **Pre-flight hardening**
  - Re-GET by UID via `APIReader` (already stubbed; write test that catches UID mismatch)
  - Abort if re-fetched object has finalizers not present at classify time
  - Abort if re-fetched object gained an ownerRef since classify time
- [ ] **Envtest integration suite** — `test/integration/` using `sigs.k8s.io/controller-runtime/pkg/envtest`
  - Full reconcile loop against a real API server binary
  - Create resource → annotate TTL → wait for Event → assert deleted (or dry-run logged)
- [ ] **Confidence threshold enforcement tests** — assert no delete below 0.9 even with `DryRun=false`
- [ ] **`JanitorPolicy` TTL default** — wire `spec.ttl.default` into `TTLStrategy` as fallback when no per-resource annotation present
- [ ] **Policy selector matching** — test that `spec.selector.kinds` filters correctly across Group/Version/Kind triples

---

## Phase 3 — Resolver Expansion

Goal: broaden resolver coverage to detect more abandoned resource patterns.

- [ ] **`SecretResolver`** — check Pods mounting the Secret as volume or env; check ServiceAccounts referencing it
- [ ] **`PVCResolver`** — check Pods with `spec.volumes[].persistentVolumeClaim.claimName`; check StatefulSets
- [ ] **`ServiceAccountResolver`** — check RoleBindings and ClusterRoleBindings referencing the SA
- [ ] **Confidence scoring refinement** — `ClassAbandoned` currently hard-capped at 0.6; consider scoring based on resource age + resolver evidence count
- [ ] **Resolver contract tests** — each resolver must assert `Evidence` fields (`Type`, `Name`, `Namespace`, `Reason`) are populated

Register new resolvers in `cmd/manager/main.go` and add a spec in `docs/specs/<kind>-resolver-spec.md`.

---

## Phase 4 — Advanced Features

- [ ] **Admission webhook** — `ValidatingWebhookConfiguration` that rejects resources violating TTL or expiry policy at creation time
- [ ] **Git provider integration** — detect open/closed PRs (GitHub, GitLab) and map to namespace lifecycle; close PR → mark namespace for cleanup
- [ ] **`PRNamespaceReconciler`** — dedicated reconciler consuming `spec.prNamespace` fields from `JanitorPolicy`
- [ ] **Multi-cluster support** — optionally watch a remote cluster's API server
- [ ] **Web UI / dashboard** — read-only view of findings, decisions, and audit trail

---

## Housekeeping / Quality

- [ ] **`golangci-lint` clean pass** — run `make lint` and fix any issues (godot comments, unused exports, etc.)
- [ ] **`go vet` shadow check** — `govet` shadow linter is enabled; audit all reconciler loops
- [ ] **Fuzz targets** — `TTLStrategy` duration parsing and `ExpiresAtStrategy` timestamp parsing are good candidates
- [ ] **RBAC audit** — verify `charts/kube-janitor/templates/rbac.yaml` covers all resource kinds the reconcilers watch (Secrets, PVCs once Phase 3 lands)
- [ ] **Helm chart `values.schema.json`** — add JSON schema for Helm values validation
- [ ] **`make tools` pinning** — pin exact versions of `controller-gen` and `golangci-lint` in Makefile to prevent CI drift

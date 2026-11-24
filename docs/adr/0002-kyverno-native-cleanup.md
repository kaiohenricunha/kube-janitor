# ADR-0002: Deliver cleanup as a Kyverno policy pack; keep analysis optional and report-only

**Status**: Proposed
**Date**: 2026-11-24
**Supersedes**: the controller design behind [ADR-0001](0001-use-controller-runtime.md) for the cleanup product. ADR-0001 still applies to any future Go analyzer.
**Details**: [kyverno-policy-pack.md](../specs/kyverno-policy-pack.md) · [compatibility.md](../specs/compatibility.md) · [gitops-integration.md](../specs/gitops-integration.md) · [security-and-testing.md](../specs/security-and-testing.md) · [kyverno-migration.md](../plans/kyverno-migration.md)

---

## Context

kube-janitor is a controller-runtime controller (`cmd/manager/main.go`) that classifies Namespaces and
ConfigMaps and deletes them according to a `JanitorPolicy` CRD. It has never been released: the
repository has no tags and no GitHub releases.

An audit on 2026-11-24 ([kyverno-migration.md](../plans/kyverno-migration.md) section 2) found:

- Two critical defects. A `Delete` policy deletes resources classified **Active** (D1), and policy
  selectors are ignored (D2). A throwaway probe showed a non-preview Namespace with a running Pod
  deleted by a policy written for `env=preview` namespaces.
- High-severity defects: an annotation that deletes without any policy (D3), no floor on per-policy
  confidence (D4), most CRD fields unimplemented (D5), and racy pre-flight checks (D6).
- The controller is safe only because `--dry-run=true` is the default.

Meanwhile, Kyverno 1.20 (2026-11-12) ships a stable `DeletingPolicy` API (`policies.kyverno.io/v1`, served
since 1.17) executed by its cleanup controller, plus a label-based TTL mechanism. Kubernetes itself
provides Job TTL, CronJob history limits, and owner-reference garbage collection.

## Problem statement

How should kube-janitor become a reusable cleanup solution that is safe by default, easy to install,
compatible with GitOps, and that does not re-implement what Kubernetes and Kyverno already do?

## Alternatives considered

### A1. Fix and continue the custom Go operator

| Pros | Cons |
|------|------|
| Full control: native dry-run, cross-object analysis, custom status and events | Re-implements scheduling, selection, RBAC per kind, and pre-flight safety that Kyverno and Kubernetes already provide |
| No dependency on Kyverno | The current code needs a near-rewrite (D1–D6) before it can delete safely |
| | Users must trust one more controller with broad delete rights; the chart already grants delete on kinds it does not handle (D11) |
| | Every Kubernetes minor and every new kind adds maintenance |

### A2. Kyverno policy pack only

| Pros | Cons |
|------|------|
| No workload, no image, no CRD of our own; one `helm install` | Requires Kyverno ≥ 1.20; follows Kyverno's ~3-month support window |
| Reuses Kyverno's engine, metrics, and RBAC aggregation | `DeletingPolicy` has no dry-run (#17638): the pack must build an audit mode |
| Policies are declarative and reviewable in Git | Known upstream gaps: no UID precondition (#18160), Ready never set (#18813), schedule stops after 10 failed retries (#18248), `excludeResourceRules` ignored (#17884) |
| Fits GitOps installs naturally | Decisions must be object-local to be safe; no reliable "is it unused" analysis |

### A3. Hybrid: policy pack (primary) + optional report-only analyzer (future)

| Pros | Cons |
|------|------|
| A2's simplicity for the common, explicit cases | Two artifacts to version eventually |
| Cross-object analysis where it is needed, without delete rights | Analyzer value is unproven until built |
| Analyzer reuses `internal/resolver` and `internal/domain` | |

### A4. Native Kubernetes features and documentation only

Covers finished Jobs (TTL, CronJob limits) and owned objects (GC). It does not cover preview
Namespaces, standalone Pods, or temporary ConfigMaps. Rejected as the whole answer, adopted as
principle P5 ("native first").

### A5. Kyverno `cleanup.kyverno.io/ttl` label only

Any principal who can set a label can schedule a deletion. The label works in every namespace for every
granted kind and ignores `resourceFilters`. Its documented time base conflicts with the implementation.
Rejected ([kyverno-policy-pack.md](../specs/kyverno-policy-pack.md) section 5.4).

Third-party cleanup controllers and unused-resource scanners exist in the ecosystem. They were not
evaluated in this session.

## Decision

Adopt **A3**, delivered in this order:

1. Ship `kube-janitor-policies`, a Helm chart of Kyverno `DeletingPolicy` resources, an audit-mode
   `ValidatingPolicy` per pack, per-pack aggregated RBAC, and a Kubernetes `ValidatingAdmissionPolicy`
   metadata guard. Target Kyverno ≥ 1.20.0 and Kubernetes 1.34–1.37.
2. The pack acts only on **explicit, authorized lifecycle metadata** and on objective native state
   (finished standalone Jobs). It never infers "unused".
3. Freeze the Go controller now. Make it refuse `--dry-run=false`. Deprecate it and its chart when the
   policy chart ships.
4. Build the analyzer later, report-only, from `internal/resolver` and `internal/domain`. The policy
   pack never depends on it.

## Rationale

- **Safety comes from opt-in, not from inference.** Every audited defect came from inferring intent
  (classification, confidence). The pack replaces inference with metadata that a principal who could
  delete the object has set (guard rule G-OPT).
- **The hard parts are already built.** Kyverno provides scheduling, server-side selection, CEL,
  RBAC aggregation, metrics, and `resourceFilters`. Kubernetes provides admission-time authorization checks.
- **No existing users.** With no release, a clean product change costs nothing in compatibility.
- **GitOps fit.** A chart with no workload is easy to pin, review, and sync. Manager markers keep the
  pack away from GitOps-owned objects.

## Benefits

- One `helm install`; nothing deletes until `enabled: true` **and** `mode: delete`.
- Small, auditable surface: rendered YAML is the whole product, checked in as golden files.
- Independent layers (selector, CEL, `resourceFilters`, RBAC, admission) each stop a different mistake.

## Trade-offs

- Hard dependency on Kyverno and its release cadence.
- Workarounds for upstream gaps (audit mode via `ValidatingPolicy`, regex before `timestamp()`, no
  `excludeResourceRules`, custom Argo CD health check).
- Expiry precision equals the schedule interval (default 10 minutes).
- No reference-aware ConfigMap cleanup in v0.1.
- The audit mode needs the Kyverno reports controller (enabled by default) and is only as fresh as its background scan (1h default).

## Security implications

- The cleanup controller gains cluster-wide delete on each delete-mode pack's kinds. RBAC cannot scope
  it to namespaces; policy conditions do ([security-and-testing.md](../specs/security-and-testing.md) section 3).
- That grant also arms `cleanup.kyverno.io/ttl` for those kinds everywhere. The metadata guard covers that label.
- Kyverno aggregates `namespaceddeletingpolicies` write access into the built-in `admin` role. The pack
  therefore grants only kinds that `admin` can already delete in a namespace.
- `DeletingPolicy` write access equals delete access on the granted kinds. Restrict it to platform administrators.
- Residual risk: a same-name object created between the controller's LIST and DELETE in one run (#18160).

## Consequences

- New chart `charts/kube-janitor-policies/`; new test tree (`tests/kyverno`, `tests/chainsaw`, `tests/golden`).
- `JanitorPolicy`, the policy engine, the executor, and the reconcilers are removed after migration
  ([kyverno-migration.md](../plans/kyverno-migration.md) section 3).
- `CLAUDE.md`, `README.md`, and the roadmap must be updated when Phase 1 starts. The roadmap "Phase 2 — safe deletion" of the Go controller is cancelled.
- CI gains Kyverno CLI, Helm 3/4, kind, and Chainsaw jobs.

## Open questions

| # | Question | Recommended default | Alternatives |
|---|----------|---------------------|--------------|
| Q1 | The label prefix `janitor.io/` is a DNS name the project does not own. Change it before the first release? | Decide before v0.1.0. Default: keep `janitor.io/` for continuity, and document the collision risk. | A prefix under a domain the maintainer controls. |
| Q2 | The name "kube-janitor" is also used by at least one older, unrelated open-source cleanup project, which uses `janitor/ttl` and `janitor/expires` annotations **[unverified in this session]**. Is the chart name distinct enough on Artifact Hub? | Keep `kube-janitor-policies`; state the difference in the README. | Rename the product. |
| Q3 | Keep Kubernetes 1.34 (upstream EOL 2026-10-27) in the tested matrix? | Yes, while Kyverno supports it. | Test 1.35–1.37 only. |
| Q4 | Analyzer output format | OpenReports/`PolicyReport` objects, so findings appear next to Kyverno's | JSON report + metrics only |
| Q5 | Default TTLs: preview 72h, Pods 24h, ConfigMaps 168h, Jobs 168h | As listed | Shorter Pod TTL (8h) |
| Q6 | Should the metadata guard ship even when every pack is in audit mode? | Yes (it is rendered whenever a pack is enabled) | Only in delete mode |
| Q7 | Contribute upstream fixes (#17638 dry-run, #18160 preconditions, #18813 Ready, #18248 retries)? | Yes; each fix removes a workaround | Wait |
| Q8 | Keep the analyzer in this repository? | Yes, under `cmd/analyzer` | Separate repository |
| Q9 | Guard rule G-MIX (a GitOps object created in an opted-in namespace) defaults to Warn + Audit. The namespace is still deleted at expiry, with its GitOps content. Is that the right default? | Warn + Audit: it keeps the "leftover namespace backstop" pattern working ([gitops-integration.md](../specs/gitops-integration.md) section 5.3) | Deny: fail loudly at sync time; breaks that pattern |
| Q10 | The CEL in the illustrative policies was never compiled (the Kyverno CLI download was not permitted in the authoring session). | Phase 1 compiles it first and updates the specs | — |

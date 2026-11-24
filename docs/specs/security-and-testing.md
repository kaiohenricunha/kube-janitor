# Spec: Deletion safety, security model, and test strategy

**Status**: Draft
**Date**: 2026-11-24
**Related**: [kyverno-policy-pack.md](kyverno-policy-pack.md) · [compatibility.md](compatibility.md) · [gitops-integration.md](gitops-integration.md) · [kyverno-migration.md](../plans/kyverno-migration.md)

---

## 1. Summary

kube-janitor deletes things. This spec states the invariants that must always hold, which layer
enforces each one, what each layer cannot do, and the tests that prove the invariants before a release.

Claim tags: **[upstream]**, **[local]**, **[inferred]**, **[to-test]** (see [compatibility.md](compatibility.md)).

## 2. Deletion-safety invariants

Each invariant has at least one enforcing layer and at least one negative test (section 6).

| ID | Invariant | Enforced by | Negative test |
|----|-----------|-------------|---------------|
| S1 | A default install deletes nothing and grants no delete permission. | Chart: all packs `enabled: false`; delete RBAC rendered only for `mode: delete`. | H1, H2 |
| S2 | An enabled pack deletes nothing until its mode is `delete`. | Chart: `audit` renders no `DeletingPolicy` and no delete RBAC. | H3, E4 |
| S3 | Objects in `kube-system`, `kube-public`, `kube-node-lease`, `default`, and the Kyverno namespace are never deleted, and those Namespaces are never deleted. | Selector + CEL condition (built-in list, values can only add); Kyverno `resourceFilters` for 4 of 5 names **[local]**; guard rule G-NS. | K3, K4, E3 |
| S4 | An object or namespace labeled `janitor.io/protected=true` is never deleted. | Selector + CEL; guard rule G-PROT. | K5, K6, E3 |
| S5 | An object without the opt-in (or, for Jobs, without the namespace opt-in) is never deleted. | Selector (server-side) + CEL. | K1, E5 |
| S6 | An object with a known manager marker (Argo CD, Flux, Helm) is never deleted. | CEL `no-known-manager`. | K7–K10, E6 |
| S7 | An object with a missing or malformed expiry is kept, unless the pack's default TTL applies to a *missing* annotation. | CEL expiry rule ([kyverno-policy-pack.md](kyverno-policy-pack.md) section 5.3); guard rule G-FMT. | K11, K12 |
| S8 | An owned object (`ownerReferences` non-empty) is never deleted by the Pod, ConfigMap, or Job packs. | CEL `standalone`. | K13 |
| S9 | Finalizers are never removed; terminating objects are skipped. | No pack touches `metadata.finalizers`; CEL `not-terminating`. | K14, E9 |
| S10 | Only a principal who may delete an object can make it eligible (opt-in, expiry, TTL label, protection removal). | Guard rule G-OPT with the API server `authorizer`; for labels that predate the guard, preflight check P8 before delete mode. | E7, E8, E16 |
| S11 | The cleanup controller holds delete permission only on the kinds of packs in `delete` mode. | One aggregated ClusterRole per delete-mode pack. | H4, E15 |
| S12 | Uninstalling the chart deletes no user object. | Chart owns no user objects and no CRDs. | E12 |
| S13 | A malformed annotation on one object cannot stop cleanup for other objects. | Regex guard before `timestamp()`; guard rule G-FMT. | K12, E13 |

## 3. Permission boundaries

### 3.1 What Kyverno has by default

- [local] The 3.10.0 cleanup controller ClusterRole grants `delete` only on `validatingwebhookconfigurations`,
  `globalcontextentries`, and `updaterequests`. It reads `namespaces` and `configmaps`.
- [upstream] "Kyverno cleanup-controller always requires RBAC permissions for deleting resources (even pods)."
- [upstream] Extra permissions come from ClusterRoles labeled `rbac.kyverno.io/aggregate-to-cleanup-controller: "true"`.

### 3.2 What the pack adds

| Pack in `delete` mode | Rules in its aggregated ClusterRole |
|-----------------------|-------------------------------------|
| `previewEnvironments` | `""/namespaces`: get, list, watch, delete |
| `ephemeralPods` | `""/pods`: get, list, watch, delete |
| `temporaryConfigMaps` | `""/configmaps`: get, list, watch, delete |
| `completedJobsRetention` | `batch/jobs`: get, list, watch, delete (Pods are removed by the garbage collector with `Background` propagation) |

ClusterRoles are cluster-wide. **RBAC cannot restrict the cleanup controller to some namespaces.**
Namespace scope comes only from policy selectors and conditions (layer 1 in section 5).

### 3.3 Side effects of granting these permissions

| Side effect | Why | Mitigation |
|-------------|-----|------------|
| The `cleanup.kyverno.io/ttl` label becomes effective for the granted kinds in **every** namespace, without `resourceFilters` | The TTL manager watches every kind for which the controller has cluster-wide list, watch, and delete **[upstream, source]** `pkg/controllers/ttl/manager.go:210-219`; the TTL path does not read `resourceFilters` **[inferred, source]**. | Guard rules G-OPT and G-NS cover `cleanup.kyverno.io/ttl`. |
| Namespace admins can create `NamespacedDeletingPolicy` objects that run with the cleanup controller's permissions in their namespace | [local] Kyverno 3.10.0 aggregates create/update/delete on `deletingpolicies` and `namespaceddeletingpolicies` into the built-in `admin` ClusterRole (`kj-kyverno:rbac:admin:policies`, label `rbac.authorization.k8s.io/aggregate-to-admin`). A RoleBinding to `admin` grants the namespaced kind only. | **Rule:** the pack grants only kinds that the built-in `admin` role can already delete inside a namespace (Pods, ConfigMaps, Jobs). Then a namespaced policy gives a namespace admin nothing they did not have. Namespace deletion is cluster-scoped and cannot be targeted by `NamespacedDeletingPolicy` **[upstream]**. Adding a kind that `admin` cannot delete (for example `ResourceQuota`) is forbidden without a new security review. |
| Anyone who can create or edit a cluster-scoped `DeletingPolicy` can delete every granted kind cluster-wide | `DeletingPolicy` is the deletion program. | Restrict `deletingpolicies` write to platform administrators. Only cluster-wide bindings to `admin` grant it. Recommend an audit alert on writes to `deletingpolicies` by principals other than the GitOps controller. |
| Anyone who can edit the Kyverno ConfigMap can weaken `resourceFilters` | `resourceFilters` is an independent layer. | Not relied on alone; S3 is enforced in each policy too. |

### 3.4 Separate execution boundaries

Option considered: run deletions under a dedicated identity (a second cleanup controller, or a
kube-janitor executor) so that the kube-janitor permissions do not mix with other Kyverno policies.

| Option | Isolation gained | Cost | Decision |
|--------|------------------|------|----------|
| Shared Kyverno cleanup controller (aggregated roles) | None between `DeletingPolicy` authors | None | **v0.1** |
| Second Kyverno installation for cleanup only | Separate ServiceAccount and RBAC | A second Kyverno; CRDs are cluster-wide, so both installations see all policies **[inferred]** | Rejected: no real separation |
| kube-janitor executor that impersonates the opt-in author | Strong: deletes with the author's own rights | A custom controller again; impersonation rights are dangerous | Rejected for the pack |
| Guard that requires delete rights at opt-in time (S10) | Gives most of the impersonation benefit at admission time | One `ValidatingAdmissionPolicy` | **v0.1** |

## 4. Metadata guard (admission)

The guard is a Kubernetes `ValidatingAdmissionPolicy` (GA in 1.30 **[upstream]**). It runs in the API
server, so it does not depend on Kyverno webhooks being available. It is rendered when any pack is enabled.

### 4.1 Rules

| Rule | Trigger (CREATE or UPDATE) | Check | Default action |
|------|----------------------------|-------|----------------|
| G-OPT | `janitor.io/ephemeral`, `janitor.io/expires-at`, `janitor.io/job-retention`, or `cleanup.kyverno.io/ttl` is added or its value changes | The requesting principal may `delete` this object (for `job-retention`: may delete `jobs` in this namespace), checked with `authorizer` | Deny |
| G-FMT | `janitor.io/expires-at` present and changed | Value matches `YYYY-MM-DDTHH:MM:SSZ` and `timestamp()` succeeds | Deny |
| G-PROT | `janitor.io/protected=true` is removed or changed | The principal may `delete` this object | Deny |
| G-NS | Any lifecycle key above is added to an object in, or to, a protected namespace | Always fails | Deny |
| G-MIX | An object with a manager marker is created in a Namespace labeled `janitor.io/ephemeral=true` | Always fails | Warn + Audit (configurable to Deny) |

**The guard must block (`Deny`) whenever any pack is in `delete` mode.** In that state it is the only
control on the `cleanup.kyverno.io/ttl` path, which skips the protected-namespace list. The chart refuses
to render otherwise (test H7). G-MIX has its own policy and binding, because `validationActions` is set per
binding; weakening G-MIX never weakens G-OPT, G-FMT, G-PROT, or G-NS.

Removing the opt-in label, and adding `janitor.io/protected=true`, are always allowed. They only make
deletion less likely.

Removing `janitor.io/expires-at` while the opt-in label stays is **not** a safe direction: the pack then
falls back to `creationTimestamp + defaultTTL`, which can already be in the past
([kyverno-policy-pack.md](kyverno-policy-pack.md) section 5.5). G-OPT therefore treats that removal as a
change that needs delete permission.

### 4.2 Illustrative rule

**Not machine-validated.** The `authorizer` and `request` variables are documented for
`ValidatingAdmissionPolicy` **[upstream]**; the exact expression is **[to-test]**.

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: kube-janitor-metadata-guard
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
      - apiGroups: [""]
        apiVersions: ["v1"]
        resources: ["namespaces", "pods", "configmaps"]
        operations: ["CREATE", "UPDATE"]
      - apiGroups: ["batch"]
        apiVersions: ["v1"]
        resources: ["jobs"]
        operations: ["CREATE", "UPDATE"]
  matchConditions:
    - name: touches-lifecycle-metadata
      expression: >-
        object.metadata.?labels.orValue({}).exists(k, k.startsWith('janitor.io/') || k == 'cleanup.kyverno.io/ttl') ||
        object.metadata.?annotations.orValue({}).exists(k, k.startsWith('janitor.io/')) ||
        (oldObject != null && oldObject.metadata.?labels.orValue({}).exists(k, k == 'janitor.io/protected'))
  variables:
    - name: newOptIn
      expression: "object.metadata.?labels.orValue({})[?'janitor.io/ephemeral'].orValue('')"
    - name: oldOptIn
      expression: "oldObject == null ? '' : oldObject.metadata.?labels.orValue({})[?'janitor.io/ephemeral'].orValue('')"
    - name: canDelete
      expression: >-
        authorizer.group(request.resource.group).resource(request.resource.resource)
          .namespace(request.namespace).name(object.metadata.name).check('delete').allowed()
  validations:
    - expression: "variables.newOptIn == '' || variables.newOptIn == variables.oldOptIn || variables.canDelete"
      messageExpression: "'janitor.io/ephemeral requires permission to delete ' + request.resource.resource + '/' + object.metadata.name"
    # G-FMT, G-PROT, G-NS, and the expires-at / cleanup.kyverno.io/ttl variants follow the same shape.
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: kube-janitor-metadata-guard
spec:
  policyName: kube-janitor-metadata-guard
  validationActions: [Deny]
```

### 4.3 Why the guard covers `cleanup.kyverno.io/ttl` for all kinds of enabled packs only

The guard must cover every kind for which the pack grants delete permission (section 3.3). Kinds that
the pack does not grant are outside its responsibility. If an operator granted the cleanup controller
other kinds before, the TTL label is already armed for them. The README states this.

## 5. Safety layers and their limits

| Layer | What it does | What it cannot do |
|-------|--------------|-------------------|
| 1. Kyverno policy conditions (`DeletingPolicy` selectors and CEL) | Decide per object, on live data, at each run. | They cannot see who set the metadata. They do not apply to the `cleanup.kyverno.io/ttl` path. A CEL error can stop a policy (#18248). There is no dry-run. The DELETE has no UID precondition (#18160). |
| 2. Kubernetes RBAC | Caps which kinds the cleanup controller can delete. | No per-namespace scope for an aggregated ClusterRole. Cannot tell kube-janitor policies from other `DeletingPolicy` objects. |
| 3. Admission (`ValidatingAdmissionPolicy` guard) | Checks *who* makes an object eligible and the format of the value. Works without Kyverno webhooks. | Does not cover objects labeled before the guard existed; preflight check P8 finds pre-existing `cleanup.kyverno.io/ttl` labels before delete mode. A cluster admin can delete the guard. It cannot see GitOps ownership that leaves no marker. |
| 4. Operational safeguards (audit mode, preflight, alerts, review of values changes, audit logs) | Make mistakes visible before and after deletion. | They do not prevent a deletion by themselves. |

## 6. Test strategy

### 6.1 Static policy tests (`kyverno test`, Kyverno CLI v1.20.0)

[upstream] `kyverno test` supports `DeletingPolicy` with `isDeletingPolicy: true`; `pass` means the
resource would be deleted, `fail` means it is kept, and excluded resources report `skip` (1.20, #17458).
Test inputs are the policies rendered by `helm template` with `ci/all-delete.yaml`.

Fixture timestamps are fixed far in the past (`2000-01-01T00:00:00Z`) or far in the future
(`2999-01-01T00:00:00Z`), so the result does not depend on when CI runs.

| ID | Case | Expected |
|----|------|----------|
| K1 | No opt-in label, expired by age | skip/fail (kept) |
| K2 | Opt-in, `expires-at` in the past | pass (deleted) |
| K3 | Opt-in object in `default` | kept |
| K4 | Namespace named `kube-system` with opt-in | kept |
| K5 | Opt-in object labeled `janitor.io/protected=true` | kept |
| K6 | Opt-in object in a namespace labeled `janitor.io/protected=true` | kept |
| K7 | Opt-in object with `argocd.argoproj.io/tracking-id` | kept |
| K8 | Opt-in object with `kustomize.toolkit.fluxcd.io/name` | kept |
| K9 | Opt-in object with `helm.toolkit.fluxcd.io/name` | kept |
| K10 | Opt-in object with `meta.helm.sh/release-name` or `app.kubernetes.io/managed-by: Helm` | kept |
| K11 | Opt-in, `expires-at` in the future, created long ago | kept |
| K12 | Opt-in, `expires-at: garbage`, `2026-13-45T99:99:99Z`, `2026-02-30T00:00:00Z` | kept, and no evaluation error (see [kyverno-policy-pack.md](kyverno-policy-pack.md) section 6.3 item 5) |
| K13 | Opt-in Pod with an ownerReference | kept |
| K14 | Opt-in object with `deletionTimestamp` | kept |
| K15 | Opt-in, no annotation, age > defaultTTL | deleted |
| K16 | Opt-in, no annotation, age < defaultTTL | kept |
| K17 | Boundary: `expires-at` equal to "now" ± 1 minute is not testable deterministically in the CLI; covered by E4 | — |
| K18 | Job `Complete`, finished 30 days ago, standalone, in opted-in namespace | deleted |
| K19 | Job with `ttlSecondsAfterFinished` set | kept |
| K20 | Job owned by a CronJob | kept |
| K21 | Job running (no Complete/Failed condition) | kept |
| K22 | Job in a namespace without `janitor.io/job-retention=enabled` | kept |
| K23 | Pack overlap: an object matched by two packs is deleted by neither twice (policy conflict); each kind belongs to exactly one pack | rendering check H5 |

**Audit/delete equivalence (A1).** For every fixture above, the audit `ValidatingPolicy` must report
`fail` exactly where the `DeletingPolicy` reports `pass`. CI runs both suites on the same fixtures.

**CEL validity.** Each policy compiles under `kyverno test`. A deliberately broken CEL expression in a
fixture policy must make the suite fail (negative control).

### 6.2 Helm tests

| ID | Test | Tool |
|----|------|------|
| H1 | Default values render no `DeletingPolicy`, no `ValidatingPolicy`, no ClusterRole, no guard. | `helm template` + assertions (helm-unittest or a small script; tool choice in Phase 1) |
| H2 | No rendered ClusterRole contains `delete` unless some pack has `mode: delete`. | same |
| H3 | `enabled: true, mode: audit` renders a `ValidatingPolicy` and no `DeletingPolicy`. | same |
| H4 | Each delete-mode pack renders exactly its own aggregated ClusterRole with only its kinds. | same |
| H5 | No two packs target the same group/resource. | same |
| H6 | `values.schema.json` rejects unknown keys, bad durations, bad modes. | `helm lint --strict`, `helm template` with bad values files |
| H7 | `mode: delete` with `metadataGuard.validationActions` lacking `Deny` fails the render. | `helm template` expect failure |
| H8 | Render fails without `policies.kyverno.io/v1` in capabilities, and passes with `--api-versions`. | `helm template` |
| H9 | `kubeVersion` constraint rejects 1.33. | `helm template --kube-version 1.33.0` expect failure |
| H10 | Golden files in `tests/golden/` match the render for every `ci/*.yaml`. | `diff` |
| H11 | Every action and image in workflows and templates is pinned by digest or SHA. | grep check |

All Helm tests run with Helm 3.22.x and Helm 4.3.x.

### 6.3 Integration tests (Chainsaw on kind)

Environment: kind v0.33.0 with the digest-pinned node images of [compatibility.md](compatibility.md)
section 2.2, Kyverno chart 3.10.0 (`--set config.generateSuccessEvents=true`), and the pack installed from
the working tree with `ci/e2e.yaml` (schedule `*/1 * * * *`). Pattern from Kyverno's own conformance tests
**[upstream]**: apply, wait one schedule (`sleep 1m5s`), then assert with an `error:` step that a resource is gone.

| ID | Scenario (task requirement) | Steps | Pass criteria |
|----|-----------------------------|-------|---------------|
| E1 | Kyverno prerequisites met (1) | Run `hack/preflight.sh` | Exit 0; P1–P8 PASS (P6 may report WAIT within one schedule interval of install) |
| E2 | Authorized ephemeral Pod expires and is deleted (2) | Create Pod with opt-in and past `expires-at` as an identity that may delete Pods | Pod gone after one run; `kyverno_deleting_controller_deletedobjects_total{policy_name="kube-janitor-ephemeral-pods"}` increased |
| E3 | Protected Pod untouched (3) | Opt-in + expired Pod with `janitor.io/protected=true`; another in `default` | Both present after two runs |
| E4 | Preview Namespace cleaned only under an authorized policy (4) | (a) opted-in expired Namespace with `previewEnvironments` in `audit` → present and reported; (b) switch to `delete` → gone; (c) expired Namespace without opt-in → present | as stated |
| E5 | Unknown ownership not deleted (5) | Expired-age Pod and ConfigMap with no opt-in | present |
| E6 | GitOps-managed resource protected (6), simulated | Opt-in expired Pod with an Argo CD tracking annotation; another with Flux labels | present |
| E7 | Opt-in by an unauthorized principal is rejected | As a ServiceAccount with `patch` but not `delete` on Pods, add `janitor.io/ephemeral=true` | Admission denied with the G-OPT message |
| E8 | `cleanup.kyverno.io/ttl` by an unauthorized principal is rejected | Same identity, add `cleanup.kyverno.io/ttl=1m` | Denied |
| E9 | Finalizer respected | Opt-in expired ConfigMap with a foreign finalizer | Stays `Terminating`; finalizer intact; no repeated DELETE errors |
| E10 | Missing cleanup-controller permissions detected (7) | Pack in `delete` mode; remove its aggregated ClusterRole | `hack/preflight.sh` P4 FAIL; nothing deleted; `kyverno_deleting_controller_errors_total` increased |
| E11 | Helm upgrade preserves behavior (8) | Install previous release (once published; before that, the `main` build) with ephemeralPods in delete mode; upgrade to the candidate | Same `enabled`/`mode`; E2 and E3 still hold; no new delete RBAC |
| E12 | Uninstall removes nothing unexpected (9) | Create unrelated objects and opted-in non-expired objects; `helm uninstall` | All user objects present; chart objects gone; SubjectAccessReview for the cleanup controller now denies `delete pods` |
| E13 | Malformed annotation does not stall cleanup | Before the chart (and its guard) is installed, create one Pod with `expires-at: 2026-02-30T00:00:00Z` and one valid expired Pod. Then install with `ephemeralPods` in delete mode. | Valid Pod deleted; no `kyverno_controller_drop_total{controller_name="deleting-controller"}` increase |
| E14 | Completed Job retention | In an opted-in namespace, create a standalone Job that completes at once, and a CronJob that creates Jobs. `ci/e2e.yaml` sets `retention: 1m`. | Standalone Job deleted within retention plus two runs; CronJob-owned Jobs kept |
| E16 | TTL-label arming is real and is guarded | (a) With `ephemeralPods` in `audit` mode, as cluster-admin, label a Pod in a non-protected namespace `cleanup.kyverno.io/ttl=1m`. (b) Switch to `delete` mode and label a second Pod the same way. (c) Try the same label on a Pod in `kube-system`. (d) Before (b), run `hack/preflight.sh` with a TTL-labeled Pod in `default`. | (a) Pod present after 3 minutes (no grant yet). (b) Pod deleted by the TTL controller; this turns the source-code claim in section 3.3 into a tested fact. (c) Denied by G-NS. (d) P8 FAIL. |
| E15 | Delete RBAC follows delete-mode packs only | Install with `ephemeralPods` in `delete` mode and `temporaryConfigMaps` in `audit` mode. Create `SubjectAccessReview`s for the cleanup controller ServiceAccount. | `delete pods` allowed; `delete configmaps`, `delete namespaces`, `delete jobs` denied (on a cluster with no other grants) |

### 6.4 GitOps tests

| ID | Scenario | Needs real tool? | Job |
|----|----------|------------------|-----|
| GO1 | Marker exclusion for Argo CD and Flux objects | No — simulated with labels and annotations (E6) | `e2e` (every PR) |
| GO2 | Argo CD self-heal recreate loop does not occur for a tracked object | Yes: Argo CD 3.6.x | `gitops-e2e` (nightly, release gate) |
| GO3 | ApplicationSet PR generator pattern B removes the namespace without kube-janitor | Yes: Argo CD + a fake SCM (the generator's `pullRequest.github` needs an API; use a local Gitea or a stub) **[to-test]** | `gitops-e2e` |
| GO4 | Flux Kustomization object excluded; deleting the Kustomization with `prune: true` removes the environment | Yes: Flux 2.10.x | `gitops-e2e` |
| GO5 | Install the chart through Argo CD and Flux (section 7 of [gitops-integration.md](gitops-integration.md)); render gate passes | Yes | `gitops-e2e` |
| GO6 | G-MIX warning appears when Argo CD syncs into an opted-in namespace | Yes: Argo CD | `gitops-e2e` |

## 7. Release validation gates

A release tag is published only when all of these hold on the release commit:

1. `policy-unit` (K1–K23, A1, CEL negative control) passes.
2. `chart-render` (H1–H11) passes on Helm 3.22.x and 4.3.x.
3. `e2e` (E1–E14) passes on Kubernetes 1.34, 1.35, 1.36, and 1.37.
4. `gitops-e2e` (GO2–GO6) passed within the last 24 hours on the release commit.
5. The changelog lists every change that widens deletion under `BREAKING-SAFETY`, and a maintainer approved it.
6. The pushed chart digest is signed and verified (`cosign verify`).
7. No unpinned action or image (H11).

## 8. Acceptance criteria for the test suite

- Every invariant S1–S13 maps to at least one passing negative test.
- Every v0.1 pack has positive and negative `kyverno test` cases and at least one end-to-end deletion test.
- A test that expects deletion asserts the specific object is gone and that a neighbor control object is still present.
- No test depends on wall-clock timing finer than one schedule interval plus 5 seconds.
- `make test` (Go) keeps passing while the Go code exists. After the testcontainers or kind runs, CI cleans up volumes.

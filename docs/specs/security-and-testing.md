# Spec: Deletion safety, security model, and test strategy

**Status**: Implemented in kube-janitor-policies 0.1.0 (results: [kyverno-migration.md](../plans/kyverno-migration.md) §6)
**Date**: 2026-11-24
**Related**: [kyverno-policy-pack.md](kyverno-policy-pack.md) · [compatibility.md](compatibility.md) · [gitops-integration.md](gitops-integration.md) · [kyverno-migration.md](../plans/kyverno-migration.md)

---

## 1. Summary

kube-janitor deletes things. This spec states the invariants that must always hold, which layer
enforces each one, what each layer cannot do, and the tests that prove the invariants before a release.

Claim tags: **[upstream]**, **[local]**, **[inferred]**, **[to-test]** (see [compatibility.md](compatibility.md)).

## 2. Deletion-safety invariants

Each invariant has at least one enforcing layer and at least one negative test (section 6). All tests
listed here pass in CI on Kubernetes 1.34–1.37 **[local]**.

| ID | Invariant | Enforced by | Negative test |
|----|-----------|-------------|---------------|
| S1 | A default install deletes nothing and grants no delete permission. | Chart: all packs `enabled: false`; delete RBAC rendered only for `mode: delete`. | H1, H2 |
| S2 | An enabled pack deletes nothing until its mode is `delete`. | Chart: `audit` renders no `DeletingPolicy` and no delete RBAC. | H3, E4a, E16a |
| S3 | Objects in `kube-system`, `kube-public`, `kube-node-lease`, `default`, and the Kyverno namespace are never deleted, and those Namespaces are never deleted. | Selector + CEL condition (built-in list, values can only add); Kyverno `resourceFilters` for 4 of 5 names **[local]**; guard rule G-NS. | K3, K4, E3, H15 |
| S4 | An object or namespace labeled `janitor.io/protected=true` is never deleted. The annotation of the same name (the deprecated controller's form) protects too (R40). | Selector + CEL; guard rule G-PROT, which matches Namespaces whenever a namespaced pack is enabled (R38). | K5, K6, K5b, K6b, H19, E3, `20-guard` |
| S5 | An object without the opt-in (or, for Jobs, without the namespace opt-in) is never deleted. | Selector (server-side) + CEL. | K1, K22, E5, E4c |
| S6 | An object with a known manager marker (Argo CD, Flux, Helm) is never deleted. | CEL `no-known-manager`. | K7–K10, E6, GO2, GO4 |
| S7 | An object with a missing or malformed expiry is kept, unless the pack's default TTL applies to a *missing* annotation. | CEL expiry rule ([kyverno-policy-pack.md](kyverno-policy-pack.md) section 5.3); guard rule G-FMT. | K11, K12 |
| S8 | An owned object (`ownerReferences` non-empty) is never deleted by the Pod, ConfigMap, or Job packs. | CEL `standalone`. | K13, K20, E14 |
| S9 | Finalizers are never removed; terminating objects are skipped. | No pack touches `metadata.finalizers`; CEL `not-terminating`. | K14, E9 |
| S10 | Only a principal who may delete an object can make it eligible (opt-in, expiry, TTL label, protection removal). | Guard rule G-OPT with the API server `authorizer`; for labels that predate the guard, preflight check P8 before delete mode. | E7, E8, E16, `20-guard` R27 step |
| S11 | The cleanup controller holds delete permission only on the kinds of packs in `delete` mode. | One aggregated ClusterRole per delete-mode pack. | H4, E15, E11 |
| S12 | Uninstalling the chart deletes no user object. | Chart owns no user objects and no CRDs. | E12 |
| S13 | A malformed annotation on one object cannot stop cleanup for other objects. | Format and calendar check before `timestamp()` (R5); guard rule G-FMT. | K12, E13 |

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
| The `cleanup.kyverno.io/ttl` label becomes effective for the granted kinds in **every** namespace, without `resourceFilters` | The TTL manager watches every kind for which the controller has cluster-wide list, watch, and delete **[upstream, source]** `pkg/controllers/ttl/manager.go:210-219`; the TTL path does not read `resourceFilters`: **confirmed [local]** — after delete mode granted Pods, TTL-labeled Pods in `default` and `kube-public` were deleted (R16). Arming and disarming lag a mode change by up to one `ttlReconciliationInterval` (1m) (R26). | Guard rules G-OPT and G-NS cover `cleanup.kyverno.io/ttl`; preflight P8 finds labels that predate the guard. |
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
It matches the kinds of the enabled packs, plus Namespaces whenever a namespaced pack (Pods, ConfigMaps,
Jobs) is enabled: a Namespace's `janitor.io/protected` and `janitor.io/job-retention` labels decide for every
object in it (R38). Every rule applies to the Namespaces it matches. So with only namespaced packs enabled,
setting `janitor.io/ephemeral`, `janitor.io/expires-at`, or `cleanup.kyverno.io/ttl` on a Namespace also needs
`delete` permission on that Namespace, although no enabled pack reads those keys there. This is deliberate
(fail safe, principle P3): a CI identity that labels preview namespaces needs that permission anyway once
`previewEnvironments` is enabled ([gitops-integration.md](gitops-integration.md) section 5, R28).

### 4.1 Rules

| Rule | Trigger (CREATE or UPDATE) | Check | Default action |
|------|----------------------------|-------|----------------|
| G-OPT | `janitor.io/ephemeral`, `janitor.io/expires-at`, `janitor.io/job-retention`, or `cleanup.kyverno.io/ttl` is added or its value changes | The requesting principal may `delete` this object, checked with `authorizer`. For a Namespace object, the check sets the namespace attribute to the Namespace's own name, as the API server does for `DELETE namespaces/X`, so a RoleBinding inside X counts (R27). For `job-retention` (a Namespace label): the principal may delete `jobs` in that namespace (R22). The guard covers Namespaces whenever the Jobs pack is enabled. | Deny |
| G-FMT | `janitor.io/expires-at` present and changed | Value matches `YYYY-MM-DDTHH:MM:SSZ` and is a real calendar date (month length, leap years) with a year of at least 0001, the same check the policies use (R5, R37) | Deny |
| G-PROT | `janitor.io/protected=true` (label or annotation) is removed or changed, and neither form remains | The principal may `delete` this object | Deny |
| G-NS | Any lifecycle key above is added to an object in, or to, a protected namespace | Always fails; the message lists the protected namespaces and suggests `-n <namespace>` (R24) | Deny |
| G-MIX | An object with a manager marker is created in a Namespace labeled `janitor.io/ephemeral=true` | Always fails | Warn + Audit (configurable to Deny); own policy `kube-janitor-mixed-ownership`, rendered with `previewEnvironments` |

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

After `helm uninstall`, the API server can keep enforcing the deleted guard for a few seconds (cached
admission policies) **[local]** (R34).

### 4.2 Illustrative rule

**Implemented [local].** The chart's rule is `charts/kube-janitor-policies/templates/guard/validatingadmissionpolicy.yaml`;
end-to-end scenario `20-guard` exercises G-OPT, G-NS, G-FMT, G-PROT, and the namespace-scoped grant (R27). The excerpt
below is simplified: the chart adds the Namespace and `job-retention` cases described in section 4.1.

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
`tests/kyverno/run.sh` renders each policy with `helm template --show-only` (`ci/delete.yaml` for
delete suites, `ci/audit.yaml` for audit suites) and runs one delete suite and one audit suite per pack.

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
| K12 | Opt-in, `expires-at: garbage`, `2026-13-45T99:99:99Z`, `2026-02-30T00:00:00Z`, `2025-02-29`, `1900-02-29`, `2025-04-31` | kept, and no evaluation error; `2024-02-29` and `2025-12-31T23:59:59Z` are valid (deleted) |
| K13 | Opt-in Pod with an ownerReference | kept |
| K14 | Opt-in object with `deletionTimestamp` | kept |
| K15 | Opt-in, no annotation, age > defaultTTL | deleted |
| K16 | Opt-in, no annotation, age < defaultTTL | kept |
| K17 | Boundary: `expires-at` equal to "now" ± 1 minute is not testable deterministically in the CLI; covered by e2e timing | — |
| K18 | Job `Complete` or `Failed` long ago, standalone, in opted-in namespace | deleted |
| K19 | Job with `ttlSecondsAfterFinished` set | kept |
| K20 | Job owned by a CronJob | kept |
| K21 | Job running, suspended, or finished recently | kept |
| K22 | Job in a namespace without `janitor.io/job-retention=enabled` | kept |
| K5b | Opt-in, expired, annotation `janitor.io/protected=true` (no label), on a Pod and on a preview Namespace | kept (R40) |
| K6b | Opt-in, expired, in a namespace with the annotation `janitor.io/protected=true` | kept (R40) |
| K12i | Opt-in, `expires-at: 0000-01-01T00:00:00Z` | kept, no evaluation error (R37) |
| K24 | Selector bypass: the delete policy rendered without its opt-in selector line; an object that did not opt in (Pod, ConfigMap, Namespace, Job in a namespace without `job-retention`) | kept by CEL alone (R39) |
| K23 | Pack overlap: an object matched by two packs is deleted by neither twice (policy conflict); each kind belongs to exactly one pack | rendering check H5 |

**Audit/delete equivalence (A1).** For every fixture above, the audit `ValidatingPolicy` must report
`fail` exactly where the `DeletingPolicy` reports `pass`. A1 is defined on verdicts: where the delete
policy reports `skip` (selector exclusion), the audit policy may report `pass`, which also means "kept"
(observed for the Jobs pack, whose `objectSelector` has only a `NotIn` expression; R19). CI runs both
suites on the same fixtures.

K24 runs as the `*-no-selector` suites: `suite.env` sets `STRIP`, and `tests/kyverno/run.sh` removes that
line from the render and fails if the line is not there, so the suite cannot pass by accident.

**Policy files.** `kyverno test` rejects non-policy documents in a policy file (`policy type not
supported`), so `tests/kyverno/run.sh` renders each policy alone with `helm template --show-only` (R10).

**CEL validity.** Each policy compiles under `kyverno test`. A deliberately broken CEL expression in a
fixture policy must make the suite fail (negative control, run by `tests/kyverno/run.sh`).

### 6.2 Helm tests

`tests/helm/test_chart.py` (Python with PyYAML, calls `helm template`) implements:

| ID | Test |
|----|------|
| H1 | Default values render no object at all. |
| H2 | No rendered ClusterRole contains `delete` unless some pack has `mode: delete`. |
| H3 | `mode: audit` renders one `ValidatingPolicy` per enabled pack (Audit, background only, autogen off) and no `DeletingPolicy`. |
| H4 | Each delete-mode pack renders exactly its own aggregated ClusterRole with only its kind; only delete-mode packs get one. |
| H5 | No two packs target the same group/resource. |
| H6 | `values.schema.json` rejects unknown keys, bad durations, bad modes, bad namespace names; the gate rejects Deny+Warn and unimplemented features. |
| H7 | `mode: delete` with `metadataGuard.validationActions` lacking `Deny` fails the render. |
| H8 | Render fails without `policies.kyverno.io/v1` in capabilities, and passes with `--api-versions`. |
| H9 | `kubeVersion` constraint rejects 1.33 and accepts 1.34. |
| H10 | Golden files in `tests/golden/` match the render for every `ci/*.yaml`, compared as parsed YAML (Helm 3 and Helm 4 fold long strings differently; R18). |
| H11 | Every action in every workflow, and every image in chart templates, is pinned by SHA or digest. |
| H12 | The guard covers exactly the kinds of enabled packs, plus Namespaces when the Jobs pack is enabled. |
| H13 | G-MIX renders only with `previewEnvironments`, with `[Warn, Audit]` by default. |
| H14 | `PrometheusRule` renders only when enabled and a pack is in delete mode. |
| H15 | The built-in protected namespaces cannot be removed; extras are appended. |
| H16 | Every file in `examples/` parses. |
| H17 | Release metadata: release version, old chart deprecated, `artifacthub-repo.yml` without personal data. |
| H18 | Release workflow shape: tag pattern, package, push, sign, verify, Artifact Hub, Policies gate; no GoReleaser. |
| H19 | The guard covers Namespaces whenever a namespaced pack is enabled, in audit and delete mode (R38). |
| H20 | The chart renders as a subchart of a scratch umbrella chart whose `global` has other keys; its own `global` keys are still validated (R41). |
| H21 | `monitoring.prometheusRule.labels` reaches the `PrometheusRule`; the chart's own labels stay; non-string values are refused (R42). |
| H22 | Durations above `999999h` or `99999999m` are refused; the maximums render (R43). |
| H23 | NOTES describe each pack correctly and state whether the guard rejects or only warns (R44). |

All Helm tests run with Helm 3.22.1 and Helm 4.3.2. `helm lint` does not execute template `fail`
calls, so the gate is tested with `helm template` (R12).

### 6.3 Integration tests (Chainsaw on kind)

Environment: kind v0.33.0 with the digest-pinned node images of [compatibility.md](compatibility.md)
section 2.2, Kyverno chart 3.10.0 (`--set config.generateSuccessEvents=true`), and the pack installed from
the working tree with `ci/e2e.yaml` (schedule `*/1 * * * *`). Pattern from Kyverno's own conformance tests
**[upstream]**: apply, wait one schedule (`sleep 1m5s`), then assert with an `error:` step that a resource is gone.
Scenarios live in `tests/chainsaw/packs/` and run in order through `tests/e2e/run.sh`.

Lessons from the implementation [local]: Chainsaw's default script timeout is 5 s, so tests that run
scripts set `timeouts.exec` (R17). After a mode change, the driver waits for the TTL controller to
converge (R26). After `helm uninstall`, it waits until the API server stops enforcing the deleted
guard (R34).

| ID | Scenario (task requirement) | Steps | Pass criteria |
|----|-----------------------------|-------|---------------|
| E1 | Kyverno prerequisites met (1) | Run `hack/preflight.sh` | Exit 0; P1–P8 PASS (P6 may report WAIT within one schedule interval of install) |
| E2 | Authorized ephemeral Pod expires and is deleted (2) | Create Pods with opt-in and a past `expires-at`, and with opt-in and no annotation (default TTL 1m) | Both gone within two runs (the deletion metric is not asserted) |
| E3 | Protected Pod untouched (3) | Opt-in + expired Pod with `janitor.io/protected=true`; another in `default` (created before the guard) | Both present after two more runs |
| E4 | Preview Namespace cleaned only under an authorized policy (4) | (a) opted-in expired Namespace with `previewEnvironments` in `audit` → present and reported; (b) switch to `delete` → gone; (c) expired Namespace without opt-in → present | as stated |
| E5 | Unknown ownership not deleted (5) | Pod with no opt-in | present |
| E6 | GitOps-managed resource protected (6), simulated | Opt-in expired Pod with an Argo CD tracking annotation; another with Flux labels | present |
| E7 | Opt-in by an unauthorized principal is rejected | As a ServiceAccount with `patch` but not `delete` on Pods, add `janitor.io/ephemeral=true` | Admission denied with the G-OPT message |
| E8 | `cleanup.kyverno.io/ttl` by an unauthorized principal is rejected | Same identity, add `cleanup.kyverno.io/ttl=1m` | Denied |
| E9 | Finalizer respected | Opt-in expired ConfigMap with a foreign finalizer | Stays `Terminating` with the finalizer intact after two more runs; 0 policy errors, 0 dropped runs |
| E10 | Missing cleanup-controller permissions detected (7) | Pack in `delete` mode; remove its aggregated ClusterRole | `hack/preflight.sh` P4 FAIL; an expired opted-in Pod is kept; after `helm upgrade --reuse-values` re-creates the role, the Pod is deleted (the errors metric is not asserted) |
| E11 | Helm upgrade preserves behavior (8) | Install the chart from `origin/main` with ephemeralPods in delete mode; `helm upgrade --reuse-values` to the candidate | Same `enabled`/`mode`; only one `DeletingPolicy`; delete RBAC unchanged; E2 and E3 hold (`05-upgrade`) |
| E12 | Uninstall removes nothing unexpected (9) | Create unrelated objects and opted-in non-expired objects; `helm uninstall` | All user objects present; chart objects gone; SubjectAccessReview for the cleanup controller now denies `delete pods` |
| E13 | Malformed annotation does not stall cleanup | Before the chart (and its guard) is installed, create one Pod with `expires-at: 2026-02-30T00:00:00Z` and one valid expired Pod. Then install with every pack in delete mode. | Valid Pod deleted; no `kyverno_controller_drop_total{controller_name="deleting-controller"}` increase |
| E14 | Completed Job retention | In an opted-in namespace, create a standalone Job that completes at once, a Job with `ttlSecondsAfterFinished`, and a CronJob that creates Jobs. `ci/e2e.yaml` sets `retention: 1m`. | Standalone Job deleted; native-TTL Job and CronJob-owned Jobs kept |
| E15 | Delete RBAC follows delete-mode packs only | Install with `ephemeralPods` in `delete` mode and `temporaryConfigMaps` in `audit` mode. Create `SubjectAccessReview`s for the cleanup controller ServiceAccount. | `delete pods` allowed; `delete configmaps`, `delete namespaces`, `delete jobs` denied (on a cluster with no other grants). The driver switches values for this step and then restores `ci/e2e.yaml` (R14). |
| E16 | TTL-label arming is real and is guarded | (a) With every pack in `audit` mode, as cluster-admin, label a Pod in a non-protected namespace `cleanup.kyverno.io/ttl=1m`. (b) Switch to `delete` mode and label a second Pod the same way. (c) Try the same label on a Pod in `kube-system`. (d) Before (b), run `hack/preflight.sh --kinds pods` with TTL-labeled Pods in `default` and `kube-public`. | (a) Pod present after 3 minutes; `kyverno_ttl_controller_info` lists no Pods. (b) Pod deleted by the TTL controller; the metric lists Pods. (c) Denied by G-NS. (d) P8 FAIL for both. |
| G-MIX | Mixed ownership warning | Opt a namespace in (far-future expiry); create an Argo CD-marked ConfigMap in it | Created, with a `G-MIX` admission warning |

### 6.4 GitOps tests

| ID | Scenario | Needs real tool? | Job |
|----|----------|------------------|-----|
| GO1 | Marker exclusion for Argo CD and Flux objects | No — simulated with labels and annotations (E6) | `e2e` (every PR) |
| GO2 | Argo CD-managed opted-in expired objects are kept; no delete/recreate loop | Yes: Argo CD v3.6.3 | `gitops-e2e` (nightly); passed locally **[local]** |
| GO3 | ApplicationSet PR generator pattern B removes the namespace without kube-janitor | Yes: Argo CD + a reachable SCM API | **Not implemented in 0.1.0** (R32) |
| GO4 | Flux Kustomization objects kept; deleting the Kustomization with `prune: true` removes them | Yes: Flux v2.10.1 | `gitops-e2e`; passed locally **[local]** |
| GO5 | Install the chart through Argo CD (a) and Flux (b) ([gitops-integration.md](gitops-integration.md) section 7); render gate passes | Yes | `gitops-e2e`; both passed locally **[local]** (GO5b after R36) |
| GO6 | G-MIX warning appears when Argo CD syncs into an opted-in namespace | Yes: Argo CD | **Not run through Argo CD** (no stable place to assert admission warnings); covered without Argo CD by `tests/chainsaw/packs/14-mixed-ownership` (R32) |

## 7. Release validation gates

A release tag is published only when all of these hold on the release commit:

1. `policy-unit` (K1–K24 with K5b, K6b, K12i; A1; CEL negative control) passes. **Automated:** `release-policies.yml` runs the Policies workflow first.
2. `chart-render` (H1–H23) passes on Helm 3.22.1 and 4.3.2. **Automated** (same).
3. `e2e` (E1–E16, G-MIX) passes on Kubernetes 1.34, 1.35, 1.36, and 1.37. **Automated** (same).
4. `gitops-e2e` (GO2, GO4, GO5) passed on the release commit. **Manual check** before pushing the tag.
5. The changelog lists every change that widens deletion under `BREAKING-SAFETY`, and a maintainer approved it.
6. The pushed chart digest is signed and verified (`cosign verify`). **Automated** in `release-policies.yml`.
7. No unpinned action or image (H11).

## 8. Acceptance criteria for the test suite

- Every invariant S1–S13 maps to at least one passing negative test.
- Every v0.1 pack has positive and negative `kyverno test` cases and at least one end-to-end test.
- A test that expects deletion asserts the specific object is gone and that a neighbor control object is still present.
- No test depends on wall-clock timing finer than one schedule interval plus 5 seconds.
- `make test` (Go) keeps passing while the Go code exists. After kind runs, the drivers delete the cluster and prune Docker volumes.

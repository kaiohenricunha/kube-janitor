# Spec: Compatibility and installation preflight

**Status**: Implemented in kube-janitor-policies 0.1.0 (results: [kyverno-migration.md](../plans/kyverno-migration.md) §6)
**Date**: 2026-11-24
**Phase**: migration Phase 1 (see [kyverno-migration.md](../plans/kyverno-migration.md))
**Related**: [ADR-0002](../adr/0002-kyverno-native-cleanup.md), [kyverno-policy-pack.md](kyverno-policy-pack.md), [security-and-testing.md](security-and-testing.md)

---

## Summary

This spec defines what `kube-janitor-policies` needs from a cluster, which versions it supports,
how an installation detects an incompatible cluster, and how CI proves compatibility.

All facts were checked on 2026-11-24. Each claim has one of five tags. All six documents use them:

- **[upstream]** — stated by upstream docs, release notes, issues, or source code, with a reference.
- **[inferred]** — read from upstream source code or behavior, with no doc statement. Shown as **[upstream, inferred]** where the source is cited.
- **[local]** — reproduced in this session with a local command, which the text names.
- **[to-test]** — kube-janitor must prove it in its own CI. Nobody has tested it yet.
- **[unverified]** — no source confirms it. Do not rely on it.

**Provenance.** Research agents gathered the upstream facts from docs, release notes, and Go source at tag
`v1.20.0`. The author reproduced these locally: the Kyverno 3.10.0 chart pull and render (CRD versions,
cleanup-controller RBAC, aggregation labels, `resourceFilters`, event and report defaults, `admin`
aggregation); the state and titles of Kyverno issues #17638, #18248, #17884, #18813, #18160, and #18376
(`gh issue view`, all OPEN on 2026-11-24); and `pkg/controllers/deleting/controller.go` at `v1.20.0`
(line 290 passes `dryRun=false`; lines 391 and 404 write only `LastExecutionTime`).

## 1. Dependencies

### 1.1 Required

| Dependency | Requirement | Source |
|------------|-------------|--------|
| Kubernetes | 1.34 – 1.37 (tested range, section 2) | [upstream] Kyverno 1.20 support table, section 2 |
| Kyverno | **1.20.0 or later 1.20.x** (minimum supported) | section 3 |
| Kyverno cleanup controller | Enabled and running. It executes `DeletingPolicy` and the `cleanup.kyverno.io/ttl` label. | [upstream] "Cleanup controller (optional): The component responsible for DeletingPolicy and TTL cleanup." — https://kyverno.io/docs/installation/installation/ |
| CRD `deletingpolicies.policies.kyverno.io`, version `v1` served | Every policy in the pack uses `apiVersion: policies.kyverno.io/v1`. | [local] `helm template` of `kyverno-3.10.0.tgz`: `v1 served=True storage=False`, `v1beta1 served=True storage=True`, `v1alpha1 served=True deprecated=True` |
| `admissionregistration.k8s.io/v1` `ValidatingAdmissionPolicy` | Used by the metadata guard (see [security-and-testing.md](security-and-testing.md)). GA in Kubernetes 1.30. | [upstream] https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/ (`state="stable" for_k8s_version="v1.30"`) |
| Helm | 3.8 or later for OCI. Tested: 3.22.x and 4.3.x (section 5). | [upstream] OCI GA in Helm 3.8.0 — https://helm.sh/docs/topics/registries/ |

### 1.2 Required for audit mode

Audit mode is the default mode of an enabled pack ([kyverno-policy-pack.md](kyverno-policy-pack.md) section 7). It needs:

| Dependency | Requirement | Source |
|------------|-------------|--------|
| Kyverno reports controller | Enabled and running. It runs the background scan of the audit `ValidatingPolicy`. | [local] `reportsController.enabled: true` in Kyverno 3.10.0 values; `backgroundScanInterval: 1h` |
| CRD `validatingpolicies.policies.kyverno.io`, `v1` served | Audit policies use `apiVersion: policies.kyverno.io/v1`. | [local] rendered chart: `v1 served=True` |
| Policy report CRDs | Installed by the Kyverno chart (`wgpolicyk8s.io` by default; OpenReports is optional). | [upstream] Kyverno chart subcharts `crds` and `openreports` |

If the reports controller is disabled, audit mode produces no reports. The planned CLI preview
(`hack/preview.sh`) is not implemented in 0.1.0 (R35); run `kyverno apply` with the rendered policies instead.

### 1.3 Not required

| Component | Why the pack does not need it |
|-----------|-------------------------------|
| Kyverno admission webhooks for policies | The metadata guard uses the in-process Kubernetes `ValidatingAdmissionPolicy`. It works when the Kyverno admission controller is unavailable. The Kyverno admission controller is still required by Kyverno itself ([upstream] "Admission controller (required)" — installation doc). |
| Kyverno background controller | Used only for generate and mutate-existing rules. The pack has none. |
| Kyverno reports controller in `delete` mode | Deletion does not use policy reports. (Audit mode needs it; section 1.2.) |
| `PolicyException` (`policies.kyverno.io`) | [upstream] "PolicyExceptions are disabled by default" (exceptions guide). [upstream, inferred] The 1.20.0 cleanup controller does not register the exceptions flag, so it never loads exceptions (`cmd/cleanup-controller/main.go:116-129`, `fetch.go:58-63`); no doc or test confirms this. The pack does not rely on exceptions for safety. Exclusions are CEL conditions in each policy. |
| Argo CD, Flux | Optional. See [gitops-integration.md](gitops-integration.md). |
| A kube-janitor controller or image | The policy chart ships no workload. |

### 1.4 RBAC that the pack adds

The Kyverno cleanup controller cannot delete user resources out of the box:

- [local] In the rendered `kj-kyverno:cleanup-controller:core` ClusterRole, the only `delete` rules
  cover `validatingwebhookconfigurations`, `globalcontextentries`, and `updaterequests`.
- [upstream] "Kyverno cleanup-controller always requires RBAC permissions for deleting resources (even pods)" —
  https://kyverno.io/docs/policy-types/deleting-policy/

The aggregate ClusterRole selects extra roles by label:

- [upstream] `rbac.kyverno.io/aggregate-to-cleanup-controller: "true"` — https://kyverno.io/docs/installation/customization/
- [local] Rendered `aggregationRule.clusterRoleSelectors` of `kj-kyverno:cleanup-controller` contains that label.

The pack ships **one aggregated ClusterRole for each pack in delete mode**. It grants only the
`get`, `list`, `watch`, and `delete` verbs on only the kinds that pack targets. The pack never
modifies Kyverno's default roles, as upstream recommends:
[upstream] "it is highly recommended that default roles are not modified but new roles are used to extend them."

Aggregated ClusterRoles are cluster-wide. RBAC cannot limit the cleanup controller to some
namespaces. Policy conditions do that. See [security-and-testing.md](security-and-testing.md) section 3.

## 2. Supported version matrix

### 2.1 Kubernetes

| Kubernetes | Upstream status on 2026-11-24 | Kyverno 1.20 | kube-janitor-policies v0.1 |
|------------|-------------------------------|--------------|----------------------------|
| 1.37 | Supported, EOL 2027-10-28 | Supported | **Tested** (CI e2e) |
| 1.36 | Supported, EOL 2027-06-28 | Supported | **Tested** (CI e2e) |
| 1.35 | Supported, EOL 2027-02-28 | Supported | **Tested** (CI e2e) |
| 1.34 | **Upstream EOL 2026-10-27** | Supported | **Tested** (CI e2e; until Kyverno drops it) |
| ≤ 1.33 | EOL | Not supported | Not supported. The chart `kubeVersion` blocks install. |
| 1.38 | Planned 2026-12-16 | Not supported | Not supported until a Kyverno release supports it |

Sources:

- [upstream] Kyverno 1.20: "Kubernetes Versions Supported: v1.34 - v1.37" — https://kyverno.io/docs/installation/releases/ ; the 1.20 blog agrees.
- [upstream] Kubernetes support windows — https://kubernetes.io/releases/ and https://kubernetes.io/releases/patch-releases/ . The site data was last updated 2026-09-29. GitHub releases show newer patches (1.37.2, 1.36.6, 1.35.10, 1.34.13).
- [upstream] Kyverno's own CI for 1.20 uses kind images for v1.34.11, v1.35.8, v1.36.4, v1.37.0 (`.github/workflows/kubernetes-matrix.yaml` at tag v1.20.0).

**Decision.** The tested range is the intersection of:

1. the Kubernetes range supported by the minimum supported Kyverno minor, and
2. the Kubernetes minors that have a published kind node image.

Thus v0.1 keeps 1.34, which is upstream-EOL, because Kyverno 1.20 still supports it and
managed providers still run it. The README marks 1.34 as "upstream EOL".

### 2.2 kind node images for CI

[upstream] kind v0.33.0 (2026-08-26) publishes these images. kind requires the digest:
"You *must* use the `@sha256` digest" — https://github.com/kubernetes-sigs/kind/releases/tag/v0.33.0

```text
kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5
kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed
kindest/node:v1.35.8@sha256:07b2536e30b803ed61d1677a79df6115f798ce64c80f9e22f6ed45afd09323c0
kindest/node:v1.34.11@sha256:44e222ee2132dab25ff87301682f89eb82c7880ea3a1bf543bfe9708fd08d67d
```

`hack/install-tools.sh` installs kind v0.33.0 (pinned in `tests/versions.env`, checksum-verified) **[local]**.

## 3. Kyverno versions

### 3.1 Release facts

| Kyverno | Published | Helm chart | Kubernetes range | Source |
|---------|-----------|------------|------------------|--------|
| v1.20.0 | 2026-11-12 | 3.10.0 | 1.34 – 1.37 | [upstream] GitHub releases; https://kyverno.github.io/kyverno/index.yaml ; releases doc |
| v1.19.2 | 2026-10-20 | 3.9.2 | 1.33 – 1.35 | [upstream] https://release-1-19-0.kyverno.io/docs/installation/releases/ |
| v1.18.2 | 2026-07-10 | 3.8.2 | **[unverified]** (docs page is stale) | — |

- [upstream] "Kyverno v1.20.0 is installed with Helm chart version 3.10.0. Chart and Kyverno application versions are different" — installation doc.
- [local] `helm pull oci://ghcr.io/kyverno/charts/kyverno --version 3.10.0` succeeded:
  digest `sha256:290ff54d4b7d015ce2ffe5be84b60e2db5a59d8c5dcea772a8f6e32475c2eba2`, `appVersion: v1.20.0`, `kubeVersion: '>=1.25.0-0'`.
  The OCI location exists, but Kyverno documents only the HTTP repository `https://kyverno.github.io/kyverno/`.
- [upstream] Kyverno support policy: "approximately three (3) months of community patch support"; minor releases "Roughly every 3 months"; the schedule lists one supported release at a time — https://kyverno.io/docs/installation/releases/
- [upstream] The Kyverno CLI version equals the Kyverno version. Pinned install: download `kyverno-cli_v1.20.0_linux_x86_64.tar.gz` from the v1.20.0 GitHub release — https://kyverno.io/docs/subprojects/kyverno-cli/ (automated in `hack/install-tools.sh`).

### 3.2 Minimum supported Kyverno: 1.20.0

`policies.kyverno.io/v1` is served since 1.17 ([upstream] 1.17 blog: "promotion of CEL-based policy
types to v1", including `DeletingPolicy`). The pack still requires 1.20.0, for these reasons:

1. **The deleting controller honors `resourceFilters` only since 1.20.** [upstream] release note
   "fix(controllers): honor resourceFilters in cleanup and deleting controllers #17632".
   [local] The default filters exclude the `kyverno`, `kube-system`, `kube-public`, and
   `kube-node-lease` namespaces. This is a second, independent protection layer.
2. **Namespaced resources are paginated since 1.20.** [upstream] "fix(deleting): process namespaced resources with pagination #17263".
3. **TTL deletes use identity preconditions since 1.20.** [upstream] "fix(ttl): guard cleanup deletes with object identity preconditions #18149".
4. **Only the current minor gets upstream patches** (section 3.1).

Kyverno 1.17 – 1.19 may accept the manifests. They are **not supported** and are not tested.

### 3.3 Known upstream inconsistencies

| ID | Inconsistency | Impact on kube-janitor | Default |
|----|---------------|------------------------|---------|
| K1 | The 1.20 blog says the storage version of `policies.kyverno.io` moves to `v1` in 1.20. [local] The 1.20.0 chart still stores `v1beta1`. | None for the pack. It writes `v1`, which every 1.17+ release serves. | Write `v1`. Never write `v1beta1` or `v1alpha1`. |
| K2 | Legacy CRD removal is "planned for v1.21" (1.20 blog). Earlier posts said v1.20. | None. The pack uses no legacy type. | — |
| K3 | The Kyverno chart README says to use Argo CD `Replace`; the platform notes say to use `ServerSideApply=true` and avoid `Replace=true`. | Affects users who install Kyverno with Argo CD. | Document `ServerSideApply=true` for the Kyverno install; it is not this chart's concern. |
| K4 | Deletion success events: the `DeletingPolicy` docs show a `PolicyApplied` / `Resource Cleaned Up` event, but success events are sent only when `generateSuccessEvents: true` [upstream, inferred] (`pkg/event/controller.go:106-110`), and the chart default is `false` [local]. | Auditability. | Do not depend on Events. Use metrics and API audit logs (see [kyverno-policy-pack.md](kyverno-policy-pack.md) section 9). The bootstrap in section 4.4 turns success events on. |

## 4. Installation preflight

### 4.1 Requirements

- It must fail closed: if the pack cannot prove a prerequisite, it does not enable deletion.
- It must add no long-running component and no new image of our own.
- It must work under plain Helm, Argo CD, and Flux.

### 4.2 What a chart can and cannot detect at render time

| Signal | Plain `helm install` | `helm template` (CI, Argo CD) | Flux helm-controller | Can it tell 1.17 from 1.20? |
|--------|----------------------|-------------------------------|----------------------|-----------------------------|
| `.Capabilities.APIVersions.Has "policies.kyverno.io/v1"` (group-version form) | Live discovery | Client-only: built-in groups plus `--api-versions` ([upstream] helm_template doc). Argo CD passes the target cluster's API versions ([upstream] "By default, Argo CD uses the API versions of the target cluster"). With the group-version gate, an Argo CD v3.6.3 install of the chart rendered and synced **[local]** (GO5a, R34b). Whether Argo CD also passes Kind-level entries is still unknown and no longer matters. | Live discovery: a Flux v2.10.1 HelmRelease install passed the gate **[local]** (GO5b, R34b) | **No** — v1 is served in 1.17 – 1.20 |
| `.Capabilities.KubeVersion` / `Chart.yaml kubeVersion` | Live | Client-only fake value; Argo CD uses the target cluster's version | Live (the GO5b install on Kubernetes 1.37 passed the `>=1.34.0-0` constraint) **[local]** | n/a |
| `lookup` of the Kyverno Deployment image tag | Live | **Empty** ([upstream] "Helm is not supposed to contact the Kubernetes API Server during a helm template … operation"; Kyverno chart README: `helm template` … "never populate `lookup`") | Live **[unverified]** | Yes, but only where `lookup` works |

**Conclusion.** No render-time signal is reliable across all three paths for the Kyverno
*version*. The chart can reliably detect only "the `DeletingPolicy` v1 API exists" and the
Kubernetes version.

### 4.3 Mechanism

The preflight has two implemented layers and one planned layer. None adds a running component.

**Layer 1 — render-time API gate (always on).**

- `Chart.yaml` sets `kubeVersion: ">=1.34.0-0"`. There is no upper bound, because an upper bound blocks
  upgrades on new clusters. The README states the tested range.
- A template helper fails the render with a clear message when
  `.Capabilities.APIVersions.Has "policies.kyverno.io/v1"` is false. The message
  names the missing Kyverno CRDs and the fix. The group-version form is used because it is the form
  that every renderer populates; it proves that some `policies.kyverno.io/v1` kind is served, not
  `DeletingPolicy` specifically. Preflight check P3 closes that gap.
- Escape hatch for offline rendering: `helm template --api-versions policies.kyverno.io/v1`.
  There is no values flag to skip the gate. A values flag would be copied into GitOps repositories and
  would silently disable the check.
- `helm lint` does not execute template `fail` calls, so it passes even when the gate conditions are
  violated; the chart's `helm template` tests (H7, H8) are the real gate tests **[local]** (R12).

**Layer 2 — preflight script (on demand, no cluster component).**

`hack/preflight.sh` uses only `kubectl` and `jq` and the user's kubeconfig. It prints PASS, WARN, WAIT,
or FAIL for each check and exits non-zero on any FAIL:

| Check | How |
|-------|-----|
| P1 Kyverno cleanup controller Deployment exists and is Available | `kubectl get deploy -A -l app.kubernetes.io/component=cleanup-controller` and read `status.conditions` |
| P2 Kyverno version ≥ 1.20.0 | Parse the image tag of the P1 Deployment. A digest-only or custom image gives WARN, not PASS. |
| P3 `DeletingPolicy` v1 is served | `kubectl get --raw /apis/policies.kyverno.io/v1` lists `deletingpolicies` |
| P4 The cleanup controller can delete each kind that a pack in delete mode targets | Create an `authorization.k8s.io/v1` `SubjectAccessReview` for the cleanup controller ServiceAccount, for verbs `list` and `delete` on each kind. This uses `create subjectaccessreviews`, not impersonation. |
| P5 The metadata guard is active | `ValidatingAdmissionPolicy` and its binding exist. The binding `validationActions` contains `Deny`. |
| P6 The pack's delete-mode policies run | Each `kube-janitor-*` `DeletingPolicy` exists, and its `status.lastExecutionTime` is set and newer than two schedule intervals. `status.conditionStatus.ready` is never set in 1.20.0 [upstream] (#18813), so P6 must not read it. Right after install, P6 reports WAIT, not FAIL, until one interval has passed. |
| P7 Audit mode can report (only if a pack is in audit mode) | The reports controller Deployment (`app.kubernetes.io/component=reports-controller`) is Available. |
| P8 No pre-armed TTL labels (run before switching a pack to `delete`, `--kinds <kinds>`) | For each kind, list objects that already carry `cleanup.kyverno.io/ttl`. FAIL if any is in a protected namespace or carries `janitor.io/protected=true`; WARN for the rest and print them. The guard checks only additions and changes, so these labels become effective the moment delete RBAC is granted ([security-and-testing.md](security-and-testing.md) section 3.3). Proven in e2e: E16d **[local]**. |

**Layer 3 — Helm test hook: planned, not implemented in 0.1.0.** `preflight.helmTest.enabled: true` fails the
render (R25). The planned design: a `helm.sh/hook: test` Pod that runs the same script with a digest-pinned
`kubectl` image, a dedicated ServiceAccount, read-only RBAC plus `create subjectaccessreviews`. How Argo CD
treats Helm test hooks is **[unverified]**.

**Why there is no blocking install hook.** A `pre-install` Job would block Argo CD and Flux syncs
on transient failures, and [upstream] both tools let users skip hooks (Kyverno chart README:
"ArgoCD `Skip Hooks` sync option … skips the hook Job"). A blocking hook would give a false
sense of safety. Deletion safety must come from the policies being disabled by default
(see [kyverno-policy-pack.md](kyverno-policy-pack.md) section 5), not from a hook.

### 4.4 Bring your own Kyverno, or bootstrap it

The chart never installs Kyverno (G2 and N4 in [kyverno-policy-pack.md](kyverno-policy-pack.md)). Two paths are supported:

**Bring your own Kyverno (default).** The cluster already runs Kyverno 1.20.x. The operator checks it with
`hack/preflight.sh` (layer 2) before enabling a pack. Settings the pack depends on, all at Kyverno chart defaults:
`cleanupController.enabled: true`, `reportsController.enabled: true` (for audit mode), and the default
`config.resourceFilters`. Recommended, not required: `config.generateSuccessEvents: true` for a per-object
deletion trail (K4).

**Bootstrap (documented, not shipped).** For a cluster without Kyverno, the README documents a pinned install
with the upstream-documented HTTP repository:

```bash
helm repo add kyverno https://kyverno.github.io/kyverno/
helm install kyverno kyverno/kyverno --version 3.10.0 \
  --namespace kyverno --create-namespace \
  --set config.generateSuccessEvents=true
```

The OCI location `oci://ghcr.io/kyverno/charts/kyverno` also works [local] (the e2e suites use it) but is
not documented upstream. Installing Kyverno with Argo CD needs `ServerSideApply=true` (K3).

## 5. Helm and CLI requirements

| Tool | Requirement | Tested in CI | Notes |
|------|-------------|--------------|-------|
| Helm 4 | 4.3.x | Yes (`chart-render`, 4.3.2) | [upstream] Helm 4.0.0 released 2025-11-12; latest 4.3.2 (2026-11-13). Argo CD 3.5+ renders only with Helm 4. Helm 4 folds long strings differently from Helm 3, so golden files are compared as parsed YAML **[local]** (R18). |
| Helm 3 | ≥ 3.8 (OCI). Tested 3.22.x. | Yes, until 2027-02-10 | [upstream] Helm 3 security fixes end 2027-02-10 (https://helm.sh/blog/helm-v3-end-of-life/). Drop Helm 3 from CI after that date. |
| Kyverno CLI | Same version as the minimum Kyverno (v1.20.0) | Yes (`policy-unit`) | Used for `kyverno test` (see [security-and-testing.md](security-and-testing.md)). |
| kubectl | Within ±1 minor of the cluster | Yes (preflight, e2e) | — |
| kind | v0.33.0 | Yes (`e2e`, `gitops-e2e`) | Section 2.2. |
| Chainsaw | v0.2.15 (pinned in `tests/versions.env`, checksum-verified by `hack/install-tools.sh`) **[local]** (R13) | Yes (`e2e`) | Kyverno's own conformance tests use it for `DeletingPolicy` [upstream]. Default script timeout is 5 s; tests set `timeouts.exec` (R17). |
| Argo CD / Flux | v3.6.3 / v2.10.1 (pinned in `tests/versions.env`) | Nightly (`gitops-e2e`) | Argo CD v3.6.3 was released on 2026-11-24 (R33). |

Chart format: `apiVersion: v2`. [upstream] Helm 4: "v2 charts continue to work unchanged."

## 6. Upgrade assumptions

| Event | Expected behavior | Action |
|-------|-------------------|--------|
| Kyverno 1.20.x patch | No change. | Planned: a nightly job against the latest 1.20.x patch (section 8). |
| Kyverno 1.21 | Legacy CRDs removed; storage version may move to `v1`. The pack uses neither legacy kinds nor `v1beta1`. | Add 1.21 to the matrix before claiming support. Keep 1.20 for one more pack minor (N-1). |
| Kyverno removes `v1beta1` | No impact. | None. |
| Kubernetes 1.38 | Not supported until a Kyverno release supports it. | Follow Kyverno. |
| kube-janitor-policies upgrade | Helm updates `DeletingPolicy` objects in place. A pack that was disabled stays disabled. Value renames follow section 7. | Upgrade test E11 runs on every PR against the chart on `main` **[local]**. |
| Kyverno uninstalled while the pack is installed | The `DeletingPolicy` CRD and objects are removed with Kyverno's CRDs, or they stay inert. No deletion happens without the cleanup controller. | Document. |

## 7. Versioning and deprecation policy

- The chart follows SemVer. `version` and `appVersion` are equal. The chart has no image.
- Before 1.0.0, a minor release can change values. Each change is listed in `charts/kube-janitor-policies/CHANGELOG.md` with a migration note.
- A renamed or removed values key is deprecated for at least one minor release first. During that release, the
  old key still works and `NOTES.txt` prints a warning.
- A change that can **widen** what a policy deletes (new kind, looser default, longer selector) is a
  **minor** release, never a patch. It needs a `BREAKING-SAFETY` changelog entry.
- Each pack release states the Kyverno minors it is tested with. After Kyverno 1.21 ships, the pack supports
  the current Kyverno minor and the previous one (N-1), because upstream patches only the current minor for about three months.
- Policy object names are stable. A rename is a breaking change, because metrics labels and user dashboards use them.
- A release is published by pushing the tag `kube-janitor-policies-v<version>` that matches `Chart.yaml`
  (`.github/workflows/release-policies.yml`).

## 8. Compatibility test strategy

| Job | Trigger | Matrix | Gate |
|-----|---------|--------|------|
| `policy-unit` | Every PR that touches the chart, `tests/`, or `hack/` | Kyverno CLI v1.20.0 | Required |
| `chart-render` | Same | Helm 3.22.1 and 4.3.2; `helm template` with and without `--api-versions` | Required |
| `e2e` | Same | Kubernetes 1.34, 1.35, 1.36, 1.37 (kind v0.33.0 digests) × Kyverno chart 3.10.0 | Required; also the release gate (`workflow_call` from `release-policies.yml`) |
| `gitops-e2e` | Nightly and on demand (`.github/workflows/gitops-e2e.yml`) | Kubernetes 1.37 + Argo CD v3.6.3, and + Flux v2.10.1 | Release gate (GO2, GO4, GO5; GO3 and GO6 not run, R32) |
| `e2e-latest` | Planned: nightly | Latest Kyverno 1.20.x patch and latest kind | Not implemented in 0.1.0 |
| `e2e-next` | Planned: weekly, when a Kyverno RC exists | Kyverno next RC on Kubernetes 1.37 | Not implemented in 0.1.0 |

Pinned versions live in one file (`tests/versions.env`). The full test definitions are in
[security-and-testing.md](security-and-testing.md).

## 9. Could not verify

1. The Kubernetes range of Kyverno 1.18 (docs page is stale).
2. How Flux populates `lookup` (the render gate does not use it; Flux passes the gate, R34b).
3. How Argo CD handles Helm `test` hooks (the chart has no test hook in 0.1.0).
4. Whether Argo CD passes Kind-level entries in `.Capabilities.APIVersions` (the gate does not need them; Argo CD passes it, R34b).
5. No Kyverno statement on Helm 4 or on a minimum Helm version.
6. Kyverno does not document its OCI chart location, although the artifact exists.
7. Whether the first GHCR push makes the chart package private (R31).

Items K4 (success events) and the `PolicyException` question (section 1.3) rest on source reading only.
They are tagged **[upstream, inferred]** and the design does not depend on them.

# Changelog — kube-janitor-policies

Versioning and deprecation rules: docs/specs/compatibility.md section 7. Any change that can
widen what a policy deletes is listed under **BREAKING-SAFETY**.

## 0.1.0

First release. Requires Kyverno 1.20.x and Kubernetes 1.34–1.37.

- **Packs** (all disabled by default; an enabled pack starts in `audit` mode):
  `ephemeralPods`, `previewEnvironments`, `temporaryConfigMaps`, `completedJobsRetention`.
- **Metadata guard** (`ValidatingAdmissionPolicy`): only a principal allowed to delete an object
  may opt it in (`janitor.io/ephemeral`, `janitor.io/expires-at`, `cleanup.kyverno.io/ttl`,
  `janitor.io/job-retention`); no cleanup metadata in protected namespaces; strict expiry format.
- **G-MIX** warning when a GitOps- or Helm-managed object is created in an opted-in namespace.
- **Preflight** `hack/preflight.sh` (checks P1–P8), optional `PrometheusRule` (with
  `monitoring.prometheusRule.labels` for a Prometheus Operator `ruleSelector`).
- **Pre-release review fixes** (docs/plans/kyverno-migration.md §6.5):
  - An expiry in year 0000 is rejected by the policies and the guard (it made every run fail).
  - The guard protects a Namespace's `janitor.io/protected` label whenever a namespaced pack is enabled.
    All its rules then apply to Namespaces: lifecycle keys on a Namespace need delete permission on it.
  - Every policy checks the opt-in again in CEL, not only in its selectors.
  - The annotation `janitor.io/protected=true` (the deprecated controller's default form) protects too.
    A custom controller annotation key is not honored.
  - `global` accepts a parent chart's keys but refuses near-misses of its own (`extraProtectedNamespace`);
    durations are limited to `999999h` / `99999999m`.
- **BREAKING-SAFETY:** none (first release).
- Known upstream limitations (Kyverno 1.20.0): no `DeletingPolicy` dry-run (#17638),
  Ready never set (#18813, see examples/argocd), no UID precondition on DELETE (#18160).

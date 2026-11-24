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
- **Preflight** `hack/preflight.sh` (checks P1–P8), optional `PrometheusRule`.
- **BREAKING-SAFETY:** none (first release).
- Known upstream limitations (Kyverno 1.20.0): no `DeletingPolicy` dry-run (#17638),
  Ready never set (#18813, see examples/argocd), no UID precondition on DELETE (#18160).

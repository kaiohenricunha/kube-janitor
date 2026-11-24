# Examples

Copy and adapt these files. They follow `docs/specs/gitops-integration.md`.
They are checked for YAML syntax in CI. The GitOps flows themselves are tested by the nightly
`gitops-e2e` job (Phase 3), not by these files directly.

| Path | What it shows |
|------|---------------|
| `argocd/application.yaml` | Install `kube-janitor-policies` with Argo CD, pinned version, one pack in audit mode |
| `argocd/applicationset-previews.yaml` | Pattern B: PR previews owned by Argo CD (kube-janitor does nothing) |
| `argocd/argocd-cm-deletingpolicy-health.yaml` | Health check for `DeletingPolicy` (Kyverno 1.20 never sets Ready, #18813) |
| `flux/helmrelease.yaml` | Install with Flux from OCI, cosign verification |
| `github-actions/preview-namespace.yml` | Pattern A: CI-owned preview namespace with expiry, and expiry on PR close |
| `gitlab-ci/preview-namespace.gitlab-ci.yml` | Pattern A for GitLab CI |

The CI identity that labels a namespace `janitor.io/ephemeral=true` must be allowed to delete that
namespace. Otherwise the metadata guard (rule G-OPT) rejects the label.

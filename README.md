# kube-janitor

**kube-janitor-policies** removes Kubernetes resources that someone explicitly marked as temporary,
after their expiry. It is a Helm chart of [Kyverno](https://kyverno.io) `DeletingPolicy` resources:
no controller, no image, no CRD of its own.

- **Nothing is deleted by default.** Every pack is disabled. An enabled pack starts in **audit** mode,
  which only reports what it would delete.
- **Opt-in, not guesswork.** The chart never deletes a resource because it looks old or unused.
- **Safe with GitOps.** Objects managed by Argo CD, Flux, or Helm are never deleted directly.

Design: [ADR-0002](docs/adr/0002-kyverno-native-cleanup.md) ·
[policy pack spec](docs/specs/kyverno-policy-pack.md) ·
[compatibility](docs/specs/compatibility.md) ·
[GitOps](docs/specs/gitops-integration.md) ·
[safety and tests](docs/specs/security-and-testing.md)

## Requirements

| Component | Version |
|-----------|---------|
| Kyverno | 1.20.x, with the cleanup controller (default) and, for audit mode, the reports controller (default) |
| Kubernetes | 1.34 – 1.37 (1.34 is upstream end-of-life but still supported by Kyverno 1.20) |
| Helm | 3.8+ (tested 3.22 and 4.3) |

No Kyverno yet? Install it first (pinned):

```bash
helm repo add kyverno https://kyverno.github.io/kyverno/
helm install kyverno kyverno/kyverno --version 3.10.0 -n kyverno --create-namespace \
  --set config.generateSuccessEvents=true
```

## Quick start

```bash
# 1. Install. All packs are disabled: this creates no policy, no RBAC, no admission rule.
helm install kube-janitor-policies oci://ghcr.io/kaiohenricunha/charts/kube-janitor-policies \
  --version 0.1.0 -n kube-janitor --create-namespace

# 2. Enable a pack in audit mode, and read what it would delete.
helm upgrade kube-janitor-policies oci://ghcr.io/kaiohenricunha/charts/kube-janitor-policies \
  --version 0.1.0 -n kube-janitor --reuse-values --set packs.ephemeralPods.enabled=true
kubectl get policyreports -A

# 3. Check the cluster, then switch the pack to delete mode.
hack/preflight.sh --kinds pods
helm upgrade ... --reuse-values --set packs.ephemeralPods.mode=delete
```

Verify the chart signature (keyless cosign):

```bash
cosign verify ghcr.io/kaiohenricunha/charts/kube-janitor-policies:0.1.0 \
  --certificate-identity-regexp 'https://github.com/kaiohenricunha/kube-janitor/.github/workflows/release-policies.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Packs

| Values key | Deletes | Selection | Default lifetime |
|------------|---------|-----------|------------------|
| `ephemeralPods` | standalone Pods | label `janitor.io/ephemeral=true` on the Pod | 24h |
| `previewEnvironments` | Namespaces (with everything in them) | label `janitor.io/ephemeral=true` on the Namespace | 72h |
| `temporaryConfigMaps` | standalone ConfigMaps | label `janitor.io/ephemeral=true` on the ConfigMap | 168h |
| `completedJobsRetention` | finished standalone Jobs without `ttlSecondsAfterFinished` | label `janitor.io/job-retention=enabled` on the Namespace | 168h after the Job finished |

Each pack has `enabled` (default `false`), `mode` (`audit` | `delete`, default `audit`), and its lifetime.
See [values.yaml](charts/kube-janitor-policies/values.yaml).

## Marking objects

```bash
# A debug Pod that is deleted 8 hours from now. Use a namespace that is not protected.
kubectl run debug-$USER -n sandbox --image=busybox -l janitor.io/ephemeral=true -- sleep infinity
kubectl annotate pod debug-$USER -n sandbox janitor.io/expires-at="$(date -u -d '+8 hours' +%Y-%m-%dT%H:%M:%SZ)"

# Never delete this object.
kubectl label configmap important -n sandbox janitor.io/protected=true
```

| Key | Kind | Meaning |
|-----|------|---------|
| `janitor.io/ephemeral: "true"` | label | Opt-in: the object may be deleted after expiry. |
| `janitor.io/expires-at` | annotation | Expiry, UTC, exactly `YYYY-MM-DDTHH:MM:SSZ`. Without it: creation time + the pack's lifetime. |
| `janitor.io/protected: "true"` | label | Veto on the object or its namespace. |
| `janitor.io/job-retention: enabled` | label (Namespace) | Opt-in for `completedJobsRetention`. |

## Safety model

- **Protected namespaces:** `kube-system`, `kube-public`, `kube-node-lease`, `default`, and the Kyverno namespace.
  Values can add names (`global.extraProtectedNamespaces`) but cannot remove these.
- **Metadata guard** (a Kubernetes `ValidatingAdmissionPolicy`, rendered when a pack is enabled):
  only a principal that may delete an object may opt it in or set its expiry; cleanup labels are
  rejected in protected namespaces; malformed expiry dates are rejected.
- **Kyverno's own `cleanup.kyverno.io/ttl` label:** delete mode grants the Kyverno cleanup controller
  delete permission on a kind, and that also arms Kyverno's TTL label for that kind in every namespace,
  without Kyverno's namespace filters (confirmed in our end-to-end tests). The guard covers that label
  too; keep `metadataGuard.validationActions` at `Deny` (the chart refuses otherwise), and run
  `hack/preflight.sh` (check P8) before you switch to delete mode.
- **GitOps:** objects with Argo CD, Flux, or Helm ownership markers are never deleted. Remove them
  through their source of truth. See [examples/](examples/).
- **Preview namespaces:** the identity that labels a namespace `janitor.io/ephemeral=true` must be allowed
  to delete that namespace.

## Operations

| Task | How |
|------|-----|
| Check prerequisites | `hack/preflight.sh` (checks P1–P8; exit 1 on any FAIL) |
| See what would be deleted | audit mode → `kubectl get policyreports,clusterpolicyreports -A` |
| Alerts | `--set monitoring.prometheusRule.enabled=true` (Prometheus Operator) |
| Emergency stop | `helm upgrade ... --reuse-values --set packs.<pack>.mode=audit` |
| Uninstall | `helm uninstall` removes the policies, RBAC, and guard. It deletes no user object. |
| Argo CD shows `DeletingPolicy` Degraded | Kyverno 1.20 never sets Ready (kyverno#18813): apply [examples/argocd/argocd-cm-deletingpolicy-health.yaml](examples/argocd/argocd-cm-deletingpolicy-health.yaml) |

## Development

```bash
hack/install-tools.sh               # pinned Kyverno CLI, kind, Chainsaw into ./bin
tests/kyverno/run.sh                # policy unit tests (kyverno test)
python3 tests/helm/test_chart.py    # Helm render tests
tests/e2e/run.sh                    # kind + Kyverno end-to-end (about 30 minutes)
tests/gitops/run.sh                 # Argo CD + Flux end-to-end (needs the commit pushed)
```

Releases: push the tag `kube-janitor-policies-v<version>` that matches `charts/kube-janitor-policies/Chart.yaml`;
[release-policies.yml](.github/workflows/release-policies.yml) tests, publishes, and signs the chart.

## The deprecated Go controller

The original controller (`cmd/manager`, `charts/kube-janitor`) is **deprecated and frozen in report-only mode**.
An audit found defects in its deletion path ([migration plan, section 2](docs/plans/kyverno-migration.md#2-audit-of-the-current-go-controller)),
so the binary refuses to start with `--dry-run=false`. Its resolver code may become an optional,
report-only analyzer later ([migration plan, Phase 4](docs/plans/kyverno-migration.md#phase-4--optional-report-only-analyzer-remove-the-controller)).

```bash
make test   # Go unit tests
make lint   # golangci-lint
```

## License

Apache License 2.0 — see [LICENSE](LICENSE).

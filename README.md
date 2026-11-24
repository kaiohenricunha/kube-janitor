# kube-janitor

> [!WARNING]
> **This controller is deprecated and frozen in report-only mode.** An audit found defects in its
> deletion path ([migration plan, section 2](docs/plans/kyverno-migration.md#2-audit-of-the-current-go-controller)),
> so the binary now refuses to start with `--dry-run=false`.
> kube-janitor is moving to **kube-janitor-policies**, a Helm chart of Kyverno `DeletingPolicy`
> resources. See [ADR-0002](docs/adr/0002-kyverno-native-cleanup.md) and the
> [design spec](docs/specs/kyverno-policy-pack.md). The policy chart is under development and not yet published.

A Kubernetes cleanup controller that continuously classifies resources and applies
policy-driven cleanup with strong safety controls.

## What it does

kube-janitor watches your cluster resources and:
- **Classifies** each resource as active, expired, orphaned, abandoned, protected, or blocked
- **Evaluates** JanitorPolicy CRDs to determine what action to take
- **Reports** findings as Kubernetes Events, structured logs, and Prometheus metrics
- ~~**Deletes** resources only when confidence is high, action mode is `Delete`, and global dry-run is disabled~~ — disabled: the controller refuses `--dry-run=false` (ADR-0002)

**kube-janitor runs only in dry-run mode. It never deletes anything.**

## Supported resource kinds (Phase 1)

| Kind           | TTL | Expires-At | Orphan | PR Namespace |
|----------------|-----|------------|--------|--------------|
| Namespace      | ✓   | ✓          | ✓      | ✓            |
| ConfigMap      | ✓   | ✓          | ✓      | -            |

## Quick start

### Apply a policy

```yaml
apiVersion: janitor.io/v1alpha1
kind: JanitorPolicy
metadata:
  name: preview-namespace-cleanup
spec:
  selector:
    kinds:
      - group: ""
        version: v1
        kind: Namespace
    namespaceSelector:
      matchLabels:
        env: preview
  ttl:
    default: 72h
    annotation: "janitor.io/ttl"
  prNamespace:
    enabled: true
    labelKey: "janitor.io/pr"
    closedTTL: 1h
  action:
    mode: DryRun   # safe default — change to Delete to enable cleanup
```

### Mark a resource as protected

```bash
kubectl annotate namespace production janitor.io/protected=true
```

### Override TTL per resource

```bash
kubectl annotate configmap my-config janitor.io/ttl=48h
kubectl annotate namespace feature-x janitor.io/expires-at=2025-12-31T23:59:00Z
```

## Architecture

Classification strategy chain (priority order — first match wins):
1. `ProtectionStrategy` — protected annotation/label/namespace?
2. `FinalizerStrategy` — blocking finalizers?
3. `TTLStrategy` — TTL annotation elapsed?
4. `ExpiresAtStrategy` — expires-at timestamp past?
5. `OwnerRefStrategy` — live ownerReference exists?
6. `ResolverStrategy` — any resolver finds a live reference?

**Safety controls:**
- `--dry-run=true` is the only accepted value. The binary exits with an error on `--dry-run=false`.
- Confidence ≥ 0.9 required for auto-deletion.
- Protected namespace blocklist: `kube-system`, `kube-public`, `default`, `kube-node-lease`.
- Pre-flight check before every deletion: re-GET object, verify UID, check finalizers.
- Foreign finalizers are never removed.

## Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | `true` | Must be `true`; `false` is refused (ADR-0002) |
| `--scan-interval` | `5m` | How often to re-scan all resources for TTL expiry |
| `--metrics-bind-address` | `:8080` | Prometheus metrics endpoint |
| `--health-probe-bind-address` | `:8081` | Health/readiness probe endpoint |
| `--leader-elect` | `false` | Enable leader election for HA |
| `--otel-endpoint` | `""` | OpenTelemetry collector endpoint (empty = disabled) |

## Observability

| Metric | Type | Labels |
|--------|------|--------|
| `janitor_resources_scanned_total` | Counter | `kind` |
| `janitor_resources_classified_total` | Counter | `kind`, `class` |
| `janitor_actions_total` | Counter | `kind`, `action`, `dry_run` |
| `janitor_reconcile_duration_seconds` | Histogram | `controller` |
| `janitor_reconcile_errors_total` | Counter | `controller`, `kind` |
| `janitor_policy_evaluations_total` | Counter | `policy`, `action` |

## Development

```bash
make tools        # install controller-gen, golangci-lint, setup-envtest
make test         # unit tests with race detector
make lint         # golangci-lint
make run          # run locally against current cluster (dry-run by default)
make docker-build # build container image
```

## License

Apache License 2.0 — see [LICENSE](LICENSE).
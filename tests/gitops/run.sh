#!/usr/bin/env bash
# GitOps end-to-end tests (docs/specs/security-and-testing.md section 6.4) on kind with real
# Argo CD and Flux. Argo CD and Flux pull this repository from GitHub at REVISION, so the
# commit must be pushed.
#
#   GO5 install the chart through Argo CD (render gate with Argo's API versions; health override
#       from examples/argocd) and through Flux (HelmRelease from a GitRepository)
#   GO2 opted-in, expired objects managed by Argo CD are never deleted: no delete/recreate loop
#   GO4 the same for a Flux Kustomization; deleting the Kustomization prunes them
#   GO3 (ApplicationSet PR generator) and GO6 (G-MIX warning through Argo CD) are NOT run:
#       GO3 needs a reachable SCM API, and Argo CD does not surface admission warnings in a
#       stable place to assert on. GO6 is covered without Argo CD by tests/chainsaw/packs/14.
#
# Env: REPO_URL (default the GitHub repository), REVISION (default HEAD), K8S_IMAGE,
#      KEEP_CLUSTER=1, CLUSTER (default kjp-gitops).
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
. "$root/tests/versions.env"
export PATH="$root/bin:$PATH"
repo_url="${REPO_URL:-https://github.com/kaiohenricunha/kube-janitor.git}"
revision="${REVISION:-$(git -C "$root" rev-parse HEAD)}"
cluster="${CLUSTER:-kjp-gitops}"
image="${K8S_IMAGE:-$KIND_NODE_1_37}"
work="$(mktemp -d)"
export KUBECONFIG="$work/kubeconfig"

log() { printf '\n=== %s\n' "$*"; }
fail() { echo "FAILED: $*"; exit 1; }
cleanup() {
  if [ "${KEEP_CLUSTER:-0}" != "1" ]; then
    kind delete cluster --name "$cluster" >/dev/null 2>&1 || true
    docker volume prune -f >/dev/null 2>&1 || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT

wait_for() { # <timeout seconds> <description> <command...>
  local timeout="$1" what="$2" waited=0; shift 2
  until "$@" >/dev/null 2>&1; do
    sleep 5; waited=$((waited + 5))
    [ "$waited" -lt "$timeout" ] || fail "timed out after ${timeout}s waiting for: $what"
  done
}
app_ok() { # <app> -> Synced and Healthy
  [ "$(kubectl -n argocd get application "$1" -o jsonpath='{.status.sync.status}/{.status.health.status}')" = "Synced/Healthy" ]
}
uids() { kubectl -n "$1" get pod,configmap -l janitor.io/ephemeral=true -o jsonpath='{range .items[*]}{.kind}/{.metadata.name}={.metadata.uid}{"\n"}{end}' | sort; }
dpol_count() { kubectl get deletingpolicies -l app.kubernetes.io/name=kube-janitor-policies -o name 2>/dev/null | wc -l; }

log "revision $revision from $repo_url"
git -C "$root" fetch --quiet "$repo_url" "$revision" 2>/dev/null || echo "note: could not confirm $revision on the remote; Argo CD and Flux need it pushed"

log "cluster $cluster ($image) and Kyverno $KYVERNO_CHART_VERSION"
kind get clusters 2>/dev/null | grep -qx "$cluster" || kind create cluster --name "$cluster" --image "$image" --wait 180s
kind get kubeconfig --name "$cluster" > "$KUBECONFIG"
helm status kyverno -n kyverno >/dev/null 2>&1 || helm install kyverno oci://ghcr.io/kyverno/charts/kyverno \
  --version "$KYVERNO_CHART_VERSION" -n kyverno --create-namespace --wait --timeout 10m
kubectl -n kyverno rollout status deploy --timeout=5m

log "Argo CD $ARGOCD_VERSION"
kubectl create namespace argocd --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -n argocd --server-side --force-conflicts \
  -f "https://raw.githubusercontent.com/argoproj/argo-cd/$ARGOCD_VERSION/manifests/install.yaml" >/dev/null
kubectl -n argocd rollout status deploy --timeout=8m
kubectl -n argocd rollout status statefulset argocd-application-controller --timeout=8m
# Health override for DeletingPolicy (Kyverno #18813), exactly as shipped in examples/.
kubectl -n argocd patch configmap argocd-cm --type merge \
  --patch-file <(python3 -c 'import sys,yaml,json; d=yaml.safe_load(open(sys.argv[1])); print(json.dumps({"data": d["data"]}))' \
  "$root/examples/argocd/argocd-cm-deletingpolicy-health.yaml")

log "GO5a: install the chart through Argo CD"
kubectl apply -f - <<EOF
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata: {name: kjp, namespace: argocd}
spec:
  project: default
  source:
    repoURL: $repo_url
    targetRevision: $revision
    path: charts/kube-janitor-policies
    helm: {valueFiles: [ci/e2e.yaml]}
  destination: {server: https://kubernetes.default.svc, namespace: kube-janitor}
  syncPolicy:
    automated: {prune: true, selfHeal: true}
    syncOptions: [CreateNamespace=true, ServerSideApply=true]
EOF
wait_for 480 "Argo CD app kjp Synced/Healthy" app_ok kjp
[ "$(dpol_count)" -eq 4 ] || fail "GO5a: expected 4 DeletingPolicies, got $(dpol_count)"
echo "GO5a PASS: chart installed by Argo CD (render gate passed with Argo's API versions); app Synced/Healthy"

log "GO2: Argo CD-managed opted-in expired objects are kept (no delete/recreate loop)"
kubectl apply -f - <<EOF
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata: {name: go2, namespace: argocd}
spec:
  project: default
  source: {repoURL: $repo_url, targetRevision: $revision, path: tests/gitops/fixtures/argo-app}
  destination: {server: https://kubernetes.default.svc, namespace: go2-demo}
  syncPolicy:
    automated: {prune: true, selfHeal: true}
    syncOptions: [CreateNamespace=true]
EOF
wait_for 300 "Argo CD app go2 Synced/Healthy" app_ok go2
before="$(uids go2-demo)"; echo "$before"
[ "$(wc -l <<<"$before")" -eq 2 ] || fail "GO2: expected 2 objects"
kubectl -n go2-demo get configmap go2-config -o jsonpath='{.metadata.annotations.argocd\.argoproj\.io/tracking-id}{"\n"}'
sleep 150   # at least two runs of the one-minute schedule (ci/e2e.yaml)
after="$(uids go2-demo)"
[ "$before" = "$after" ] || { echo "$after"; fail "GO2: objects were deleted and recreated"; }
echo "GO2 PASS: same UIDs after 150s; app still $(kubectl -n argocd get application go2 -o jsonpath='{.status.sync.status}')"

log "remove the Argo CD installation of the chart before the Flux part"
kubectl -n argocd patch application kjp --type merge -p '{"metadata":{"finalizers":["resources-finalizer.argocd.argoproj.io"]}}'
kubectl -n argocd delete application kjp --wait=true --timeout=5m
wait_for 180 "DeletingPolicies removed" test "$(dpol_count)" -eq 0

log "Flux $FLUX_VERSION"
kubectl apply -f "https://github.com/fluxcd/flux2/releases/download/$FLUX_VERSION/install.yaml" >/dev/null
kubectl -n flux-system rollout status deploy --timeout=5m
kubectl apply -f - <<EOF
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata: {name: kjp-repo, namespace: flux-system}
spec:
  interval: 10m
  url: $repo_url
  ref: {commit: $revision}
EOF
wait_for 180 "GitRepository ready" kubectl -n flux-system wait gitrepository/kjp-repo --for=condition=Ready --timeout=5s

log "GO4: Flux-managed opted-in expired objects are kept; pruning removes them"
kubectl apply -f - <<EOF
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata: {name: go4, namespace: flux-system}
spec:
  interval: 1m
  path: ./tests/gitops/fixtures/flux-app
  prune: true
  sourceRef: {kind: GitRepository, name: kjp-repo}
EOF
wait_for 180 "Kustomization go4 ready" kubectl -n flux-system wait kustomization/go4 --for=condition=Ready --timeout=5s
before="$(uids go4-demo)"; echo "$before"
[ "$(wc -l <<<"$before")" -eq 2 ] || fail "GO4: expected 2 objects"
sleep 150
after="$(uids go4-demo)"
[ "$before" = "$after" ] || { echo "$after"; fail "GO4: objects were deleted and recreated"; }
kubectl -n flux-system delete kustomization go4 --wait=true --timeout=3m
wait_for 120 "Flux pruned go4 objects" test "$(kubectl -n go4-demo get pod,configmap -l janitor.io/ephemeral=true -o name 2>/dev/null | wc -l)" -eq 0
echo "GO4 PASS: same UIDs after 150s; deleting the Kustomization pruned them"

log "GO5b: install the chart through Flux"
kubectl apply -f - <<EOF
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata: {name: kjp, namespace: flux-system}
spec:
  interval: 10m
  targetNamespace: kube-janitor
  install: {createNamespace: true}
  chart:
    spec:
      chart: ./charts/kube-janitor-policies
      sourceRef: {kind: GitRepository, name: kjp-repo}
      # Flux valuesFiles REPLACE the chart's values.yaml; list it first (R36).
      valuesFiles: [./charts/kube-janitor-policies/values.yaml, ./charts/kube-janitor-policies/ci/e2e.yaml]
EOF
wait_for 300 "HelmRelease kjp ready" kubectl -n flux-system wait helmrelease/kjp --for=condition=Ready --timeout=5s
[ "$(dpol_count)" -eq 4 ] || fail "GO5b: expected 4 DeletingPolicies, got $(dpol_count)"
echo "GO5b PASS: chart installed by Flux helm-controller (render gate passed)"

log "GitOps e2e passed: GO2, GO4, GO5 (GO3 and GO6 not run; see the header)"

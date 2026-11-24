#!/usr/bin/env bash
# End-to-end tests for kube-janitor-policies on kind (docs/specs/security-and-testing.md 6.3).
# Order matters: audit mode -> delete mode -> guard -> TTL arming -> RBAC scope -> preflight
# -> missing RBAC -> uninstall. Chainsaw runs the assertions; this driver runs the Helm and
# preflight steps between them.
#
# Env: K8S_IMAGE (kind node image, digest-pinned), KEEP_CLUSTER=1 to keep the cluster,
#      CLUSTER (default kjp-e2e). Tool versions: tests/versions.env.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
. "$root/tests/versions.env"
export PATH="$root/bin:$PATH"
export REPO_ROOT="$root"
cluster="${CLUSTER:-kjp-e2e}"
image="${K8S_IMAGE:-$KIND_NODE_1_37}"
chart="$root/charts/kube-janitor-policies"
suite="$root/tests/chainsaw/ephemeral-pods"
work="$(mktemp -d)"
export KUBECONFIG="$work/kubeconfig"

log() { printf '\n=== %s\n' "$*"; }
cleanup() {
  if [ "${KEEP_CLUSTER:-0}" != "1" ]; then
    kind delete cluster --name "$cluster" >/dev/null 2>&1 || true
    docker volume prune -f >/dev/null 2>&1 || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT

metric() { # <name> <label filter> -> summed value (0 when absent)
  kubectl get --raw "/api/v1/namespaces/kyverno/services/kyverno-cleanup-controller-metrics:8000/proxy/metrics" 2>/dev/null \
    | awk -v n="$1" -v f="$2" '$1 ~ "^"n"(\\{|$)" && index($0, f) { s += $NF } END { printf "%d\n", s }'
}
run_chainsaw() { chainsaw test --test-dir "$suite/$1" --parallel 1 --no-color; }

log "cluster $cluster ($image)"
if ! kind get clusters 2>/dev/null | grep -qx "$cluster"; then
  kind create cluster --name "$cluster" --image "$image" --wait 180s
fi
kind get kubeconfig --name "$cluster" > "$KUBECONFIG"
kubectl version | grep -i server

log "Kyverno chart $KYVERNO_CHART_VERSION"
if ! helm status kyverno -n kyverno >/dev/null 2>&1; then
  helm install kyverno oci://ghcr.io/kyverno/charts/kyverno --version "$KYVERNO_CHART_VERSION" \
    -n kyverno --create-namespace --set config.generateSuccessEvents=true --wait --timeout 10m
fi
kubectl -n kyverno rollout status deploy --timeout=5m

log "objects created before the chart and its guard exist"
kubectl apply -f "$suite/fixtures/pre-install.yaml"

log "install the chart in audit mode"
helm upgrade --install kjp "$chart" -n kube-janitor --create-namespace -f "$chart/ci/audit.yaml" --wait

log "E16d: preflight before delete mode finds pre-armed TTL labels (expect exit 1, P8 FAIL)"
set +e; hack_out="$("$root/hack/preflight.sh" --kinds pods)"; rc=$?; set -e
echo "$hack_out"
[ "$rc" -eq 1 ] && grep -q "P8   FAIL pods default/e16d-ttl-default" <<<"$hack_out" \
  && grep -q "P8   FAIL pods kube-public/e16e-ttl-kube-public" <<<"$hack_out" \
  || { echo "E16d FAILED"; exit 1; }

ttl_watches_pods() {
  kubectl get --raw "/api/v1/namespaces/kyverno/services/kyverno-cleanup-controller-metrics:8000/proxy/metrics" 2>/dev/null \
    | grep -c '^kyverno_ttl_controller_info{.*resource_resource="pods"' || true
}

log "audit mode"
echo "TTL controller watches pods in audit mode: $(ttl_watches_pods) (expect 0)"
[ "$(ttl_watches_pods)" -eq 0 ] || { echo "E16a FAILED: TTL label armed without delete mode"; exit 1; }
run_chainsaw 01-audit-mode

drop_before="$(metric kyverno_controller_drop_total 'deleting-controller')"
log "switch to delete mode (ci/e2e.yaml); deleting-controller drops before: $drop_before"
helm upgrade kjp "$chart" -n kube-janitor -f "$chart/ci/e2e.yaml" --wait
run_chainsaw 10-delete-mode
drop_after="$(metric kyverno_controller_drop_total 'deleting-controller')"
errors_after="$(metric kyverno_deleting_controller_errors_total 'kube-janitor-ephemeral-pods')"
echo "E13: deleting-controller drops after: $drop_after; kube-janitor policy errors: $errors_after"
[ "$drop_after" -eq "$drop_before" ] || { echo "E13 FAILED: the schedule dropped a run"; exit 1; }

echo "TTL controller watches pods in delete mode: $(ttl_watches_pods) (expect 1: the pack's role arms the TTL label)"
[ "$(ttl_watches_pods)" -ge 1 ] || { echo "E16b FAILED: TTL controller does not watch pods in delete mode"; exit 1; }
run_chainsaw 20-guard
run_chainsaw 30-ttl-arming
run_chainsaw 40-rbac-scope

log "observation: TTL-labeled Pods created before the guard, now that delete mode granted Pods"
for p in default/e16d-ttl-default kube-public/e16e-ttl-kube-public; do
  if kubectl -n "${p%%/*}" get pod "${p#*/}" >/dev/null 2>&1; then echo "$p: still present"; else echo "$p: DELETED by the Kyverno TTL controller"; fi
done

log "remove leftover pre-install fixtures, then E1: full preflight must pass"
kubectl delete pod -n default e16d-ttl-default e3-default-optin --ignore-not-found --wait=false
kubectl delete pod -n kube-public e16e-ttl-kube-public --ignore-not-found --wait=false
kubectl delete namespace e2e-pre --ignore-not-found --wait=false
"$root/hack/preflight.sh"

run_chainsaw 50-missing-rbac
run_chainsaw 60-after-uninstall

log "all end-to-end scenarios passed"

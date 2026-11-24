#!/usr/bin/env bash
# End-to-end tests for kube-janitor-policies on kind (docs/specs/security-and-testing.md 6.3).
# Order matters: upgrade from the previous chart -> audit mode -> delete mode -> guard -> TTL
# arming -> RBAC scope -> preflight -> missing RBAC -> uninstall. Chainsaw runs the
# assertions; this driver runs the Helm and preflight steps between them.
#
# Env: K8S_IMAGE (kind node image, digest-pinned), KEEP_CLUSTER=1 to keep the cluster,
#      CLUSTER (default kjp-e2e), PREVIOUS_REF (git ref of the previous chart, default
#      origin/main). Tool versions: tests/versions.env.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
. "$root/tests/versions.env"
export PATH="$root/bin:$PATH"
export REPO_ROOT="$root"
cluster="${CLUSTER:-kjp-e2e}"
image="${K8S_IMAGE:-$KIND_NODE_1_37}"
previous_ref="${PREVIOUS_REF:-origin/main}"
chart="$root/charts/kube-janitor-policies"
suite="$root/tests/chainsaw/packs"
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

metrics_raw() { kubectl get --raw "/api/v1/namespaces/kyverno/services/kyverno-cleanup-controller-metrics:8000/proxy/metrics" 2>/dev/null; }
metric() { # <name> <label filter> -> summed value (0 when absent)
  metrics_raw | awk -v n="$1" -v f="$2" '$1 ~ "^"n"(\\{|$)" && index($0, f) { s += $NF } END { printf "%d\n", s }'
}
ttl_watches_pods() { metrics_raw | grep -c '^kyverno_ttl_controller_info{.*resource_resource="pods"' || true; }
# The TTL manager re-checks its permissions every ttlReconciliationInterval (1m), so arming and
# disarming the cleanup.kyverno.io/ttl label lag a mode change by up to one interval.
wait_ttl_watch() { # <want: 0|1> -> waits up to 150s
  local _
  for _ in $(seq 1 30); do
    if [ "$1" -eq 0 ] && [ "$(ttl_watches_pods)" -eq 0 ]; then return 0; fi
    if [ "$1" -eq 1 ] && [ "$(ttl_watches_pods)" -ge 1 ]; then return 0; fi
    sleep 5
  done
  return 1
}
can_delete() { # <group> <resource> -> true|false for the cleanup controller
  kubectl create -o jsonpath='{.status.allowed}' -f - <<EOF
{"apiVersion":"authorization.k8s.io/v1","kind":"SubjectAccessReview","spec":{"user":"system:serviceaccount:kyverno:kyverno-cleanup-controller","groups":["system:serviceaccounts","system:serviceaccounts:kyverno","system:authenticated"],"resourceAttributes":{"group":"$1","resource":"$2","verb":"delete"}}}
EOF
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

log "E11: upgrade from the previous chart ($previous_ref)"
if git -C "$root" cat-file -e "$previous_ref:charts/kube-janitor-policies/Chart.yaml" 2>/dev/null; then
  mkdir -p "$work/previous"
  git -C "$root" archive "$previous_ref" charts/kube-janitor-policies | tar -x -C "$work/previous"
  helm install kjp "$work/previous/charts/kube-janitor-policies" -n kube-janitor --create-namespace --wait \
    --set packs.ephemeralPods.enabled=true --set packs.ephemeralPods.mode=delete --set 'global.schedule=*/1 * * * *'
  helm upgrade kjp "$chart" -n kube-janitor --reuse-values --wait
  values="$(helm get values kjp -n kube-janitor -o json)"
  [ "$(jq -r '.packs.ephemeralPods.mode' <<<"$values")" = "delete" ] || fail "E11: mode changed by upgrade"
  [ "$(kubectl get deletingpolicies -l app.kubernetes.io/name=kube-janitor-policies -o name | wc -l)" -eq 1 ] \
    || fail "E11: upgrade enabled more packs"
  sleep 5
  echo "E11 delete RBAC: pods=$(can_delete '' pods) configmaps=$(can_delete '' configmaps) namespaces=$(can_delete '' namespaces) jobs=$(can_delete batch jobs)"
  [ "$(can_delete '' pods)/$(can_delete '' configmaps)/$(can_delete '' namespaces)/$(can_delete batch jobs)" = "true/false/false/false" ] \
    || fail "E11: upgrade changed delete RBAC"
  run_chainsaw 05-upgrade
  helm uninstall kjp -n kube-janitor --wait
  # The API server enforces ValidatingAdmissionPolicies from a cache, so a deleted guard can
  # still reject requests for a short time after `helm uninstall --wait` returns. Wait until a
  # request that the guard would reject (cleanup label in `default`) is admitted again.
  admitted=0
  for _ in $(seq 1 24); do
    if kubectl -n default run guard-gone-probe --image=registry.k8s.io/pause:3.10 \
         --labels=janitor.io/ephemeral=true --dry-run=server >/dev/null 2>&1; then admitted=1; break; fi
    sleep 5
  done
  [ "$admitted" -eq 1 ] || fail "E11: the guard still rejects requests 120s after uninstall"
else
  echo "SKIP E11: $previous_ref has no kube-janitor-policies chart yet"
fi

log "objects created before the chart and its guard exist"
kubectl apply -f "$suite/fixtures/pre-install.yaml"

log "install the chart with every pack in audit mode"
helm upgrade --install kjp "$chart" -n kube-janitor --create-namespace -f "$chart/ci/audit.yaml" --wait

log "E16d: preflight before delete mode finds pre-armed TTL labels (expect exit 1, P8 FAIL)"
set +e; preflight_out="$("$root/hack/preflight.sh" --kinds pods)"; rc=$?; set -e
echo "$preflight_out"
[ "$rc" -eq 1 ] && grep -q "P8   FAIL pods default/e16d-ttl-default" <<<"$preflight_out" \
  && grep -q "P8   FAIL pods kube-public/e16e-ttl-kube-public" <<<"$preflight_out" || fail "E16d"

log "audit mode"
wait_ttl_watch 0 || fail "E16a: TTL label still armed 150s after leaving delete mode"
echo "TTL controller watches pods in audit mode: $(ttl_watches_pods) (expect 0)"
run_chainsaw 01-audit-mode
run_chainsaw 02-preview-audit

drop_before="$(metric kyverno_controller_drop_total 'deleting-controller')"
log "switch every pack to delete mode (ci/e2e.yaml); deleting-controller drops before: $drop_before"
helm upgrade kjp "$chart" -n kube-janitor -f "$chart/ci/e2e.yaml" --wait
run_chainsaw 10-delete-mode
run_chainsaw 11-preview-delete
run_chainsaw 12-finalizer
run_chainsaw 13-jobs
run_chainsaw 14-mixed-ownership
drop_after="$(metric kyverno_controller_drop_total 'deleting-controller')"
errors_after="$(metric kyverno_deleting_controller_errors_total 'kube-janitor-')"
echo "E13/E9: deleting-controller drops after: $drop_after; kube-janitor policy errors: $errors_after"
[ "$drop_after" -eq "$drop_before" ] || fail "E13: the schedule dropped a run"

wait_ttl_watch 1 || fail "E16b: TTL controller does not watch pods 150s after delete mode"
echo "TTL controller watches pods in delete mode: $(ttl_watches_pods) (expect 1: the pack's role arms the TTL label)"
run_chainsaw 20-guard
run_chainsaw 30-ttl-arming

log "E15: ephemeralPods in delete mode, temporaryConfigMaps in audit mode"
helm upgrade kjp "$chart" -n kube-janitor -f "$chart/ci/default.yaml" --wait \
  --set packs.ephemeralPods.enabled=true --set packs.ephemeralPods.mode=delete \
  --set packs.temporaryConfigMaps.enabled=true --set 'global.schedule=*/1 * * * *'
sleep 5
run_chainsaw 40-rbac-scope
helm upgrade kjp "$chart" -n kube-janitor -f "$chart/ci/e2e.yaml" --wait

log "observation: TTL-labeled Pods created before the guard, now that delete mode granted Pods"
for p in default/e16d-ttl-default kube-public/e16e-ttl-kube-public; do
  if kubectl -n "${p%%/*}" get pod "${p#*/}" >/dev/null 2>&1; then echo "$p: still present"; else echo "$p: DELETED by the Kyverno TTL controller"; fi
done

log "remove leftover pre-install fixtures, then E1: full preflight must pass"
kubectl delete pod -n default e16d-ttl-default e3-default-optin --ignore-not-found --wait=false
kubectl delete pod -n kube-public e16e-ttl-kube-public --ignore-not-found --wait=false
kubectl delete namespace e2e-pre --ignore-not-found --wait=false
sleep 70  # let every DeletingPolicy run once after the last upgrade (P6)
"$root/hack/preflight.sh"

run_chainsaw 50-missing-rbac
run_chainsaw 60-after-uninstall

log "all end-to-end scenarios passed"

#!/usr/bin/env bash
# kube-janitor-policies preflight (docs/specs/compatibility.md section 4.3, layer 2).
# Read-only: it uses kubectl and creates only SubjectAccessReview objects (never stored).
#
# Usage: hack/preflight.sh [--context <kube-context>] [--extra-protected ns1,ns2] [--kinds pods,configmaps]
#   --kinds  kinds that a pack is ABOUT to get delete permission for (P8). Default: the kinds of
#            every kube-janitor policy that exists (audit or delete mode).
# Exit: 0 when no check FAILs (WARN and WAIT do not fail), 1 otherwise, 2 on usage error.
set -uo pipefail

ctx=()
extra_protected=""
kinds_override=""
while [ $# -gt 0 ]; do
  case "$1" in
    --context) ctx=(--context "$2"); shift 2 ;;
    --extra-protected) extra_protected="$2"; shift 2 ;;
    --kinds) kinds_override="$2"; shift 2 ;;
    -h|--help) sed -n '2,8p' "$0"; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

for tool in kubectl jq; do
  command -v "$tool" >/dev/null || { echo "missing required tool: $tool" >&2; exit 2; }
done

k() { kubectl "${ctx[@]}" "$@"; }
failed=0
result() { # <check> <PASS|FAIL|WARN|WAIT> <message>
  printf '%-4s %-4s %s\n' "$1" "$2" "$3"
  [ "$2" = "FAIL" ] && failed=1
  return 0
}
min_version="1.20.0"
version_ge() { [ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -1)" = "$2" ]; }

# --- P1: cleanup controller -----------------------------------------------------------
cc_json="$(k get deploy -A -l app.kubernetes.io/component=cleanup-controller -o json 2>/dev/null || echo '{"items":[]}')"
cc_count="$(jq '.items | length' <<<"$cc_json")"
kyverno_ns=""
if [ "$cc_count" -ge 1 ]; then
  kyverno_ns="$(jq -r '.items[0].metadata.namespace' <<<"$cc_json")"
  cc_name="$(jq -r '.items[0].metadata.name' <<<"$cc_json")"
  avail="$(jq -r '.items[0].status.conditions[]? | select(.type=="Available") | .status' <<<"$cc_json")"
  if [ "$avail" = "True" ]; then
    result P1 PASS "cleanup controller $kyverno_ns/$cc_name is Available"
  else
    result P1 FAIL "cleanup controller $kyverno_ns/$cc_name is not Available"
  fi
else
  result P1 FAIL "no Kyverno cleanup controller Deployment (label app.kubernetes.io/component=cleanup-controller)"
fi

# --- P2: Kyverno version ----------------------------------------------------------------
if [ "$cc_count" -ge 1 ]; then
  image="$(jq -r '.items[0].spec.template.spec.containers[0].image' <<<"$cc_json")"
  tag="${image##*:}"; tag="${tag%%@*}"; ver="${tag#v}"
  if [[ "$image" != *:* || "$ver" == sha256* || ! "$ver" =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]]; then
    result P2 WARN "cannot read a version from image $image; confirm Kyverno >= $min_version manually"
  elif version_ge "${BASH_REMATCH[0]}" "$min_version"; then
    result P2 PASS "Kyverno $tag >= v$min_version"
  else
    result P2 FAIL "Kyverno $tag < v$min_version (minimum supported)"
  fi
else
  result P2 FAIL "skipped: no cleanup controller"
fi

# --- P3: DeletingPolicy v1 served ---------------------------------------------------------
if k get --raw /apis/policies.kyverno.io/v1 2>/dev/null | jq -e '.resources[] | select(.name=="deletingpolicies")' >/dev/null; then
  result P3 PASS "policies.kyverno.io/v1 DeletingPolicy is served"
else
  result P3 FAIL "policies.kyverno.io/v1 DeletingPolicy is not served"
fi

# Kube-janitor policies present in the cluster.
dpol_json="$(k get deletingpolicies.policies.kyverno.io -l app.kubernetes.io/name=kube-janitor-policies -o json 2>/dev/null || echo '{"items":[]}')"
vpol_json="$(k get validatingpolicies.policies.kyverno.io -l app.kubernetes.io/name=kube-janitor-policies -o json 2>/dev/null || echo '{"items":[]}')"
# "group/resource" pairs per mode.
delete_kinds="$(jq -r '[.items[].spec.matchConstraints.resourceRules[] | .apiGroups[] as $g | .resources[] | "\($g)/\(.)"] | unique | .[]' <<<"$dpol_json")"
all_kinds="$( { echo "$delete_kinds"; jq -r '[.items[].spec.matchConstraints.resourceRules[] | .apiGroups[] as $g | .resources[] | "\($g)/\(.)"] | unique | .[]' <<<"$vpol_json"; } | sed '/^$/d' | sort -u)"

# --- P4: cleanup controller RBAC ----------------------------------------------------------
if [ -z "$delete_kinds" ]; then
  result P4 PASS "no pack in delete mode; no delete permission needed"
elif [ "$cc_count" -lt 1 ]; then
  result P4 FAIL "skipped: no cleanup controller"
else
  sa="$(jq -r '.items[0].spec.template.spec.serviceAccountName' <<<"$cc_json")"
  user="system:serviceaccount:$kyverno_ns:$sa"
  while IFS= read -r gr; do
    group="${gr%%/*}"; resource="${gr#*/}"
    for verb in list delete; do
      allowed="$(k create -o json -f - <<EOF 2>/dev/null | jq -r '.status.allowed'
{"apiVersion":"authorization.k8s.io/v1","kind":"SubjectAccessReview",
 "spec":{"user":"$user","groups":["system:serviceaccounts","system:serviceaccounts:$kyverno_ns","system:authenticated"],
         "resourceAttributes":{"group":"$group","resource":"$resource","verb":"$verb"}}}
EOF
)"
      if [ "$allowed" = "true" ]; then
        result P4 PASS "$user may $verb ${resource}${group:+.$group} cluster-wide"
      else
        result P4 FAIL "$user may NOT $verb ${resource}${group:+.$group}: the pack cannot delete (check the aggregated ClusterRole)"
      fi
    done
  done <<<"$delete_kinds"
fi

# --- P5: metadata guard -------------------------------------------------------------------
if [ -z "$all_kinds" ]; then
  result P5 PASS "no pack enabled; guard not required"
elif k get validatingadmissionpolicy kube-janitor-metadata-guard >/dev/null 2>&1; then
  actions="$(k get validatingadmissionpolicybinding kube-janitor-metadata-guard -o json 2>/dev/null | jq -r '.spec.validationActions // [] | join(",")')"
  if [[ ",$actions," == *",Deny,"* ]]; then
    result P5 PASS "metadata guard active with validationActions=$actions"
  elif [ -n "$delete_kinds" ]; then
    result P5 FAIL "metadata guard binding lacks Deny ($actions) while a pack is in delete mode"
  else
    result P5 WARN "metadata guard binding lacks Deny ($actions); it must have Deny before delete mode"
  fi
else
  result P5 FAIL "ValidatingAdmissionPolicy kube-janitor-metadata-guard not found"
fi

# --- P6: delete-mode policies run -----------------------------------------------------------
now="$(date -u +%s)"
if [ "$(jq '.items | length' <<<"$dpol_json")" -eq 0 ]; then
  result P6 PASS "no DeletingPolicy; nothing to run"
else
  while IFS=$'\t' read -r name schedule created last; do
    if [[ "$schedule" =~ ^\*/([0-9]+)\ \*\ \*\ \*\ \*$ ]]; then interval=$(( BASH_REMATCH[1] * 60 )); else interval=3600; fi
    window=$(( 2 * interval + 60 ))
    created_s="$(date -u -d "$created" +%s)"
    if [ -z "$last" ] || [ "$last" = "null" ]; then
      if (( now - created_s < window )); then
        result P6 WAIT "$name has not run yet (created $(( now - created_s ))s ago, schedule '$schedule')"
      else
        result P6 FAIL "$name has never run; check kyverno_deleting_controller_errors_total and kyverno_controller_drop_total"
      fi
    else
      age=$(( now - $(date -u -d "$last" +%s) ))
      if (( age <= window )); then
        result P6 PASS "$name last ran ${age}s ago (schedule '$schedule')"
      else
        result P6 FAIL "$name last ran ${age}s ago, more than two intervals; the schedule may be stalled (Kyverno #18248)"
      fi
    fi
  done < <(jq -r '.items[] | [.metadata.name, .spec.schedule, .metadata.creationTimestamp, (.status.lastExecutionTime // "")] | @tsv' <<<"$dpol_json")
fi

# --- P7: audit mode can report ----------------------------------------------------------------
if [ "$(jq '.items | length' <<<"$vpol_json")" -eq 0 ]; then
  result P7 PASS "no pack in audit mode"
else
  rc="$(k get deploy -A -l app.kubernetes.io/component=reports-controller -o json 2>/dev/null | jq -r '[.items[].status.conditions[]? | select(.type=="Available") | .status] | first // "missing"')"
  if [ "$rc" = "True" ]; then
    result P7 PASS "reports controller is Available (audit results appear in PolicyReports)"
  else
    result P7 FAIL "reports controller is not Available ($rc); audit mode cannot report"
  fi
fi

# --- P8: pre-armed cleanup.kyverno.io/ttl labels -------------------------------------------------
protected="kube-system kube-public kube-node-lease default ${kyverno_ns:-kyverno} ${extra_protected//,/ }"
if [ -n "$kinds_override" ]; then
  check_kinds="$(tr ',' '\n' <<<"$kinds_override" | sed '/^$/d')"
else
  check_kinds="$(sed 's#^/##; s#^\(.*\)/\(.*\)$#\2.\1#' <<<"$all_kinds" | sed '/^$/d')"
fi
if [ -z "$check_kinds" ]; then
  result P8 PASS "no pack enabled; no TTL label to check"
else
  while IFS= read -r kind; do
    objs="$(k get "$kind" -A -l cleanup.kyverno.io/ttl -o json 2>/dev/null | jq -r '.items[] | [.metadata.namespace // "", .metadata.name, (.metadata.labels["janitor.io/protected"] // "")] | @tsv')"
    if [ -z "$objs" ]; then
      result P8 PASS "no $kind carries cleanup.kyverno.io/ttl"
      continue
    fi
    while IFS=$'\t' read -r ns name prot; do
      target="${ns:-$name}"
      if [[ " $protected " == *" $target "* ]] || [ "$prot" = "true" ]; then
        result P8 FAIL "$kind $ns/$name carries cleanup.kyverno.io/ttl in a protected scope; delete mode would let Kyverno's TTL controller delete it"
      else
        result P8 WARN "$kind $ns/$name carries cleanup.kyverno.io/ttl; it becomes effective when delete mode grants $kind"
      fi
    done <<<"$objs"
  done <<<"$check_kinds"
fi

exit "$failed"

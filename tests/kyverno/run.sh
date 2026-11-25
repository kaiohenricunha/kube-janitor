#!/usr/bin/env bash
# Runs every kyverno test suite against policies rendered from the chart.
# Usage: tests/kyverno/run.sh   (needs helm and kyverno on PATH, or in ./bin)
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export PATH="$root/bin:$PATH"
chart="$root/charts/kube-janitor-policies"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

render() { # <template> <values>
  helm template t "$chart" --namespace kube-janitor --api-versions policies.kyverno.io/v1 \
    -f "$chart/$2" --show-only "$1"
}

failed=0
for suite in "$root"/tests/kyverno/*/; do
  name="$(basename "$suite")"
  [ -f "$suite/suite.env" ] || continue
  # shellcheck disable=SC1091
  ( set -a; . "$suite/suite.env"; set +a
    mkdir -p "$work/kyverno"
    cp -r "$root/tests/kyverno/." "$work/kyverno/"
    render "$TEMPLATE" "$VALUES" > "$work/kyverno/$name/policy.yaml"
    if [ -n "${STRIP:-}" ]; then
      # Selector-bypass suite: drop the opt-in selector line so only CEL decides.
      grep -qF -- "$STRIP" "$work/kyverno/$name/policy.yaml" \
        || { echo "STRIP pattern not found in the render: $STRIP"; exit 1; }
      grep -vF -- "$STRIP" "$work/kyverno/$name/policy.yaml" > "$work/strip.tmp"
      mv "$work/strip.tmp" "$work/kyverno/$name/policy.yaml"
    fi
    echo "=== suite $name"
    kyverno test "$work/kyverno/$name" ) || failed=1
done

echo "=== negative control (must fail)"
mkdir -p "$work/neg"
cp -r "$root/tests/kyverno/ephemeral-pods/." "$work/neg/"
render templates/packs/ephemeral-pods/deletingpolicy.yaml ci/delete.yaml \
  | sed 's/time.now() > variables.expiresAt/time.now() > variables.expiresAtTypo/' > "$work/neg/policy.yaml"
if kyverno test "$work/neg" >"$work/neg.log" 2>&1; then
  echo "NEGATIVE CONTROL PASSED: broken CEL was not detected"; failed=1
else
  echo "negative control failed as expected: $(grep -m1 -iE 'error|undeclared|no such' "$work/neg.log" || true)"
fi
exit "$failed"

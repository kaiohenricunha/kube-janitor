#!/usr/bin/env bash
# Installs the pinned test tools from tests/versions.env into ./bin (git-ignored) and
# verifies each download against the release checksum file.
# Usage: hack/install-tools.sh [kyverno] [kind] [chainsaw]   (default: all three)
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck disable=SC1091
. "$root/tests/versions.env"
bin="$root/bin"
mkdir -p "$bin"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

fetch() { curl -fsSL --retry 3 -o "$tmp/$2" "$1"; }

install_kyverno() {
  local base="https://github.com/kyverno/kyverno/releases/download/$KYVERNO_CLI_VERSION"
  local asset="kyverno-cli_${KYVERNO_CLI_VERSION}_linux_x86_64.tar.gz"
  fetch "$base/$asset" "$asset"; fetch "$base/checksums.txt" kyverno-checksums.txt
  (cd "$tmp" && grep " $asset\$" kyverno-checksums.txt | sha256sum -c -)
  tar -xzf "$tmp/$asset" -C "$bin" kyverno
}

install_kind() {
  local base="https://github.com/kubernetes-sigs/kind/releases/download/$KIND_VERSION"
  fetch "$base/kind-linux-amd64" kind-linux-amd64; fetch "$base/kind-linux-amd64.sha256sum" kind.sha256sum
  (cd "$tmp" && sha256sum -c kind.sha256sum)
  install -m 0755 "$tmp/kind-linux-amd64" "$bin/kind"
}

install_chainsaw() {
  local base="https://github.com/kyverno/chainsaw/releases/download/$CHAINSAW_VERSION"
  local asset="chainsaw_linux_amd64.tar.gz"
  fetch "$base/$asset" "$asset"; fetch "$base/checksums.txt" chainsaw-checksums.txt
  (cd "$tmp" && grep " $asset\$" chainsaw-checksums.txt | sha256sum -c -)
  tar -xzf "$tmp/$asset" -C "$bin" chainsaw
}

tools=("$@")
[ ${#tools[@]} -gt 0 ] || tools=(kyverno kind chainsaw)
for t in "${tools[@]}"; do
  case "$t" in
    kyverno) install_kyverno ;;
    kind) install_kind ;;
    chainsaw) install_chainsaw ;;
    *) echo "unknown tool: $t" >&2; exit 2 ;;
  esac
done
"$bin/kyverno" version 2>/dev/null | head -1 || true
"$bin/kind" version 2>/dev/null || true
"$bin/chainsaw" version 2>/dev/null | head -1 || true

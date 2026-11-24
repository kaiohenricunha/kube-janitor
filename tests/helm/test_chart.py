#!/usr/bin/env python3
"""Helm render tests H1-H11 for charts/kube-janitor-policies.

See docs/specs/security-and-testing.md section 6.2. Needs `helm` on PATH and PyYAML.
Run: python3 tests/helm/test_chart.py            (all tests)
     python3 tests/helm/test_chart.py --update-golden
"""

import pathlib
import re
import subprocess
import sys

import yaml

ROOT = pathlib.Path(__file__).resolve().parents[2]
CHART = ROOT / "charts" / "kube-janitor-policies"
GOLDEN = ROOT / "tests" / "golden"
API = ["--api-versions", "policies.kyverno.io/v1"]
AGG_LABEL = "rbac.kyverno.io/aggregate-to-cleanup-controller"
SHA_RE = re.compile(r"@[0-9a-f]{40}\b")


def helm_template(*args, values=None, api=True):
    cmd = ["helm", "template", "t", str(CHART), "--namespace", "kube-janitor"]
    if api:
        cmd += API
    for v in values or []:
        cmd += ["-f", str(v)]
    cmd += list(args)
    return subprocess.run(cmd, capture_output=True, text=True)


def render(values=None, sets=None, api=True):
    args = []
    for s in sets or []:
        args += ["--set", s]
    r = helm_template(*args, values=values, api=api)
    if r.returncode != 0:
        raise AssertionError(f"render failed: {r.stderr.strip()}")
    return [d for d in yaml.safe_load_all(r.stdout) if d]


def render_fails(values=None, sets=None, extra=None, api=True, expect=None):
    args = list(extra or [])
    for s in sets or []:
        args += ["--set", s]
    r = helm_template(*args, values=values, api=api)
    assert r.returncode != 0, "render was expected to fail but succeeded"
    if expect:
        assert expect in r.stderr, f"expected {expect!r} in error, got: {r.stderr.strip()}"
    return r.stderr


def kinds(docs, kind):
    return [d for d in docs if d.get("kind") == kind]


def delete_rules(role):
    return [r for r in role.get("rules", []) if "delete" in r.get("verbs", [])]


# --- tests -------------------------------------------------------------------

def test_h1_default_renders_nothing():
    docs = render()
    assert docs == [], f"default values must render no object, got {[d.get('kind') for d in docs]}"


def test_h2_no_delete_rbac_without_delete_mode():
    for values in (None, [CHART / "ci" / "audit.yaml"]):
        docs = render(values=values)
        for role in kinds(docs, "ClusterRole") + kinds(docs, "Role"):
            assert not delete_rules(role), f"{role['metadata']['name']} grants delete outside delete mode"


PACKS = {"ephemeral-pods": ("", "pods"), "preview-environments": ("", "namespaces"),
         "temporary-configmaps": ("", "configmaps"), "completed-jobs-retention": ("batch", "jobs")}


def test_h3_audit_mode_renders_validating_policy_only():
    docs = render(values=[CHART / "ci" / "audit.yaml"])
    assert kinds(docs, "DeletingPolicy") == []
    vpols = {v["metadata"]["name"]: v for v in kinds(docs, "ValidatingPolicy")}
    assert set(vpols) == {f"kube-janitor-{slug}-audit" for slug in PACKS}, sorted(vpols)
    for vpol in vpols.values():
        assert vpol["spec"]["validationActions"] == ["Audit"]
        assert vpol["spec"]["evaluation"]["background"]["enabled"] is True
        assert vpol["spec"]["evaluation"]["admission"]["enabled"] is False
        assert vpol["spec"]["autogen"]["podControllers"]["controllers"] == []


def test_h4_delete_mode_renders_exact_aggregated_role():
    docs = render(values=[CHART / "ci" / "delete.yaml"])
    assert {d["metadata"]["name"] for d in kinds(docs, "DeletingPolicy")} == {f"kube-janitor-{s}" for s in PACKS}
    assert kinds(docs, "ValidatingPolicy") == []
    roles = {r["metadata"]["name"]: r for r in kinds(docs, "ClusterRole")
             if r["metadata"].get("labels", {}).get(AGG_LABEL) == "true"}
    assert set(roles) == {f"kube-janitor-policies:cleanup:{s}" for s in PACKS}, sorted(roles)
    for slug, (group, resource) in PACKS.items():
        rules = roles[f"kube-janitor-policies:cleanup:{slug}"]["rules"]
        assert rules == [{"apiGroups": [group], "resources": [resource], "verbs": ["get", "list", "watch", "delete"]}], rules
    for role in kinds(docs, "ClusterRole"):
        if role["metadata"]["name"] not in roles:
            assert not delete_rules(role), f"unexpected delete rule in {role['metadata']['name']}"
    # Only the pack that is in delete mode gets a role.
    one = render(sets=["packs.temporaryConfigMaps.enabled=true", "packs.temporaryConfigMaps.mode=delete",
                       "packs.ephemeralPods.enabled=true"])
    agg = [r["metadata"]["name"] for r in kinds(one, "ClusterRole") if r["metadata"].get("labels", {}).get(AGG_LABEL)]
    assert agg == ["kube-janitor-policies:cleanup:temporary-configmaps"], agg


def test_h5_no_two_policies_target_the_same_resource():
    docs = render(values=[CHART / "ci" / "delete.yaml"])
    seen = {}
    for pol in kinds(docs, "DeletingPolicy"):
        for rule in pol["spec"]["matchConstraints"]["resourceRules"]:
            for g in rule["apiGroups"]:
                for res in rule["resources"]:
                    key = (g, res)
                    assert key not in seen, f"{key} targeted by {seen[key]} and {pol['metadata']['name']}"
                    seen[key] = pol["metadata"]["name"]


def test_h6_schema_rejects_bad_values():
    schema = "don't meet the specifications of the schema"
    for bad in ("packs.ephemeralPods.enable=true", "packs.ephemeralPods.mode=destroy",
                "packs.ephemeralPods.defaultTTL=1d", "packs.ephemeralPods.defaultTTL=0h",
                "global.extraProtectedNamespaces={Bad_Name}", "unknownTopLevel=1"):
        render_fails(sets=[bad], expect=schema)
    render_fails(sets=["metadataGuard.validationActions={Deny,Warn}"], expect="both Deny and Warn")
    render_fails(sets=["preflight.helmTest.enabled=true"], expect="not implemented")
    render_fails(sets=["metadataGuard.mixedOwnership={Deny,Warn}"], expect="both Deny and Warn")


def test_h7_delete_mode_requires_deny_guard():
    render_fails(values=[CHART / "ci" / "delete.yaml"],
                 sets=["metadataGuard.validationActions={Warn,Audit}"], expect="Deny")
    docs = render(values=[CHART / "ci" / "delete.yaml"])
    binding = [b for b in kinds(docs, "ValidatingAdmissionPolicyBinding") if b["metadata"]["name"] == "kube-janitor-metadata-guard"]
    assert len(binding) == 1 and "Deny" in binding[0]["spec"]["validationActions"]


def test_h8_render_gate():
    render_fails(api=False, expect="policies.kyverno.io/v1")
    render_fails(values=[CHART / "ci" / "delete.yaml"], api=False, expect="policies.kyverno.io/v1")
    assert render(api=True) == []


def test_h9_kube_version_constraint():
    render_fails(extra=["--kube-version", "1.33.0"])
    assert render(sets=None) == []
    r = helm_template("--kube-version", "1.34.0")
    assert r.returncode == 0, r.stderr


def test_h12_guard_covers_every_enabled_kind():
    docs = render(values=[CHART / "ci" / "delete.yaml"])
    vap = [v for v in kinds(docs, "ValidatingAdmissionPolicy") if v["metadata"]["name"] == "kube-janitor-metadata-guard"][0]
    covered = {(r["apiGroups"][0], r["resources"][0]) for r in vap["spec"]["matchConstraints"]["resourceRules"]}
    assert covered == set(PACKS.values()), covered
    only_jobs = render(sets=["packs.completedJobsRetention.enabled=true"])
    vap = kinds(only_jobs, "ValidatingAdmissionPolicy")[0]
    covered = {(r["apiGroups"][0], r["resources"][0]) for r in vap["spec"]["matchConstraints"]["resourceRules"]}
    assert covered == {("batch", "jobs"), ("", "namespaces")}, "job-retention lives on Namespaces"


def test_h13_mixed_ownership_only_with_preview_pack():
    names = lambda d: {x["metadata"]["name"] for x in kinds(d, "ValidatingAdmissionPolicy")}
    assert "kube-janitor-mixed-ownership" not in names(render(sets=["packs.ephemeralPods.enabled=true"]))
    docs = render(sets=["packs.previewEnvironments.enabled=true"])
    assert "kube-janitor-mixed-ownership" in names(docs)
    b = [x for x in kinds(docs, "ValidatingAdmissionPolicyBinding") if x["metadata"]["name"] == "kube-janitor-mixed-ownership"][0]
    assert b["spec"]["validationActions"] == ["Warn", "Audit"]


def test_h14_prometheus_rule_only_when_enabled_and_deleting():
    assert kinds(render(values=[CHART / "ci" / "delete.yaml"]), "PrometheusRule") == []
    assert kinds(render(values=[CHART / "ci" / "audit.yaml"], sets=["monitoring.prometheusRule.enabled=true"]), "PrometheusRule") == []
    rule = kinds(render(values=[CHART / "ci" / "delete.yaml"], sets=["monitoring.prometheusRule.enabled=true"]), "PrometheusRule")
    assert len(rule) == 1
    alerts = {r["alert"] for g in rule[0]["spec"]["groups"] for r in g["rules"]}
    assert alerts == {"KubeJanitorDeletingPolicyErrors", "KubeJanitorDeletingScheduleDropped"}


def test_h15_protected_namespaces_cannot_be_removed():
    docs = render(values=[CHART / "ci" / "delete.yaml"], sets=["global.extraProtectedNamespaces={prod}", "global.kyvernoNamespace=kyv"])
    for pol in kinds(docs, "DeletingPolicy"):
        mc = pol["spec"]["matchConstraints"]
        sel = mc.get("namespaceSelector") or mc["objectSelector"]
        values = [e for e in sel["matchExpressions"] if e["key"] == "kubernetes.io/metadata.name"][0]["values"]
        assert values == ["kube-system", "kube-public", "kube-node-lease", "default", "kyv", "prod"], values


def test_h16_examples_parse():
    files = sorted((ROOT / "examples").rglob("*.y*ml"))
    assert files, "examples/ is empty"
    for f in files:
        list(yaml.safe_load_all(f.read_text()))


def golden_pairs():
    for values in sorted((CHART / "ci").glob("*.yaml")):
        yield values, GOLDEN / values.name


def golden_render(values):
    r = helm_template(values=[values])
    if r.returncode != 0:
        raise AssertionError(f"render failed for {values.name}: {r.stderr.strip()}")
    return r.stdout


def test_h10_golden_files_match():
    # Compare parsed documents, not bytes: Helm 3 and Helm 4 format the same objects differently
    # (line folding of long strings). Any change in content still fails under both versions.
    for values, golden in golden_pairs():
        assert golden.exists(), f"missing golden file {golden.relative_to(ROOT)}; run --update-golden"
        got = [d for d in yaml.safe_load_all(golden_render(values)) if d]
        want = [d for d in yaml.safe_load_all(golden.read_text()) if d]
        assert got == want, f"{golden.relative_to(ROOT)} is stale; run --update-golden (with Helm 3) and review the diff"


def test_h11_everything_pinned():
    # Every action in every workflow, and every image in chart templates, is pinned.
    for workflow in sorted((ROOT / ".github" / "workflows").glob("*.yml")):
        for n, line in enumerate(workflow.read_text().splitlines(), 1):
            m = re.search(r"uses:\s*(\S+)", line)
            if m and not m.group(1).startswith("./"):
                assert SHA_RE.search(m.group(1)), f"{workflow.name}:{n} action not pinned by SHA: {m.group(1)}"
    for path in (CHART / "templates").rglob("*"):
        if path.is_file():
            for n, line in enumerate(path.read_text().splitlines(), 1):
                m = re.search(r"image:\s*(\S+)", line)
                if m and "{{" not in m.group(1):
                    assert "@sha256:" in m.group(1), f"{path.relative_to(ROOT)}:{n} image not pinned by digest"


def test_h17_release_metadata():
    chart = yaml.safe_load((CHART / "Chart.yaml").read_text())
    assert re.fullmatch(r"\d+\.\d+\.\d+", chart["version"]), f"release version expected, got {chart['version']}"
    assert chart["appVersion"] == chart["version"]
    old = yaml.safe_load((ROOT / "charts" / "kube-janitor" / "Chart.yaml").read_text())
    assert old.get("deprecated") is True, "charts/kube-janitor must be marked deprecated"
    meta = (CHART / "artifacthub-repo.yml").read_text()
    assert "@" not in meta, "artifacthub-repo.yml must not contain an email address"
    assert yaml.safe_load(meta) is not None or meta.strip().startswith("#")


def test_h18_release_workflow_shape():
    wf = yaml.safe_load((ROOT / ".github" / "workflows" / "release-policies.yml").read_text())
    on = wf.get("on") or wf.get(True)
    assert on["push"]["tags"] == ["kube-janitor-policies-v*"], on
    text = (ROOT / ".github" / "workflows" / "release-policies.yml").read_text()
    for needle in ("helm package", "helm push", "cosign sign", "cosign verify", "artifacthub.io", "gh release create"):
        assert needle in text, f"release workflow lacks: {needle}"
    assert "uses: ./.github/workflows/policies.yml" in text, "release must reuse the Policies tests as a gate"
    old = (ROOT / ".github" / "workflows" / "release.yml").read_text()
    assert "goreleaser/goreleaser-action" not in old, "GoReleaser must be removed"
    assert "helm/chart-releaser-action" not in old, "the deprecated chart must not be published"


def update_golden():
    GOLDEN.mkdir(parents=True, exist_ok=True)
    for values, golden in golden_pairs():
        golden.write_text(golden_render(values))
        print(f"wrote {golden.relative_to(ROOT)}")


def main():
    if "--update-golden" in sys.argv:
        update_golden()
        return 0
    tests = [(n, f) for n, f in sorted(globals().items()) if n.startswith("test_") and callable(f)]
    failed = 0
    for name, fn in tests:
        try:
            fn()
            print(f"PASS {name}")
        except Exception as exc:  # noqa: BLE001 - report every failure
            failed += 1
            print(f"FAIL {name}: {exc}")
    print(f"{len(tests) - failed}/{len(tests)} passed")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())

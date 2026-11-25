# Security policy

## Supported versions

| Component | Supported |
|-----------|-----------|
| `kube-janitor-policies` chart, latest minor release | Yes |
| `kube-janitor-policies` chart, older releases | No. Upgrade to the latest release. |
| Deprecated Go controller (`charts/kube-janitor`, image `ghcr.io/kaiohenricunha/kube-janitor`) | Dependency security fixes only. It is frozen in report-only mode ([ADR-0002](docs/adr/0002-kyverno-native-cleanup.md)). |

## Report a vulnerability

Do not open a public issue. Report the problem privately through GitHub:
open the **Security** tab of this repository and click **Report a vulnerability**
(<https://github.com/kaiohenricunha/kube-janitor/security/advisories/new>).

Include:

- the chart version and the Kyverno version;
- the values you used (remove secrets);
- the manifests or steps that reproduce the problem;
- what was deleted, or what should have been deleted and was not.

## What counts as a vulnerability

The chart deletes cluster objects. Report these as security issues, not as ordinary bugs:

- An object or namespace with `janitor.io/protected=true` is deleted.
- An object in a protected namespace is deleted.
- A principal that may not delete an object can still make it eligible for deletion,
  for example by a bypass of the metadata guard (rules G-OPT, G-PROT, G-NS, G-FMT in
  [security-and-testing.md](docs/specs/security-and-testing.md) section 4).
- Metadata on one object stops cleanup for other objects, for example a value that makes
  every policy run fail.
- The chart grants the Kyverno cleanup controller more permissions than
  [security-and-testing.md](docs/specs/security-and-testing.md) section 3.2 lists.

## Response

This project has one maintainer, and reports get a best-effort response. A fix for a
deletion-safety problem is released as a new chart version, with a note in
[`charts/kube-janitor-policies/CHANGELOG.md`](charts/kube-janitor-policies/CHANGELOG.md).

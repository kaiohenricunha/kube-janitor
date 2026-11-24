{{/*
Render-time gate (docs/specs/compatibility.md section 4.3, layer 1).
It fails the render when:
- the Kyverno policies.kyverno.io/v1 API is absent (offline: --api-versions policies.kyverno.io/v1);
- a pack is in delete mode and the metadata guard does not block (Deny);
- a pack that has no templates yet is enabled.
There is deliberately no values flag that skips this gate.
*/}}
{{- define "kjp.gate" -}}
{{- if not (.Capabilities.APIVersions.Has "policies.kyverno.io/v1") -}}
{{- fail "kube-janitor-policies needs Kyverno >= 1.20: the API policies.kyverno.io/v1 was not found. Install Kyverno first. For offline rendering, run: helm template ... --api-versions policies.kyverno.io/v1" -}}
{{- end -}}
{{- if and (eq (include "kjp.anyDelete" .) "true") (not (has "Deny" .Values.metadataGuard.validationActions)) -}}
{{- fail "metadataGuard.validationActions must contain Deny while any pack is in delete mode: the guard is the only control on the cleanup.kyverno.io/ttl path (docs/specs/security-and-testing.md section 4.1)" -}}
{{- end -}}
{{- if and (has "Deny" .Values.metadataGuard.validationActions) (has "Warn" .Values.metadataGuard.validationActions) -}}
{{- fail "metadataGuard.validationActions cannot contain both Deny and Warn (Kubernetes rejects that binding)" -}}
{{- end -}}
{{- if and (has "Deny" .Values.metadataGuard.mixedOwnership) (has "Warn" .Values.metadataGuard.mixedOwnership) -}}
{{- fail "metadataGuard.mixedOwnership cannot contain both Deny and Warn (Kubernetes rejects that binding)" -}}
{{- end -}}
{{- if .Values.preflight.helmTest.enabled -}}
{{- fail (printf "preflight.helmTest is not implemented in chart version %s; run hack/preflight.sh instead" .Chart.Version) -}}
{{- end -}}
{{- $defs := include "kjp.packDefs" . | fromYaml -}}
{{- range $key, $pack := .Values.packs -}}
{{- if and $pack.enabled (not (index $defs $key).implemented) -}}
{{- fail (printf "packs.%s is not implemented in chart version %s; keep it disabled" $key $.Chart.Version) -}}
{{- end -}}
{{- end -}}
{{- end -}}

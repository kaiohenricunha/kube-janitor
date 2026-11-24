{{/*
Common labels for every object this chart renders.
*/}}
{{- define "kjp.labels" -}}
app.kubernetes.io/name: kube-janitor-policies
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end -}}

{{/*
Static definition of every pack, keyed by its values key.
`implemented: false` packs exist in the values API but have no templates yet.
*/}}
{{- define "kjp.packDefs" -}}
ephemeralPods:
  slug: ephemeral-pods
  implemented: true
  apiGroup: ""
  apiVersion: v1
  resource: pods
  scope: Namespaced
previewEnvironments:
  slug: preview-environments
  implemented: false
temporaryConfigMaps:
  slug: temporary-configmaps
  implemented: false
completedJobsRetention:
  slug: completed-jobs-retention
  implemented: false
{{- end -}}

{{/*
Protected namespaces: built-in list (cannot be removed) + Kyverno namespace + extras.
Returns a JSON array.
*/}}
{{- define "kjp.protectedNamespaces" -}}
{{- $list := list "kube-system" "kube-public" "kube-node-lease" "default" .Values.global.kyvernoNamespace -}}
{{- $list = concat $list (.Values.global.extraProtectedNamespaces | default list) | uniq -}}
{{- toJson $list -}}
{{- end -}}

{{/*
CEL list literal of the protected namespaces, for example ['kube-system', 'default'].
*/}}
{{- define "kjp.protectedNamespacesCEL" -}}
{{- $quoted := list -}}
{{- range (include "kjp.protectedNamespaces" . | fromJsonArray) -}}
{{- $quoted = append $quoted (printf "'%s'" .) -}}
{{- end -}}
{{- printf "[%s]" (join ", " $quoted) -}}
{{- end -}}

{{/*
"true" when at least one pack is enabled.
*/}}
{{- define "kjp.anyEnabled" -}}
{{- $any := false -}}
{{- range $key, $pack := .Values.packs -}}
{{- if $pack.enabled -}}{{- $any = true -}}{{- end -}}
{{- end -}}
{{- $any -}}
{{- end -}}

{{/*
"true" when at least one pack is enabled in delete mode.
*/}}
{{- define "kjp.anyDelete" -}}
{{- $any := false -}}
{{- range $key, $pack := .Values.packs -}}
{{- if and $pack.enabled (eq $pack.mode "delete") -}}{{- $any = true -}}{{- end -}}
{{- end -}}
{{- $any -}}
{{- end -}}

{{/*
Effective schedule of a pack: its own, or global.schedule.
Usage: include "kjp.schedule" (dict "root" $ "pack" $pack)
*/}}
{{- define "kjp.schedule" -}}
{{- default .root.Values.global.schedule .pack.schedule -}}
{{- end -}}

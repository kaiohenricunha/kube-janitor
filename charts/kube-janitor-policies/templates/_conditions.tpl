{{/*
Shared CEL for every pack (docs/specs/kyverno-policy-pack.md sections 5.3 and 6).
The DeletingPolicy (delete mode) and the audit ValidatingPolicy (audit mode) are both
built from these helpers, so the two modes always decide the same way.
All exclusions are CEL conditions: excludeResourceRules is not used (Kyverno #17884).

Pack shapes (kjp.packDefs):
- selection=object, scope=Namespaced: opt-in label on the object (Pods, ConfigMaps).
- selection=object, scope=Cluster: opt-in label on the Namespace object itself. These policies
  never reference namespaceObject (its behavior for kind Namespace is unverified).
- selection=namespace: opt-in label janitor.io/job-retention=enabled on the namespace and an
  objective state on the object (finished standalone Jobs).
*/}}

{{- define "kjp.expiryRegex" -}}
^[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]Z$
{{- end -}}

{{/*
CEL that validates the calendar date of a well-shaped expiry string `s` (a CEL expression
that yields a string) without calling timestamp(), which errors on dates such as
2026-02-30 and would fail the whole policy run (Kyverno #18248). Year 0000 is rejected too:
it is outside CEL's timestamp range (0001-9999) and timestamp() fails with an overflow.
Usage: include "kjp.validDateCEL" "variables.rawExpiry"
*/}}
{{- define "kjp.validDateCEL" -}}
{{- $s := . -}}
{{- $y := printf "int(%s.substring(0, 4))" $s -}}
{{- $m := printf "int(%s.substring(5, 7))" $s -}}
{{- $d := printf "int(%s.substring(8, 10))" $s -}}
{{- printf "(%s.matches('%s') && %s >= 1 && %s <= (%s == 2 ? ((%s %% 4 == 0 && (%s %% 100 != 0 || %s %% 400 == 0)) ? 29 : 28) : (%s in [4, 6, 9, 11] ? 30 : 31)))" $s (include "kjp.expiryRegex" nil) $y $d $m $y $y $y $m -}}
{{- end -}}

{{/*
Variables. Usage: include "kjp.policyVariables" (dict "def" $def "pack" $pack)
*/}}
{{- define "kjp.policyVariables" -}}
{{- $vars := list
  (dict "name" "labels" "expression" "object.metadata.?labels.orValue({})")
  (dict "name" "annotations" "expression" "object.metadata.?annotations.orValue({})")
-}}
{{- if eq .def.selection "namespace" -}}
{{- $vars = append $vars (dict "name" "finishedConditions" "expression" "object.?status.?conditions.orValue([]).filter(c, (c.type == 'Complete' || c.type == 'Failed') && c.status == 'True')") -}}
{{- $vars = append $vars (dict "name" "finishedAt" "expression" "variables.finishedConditions.size() > 0 ? timestamp(variables.finishedConditions[0].lastTransitionTime) : timestamp('9999-12-31T23:59:59Z')") -}}
{{- else -}}
{{- $vars = append $vars (dict "name" "rawExpiry" "expression" "variables.annotations[?'janitor.io/expires-at'].orValue('')") -}}
{{- $vars = append $vars (dict "name" "expiryValid" "expression" (include "kjp.validDateCEL" "variables.rawExpiry")) -}}
{{- $vars = append $vars (dict "name" "expiresAt" "expression" (printf "variables.rawExpiry == '' ? timestamp(object.metadata.creationTimestamp) + duration('%s') : (variables.expiryValid ? timestamp(variables.rawExpiry) : timestamp('9999-12-31T23:59:59Z'))" .pack.defaultTTL)) -}}
{{- end -}}
{{- toYaml $vars -}}
{{- end -}}

{{/*
Conditions, in evaluation order. All must be true for a deletion.
Usage: include "kjp.policyConditions" (dict "root" $ "def" $def "pack" $pack)
*/}}
{{- define "kjp.policyConditions" -}}
{{- $protected := include "kjp.protectedNamespacesCEL" .root -}}
{{- $conds := list (dict "name" "not-terminating" "expression" "!has(object.metadata.deletionTimestamp)") -}}
{{- /* The opt-in is also a server-side selector; checking it again in CEL keeps the main safety
       decision correct even if a selector is not applied (defense in depth, like protection). */ -}}
{{- if eq .def.selection "namespace" -}}
{{- $conds = append $conds (dict "name" "opted-in" "expression" "namespaceObject.metadata.?labels.orValue({})[?'janitor.io/job-retention'].orValue('') == 'enabled'") -}}
{{- else -}}
{{- $conds = append $conds (dict "name" "opted-in" "expression" "variables.labels[?'janitor.io/ephemeral'].orValue('') == 'true'") -}}
{{- end -}}
{{- /* janitor.io/protected=true protects as a label or as an annotation (the deprecated
       controller's form), on the object or on its namespace. */ -}}
{{- $objProt := "variables.labels[?'janitor.io/protected'].orValue('') != 'true' && variables.annotations[?'janitor.io/protected'].orValue('') != 'true'" -}}
{{- if eq .def.scope "Namespaced" -}}
{{- $conds = append $conds (dict "name" "not-protected" "expression" (printf "%s && namespaceObject.metadata.?labels.orValue({})[?'janitor.io/protected'].orValue('') != 'true' && namespaceObject.metadata.?annotations.orValue({})[?'janitor.io/protected'].orValue('') != 'true'" $objProt)) -}}
{{- $conds = append $conds (dict "name" "not-protected-namespace" "expression" (printf "!(object.metadata.namespace in %s)" $protected)) -}}
{{- else -}}
{{- $conds = append $conds (dict "name" "not-protected" "expression" $objProt) -}}
{{- $conds = append $conds (dict "name" "not-protected-namespace" "expression" (printf "!(object.metadata.name in %s)" $protected)) -}}
{{- end -}}
{{- $conds = append $conds (dict "name" "no-known-manager" "expression" "!variables.annotations.exists(k, k == 'argocd.argoproj.io/tracking-id' || k.startsWith('meta.helm.sh/')) && !variables.labels.exists(k, k.startsWith('kustomize.toolkit.fluxcd.io/') || k.startsWith('helm.toolkit.fluxcd.io/') || k == 'app.kubernetes.io/instance' || k == 'argocd.argoproj.io/instance') && variables.labels[?'app.kubernetes.io/managed-by'].orValue('') != 'Helm'") -}}
{{- if eq .def.scope "Namespaced" -}}
{{- $conds = append $conds (dict "name" "standalone" "expression" "object.metadata.?ownerReferences.orValue([]).size() == 0") -}}
{{- end -}}
{{- if eq .def.selection "namespace" -}}
{{- $conds = append $conds (dict "name" "no-native-ttl" "expression" "!has(object.spec.ttlSecondsAfterFinished)") -}}
{{- $conds = append $conds (dict "name" "finished" "expression" "variables.finishedConditions.size() > 0") -}}
{{- $conds = append $conds (dict "name" "retention-elapsed" "expression" (printf "time.now() > variables.finishedAt + duration('%s')" .pack.retention)) -}}
{{- else -}}
{{- $conds = append $conds (dict "name" "expired" "expression" "time.now() > variables.expiresAt") -}}
{{- end -}}
{{- toYaml $conds -}}
{{- end -}}

{{/*
matchConstraints. Opt-in and protection labels are server-side selectors, so the cleanup
controller lists only opted-in objects. Explicit group/version/resource: wildcards are not
expanded by the deleting controller (Kyverno #18376).
Usage: include "kjp.matchConstraints" (dict "root" $ "def" $def)
*/}}
{{- define "kjp.matchConstraints" -}}
{{- $protected := include "kjp.protectedNamespaces" .root | fromJsonArray -}}
resourceRules:
  - apiGroups: [{{ .def.apiGroup | quote }}]
    apiVersions: [{{ .def.apiVersion | quote }}]
    resources: [{{ .def.resource | quote }}]
    operations: ["*"]
    scope: {{ .def.scope }}
objectSelector:
  {{- if eq .def.selection "object" }}
  matchLabels:
    janitor.io/ephemeral: "true"
  {{- end }}
  matchExpressions:
    - key: janitor.io/protected
      operator: NotIn
      values: ["true"]
{{- if ne .def.scope "Namespaced" }}
    - key: kubernetes.io/metadata.name
      operator: NotIn
      values:
        {{- toYaml $protected | nindent 8 }}
{{- else }}
namespaceSelector:
  {{- if eq .def.selection "namespace" }}
  matchLabels:
    janitor.io/job-retention: enabled
  {{- end }}
  matchExpressions:
    - key: kubernetes.io/metadata.name
      operator: NotIn
      values:
        {{- toYaml $protected | nindent 8 }}
    - key: janitor.io/protected
      operator: NotIn
      values: ["true"]
{{- end }}
{{- end -}}

{{/*
One-line description of what a pack deletes.
*/}}
{{- define "kjp.packDescription" -}}
{{- if eq .def.selection "namespace" -}}
finished standalone {{ .def.resource }} without ttlSecondsAfterFinished, in namespaces labeled janitor.io/job-retention=enabled, {{ .pack.retention }} after they finished
{{- else -}}
{{ .def.resource }} labeled janitor.io/ephemeral=true after janitor.io/expires-at, or {{ .pack.defaultTTL }} after creation when the annotation is absent
{{- end -}}
{{- end -}}

{{/*
Full DeletingPolicy for a pack in delete mode.
Usage: include "kjp.deletingPolicy" (dict "root" $ "key" "ephemeralPods")
*/}}
{{- define "kjp.deletingPolicy" -}}
{{- $root := .root -}}
{{- $pack := index $root.Values.packs .key -}}
{{- $def := index (include "kjp.packDefs" $root | fromYaml) .key -}}
apiVersion: policies.kyverno.io/v1
kind: DeletingPolicy
metadata:
  name: kube-janitor-{{ $def.slug }}
  labels:
    {{- include "kjp.labels" $root | nindent 4 }}
    janitor.io/pack: {{ $def.slug }}
  annotations:
    policies.kyverno.io/description: >-
      kube-janitor {{ $def.slug }}: deletes {{ include "kjp.packDescription" (dict "def" $def "pack" $pack) }}.
spec:
  schedule: {{ include "kjp.schedule" (dict "root" $root "pack" $pack) | quote }}
  deletionPropagationPolicy: {{ $root.Values.global.deletionPropagationPolicy }}
  matchConstraints:
    {{- include "kjp.matchConstraints" (dict "root" $root "def" $def) | nindent 4 }}
  variables:
    {{- include "kjp.policyVariables" (dict "def" $def "pack" $pack) | nindent 4 }}
  conditions:
    {{- include "kjp.policyConditions" (dict "root" $root "def" $def "pack" $pack) | nindent 4 }}
{{- end -}}

{{/*
Full audit ValidatingPolicy for a pack in audit mode. It FAILS exactly for the objects the
DeletingPolicy would delete, so PolicyReports list "would delete" results.
Usage: include "kjp.auditPolicy" (dict "root" $ "key" "ephemeralPods")
*/}}
{{- define "kjp.auditPolicy" -}}
{{- $root := .root -}}
{{- $pack := index $root.Values.packs .key -}}
{{- $def := index (include "kjp.packDefs" $root | fromYaml) .key -}}
{{- $parts := list -}}
{{- range (include "kjp.policyConditions" (dict "root" $root "def" $def "pack" $pack) | fromYamlArray) -}}
{{- $parts = append $parts (printf "(%s)" .expression) -}}
{{- end -}}
{{- $when := "'expired at ' + string(variables.expiresAt)" -}}
{{- if eq $def.selection "namespace" -}}
{{- $when = printf "'finished at ' + string(variables.finishedAt) + ', retention %s'" $pack.retention -}}
{{- end -}}
apiVersion: policies.kyverno.io/v1
kind: ValidatingPolicy
metadata:
  name: kube-janitor-{{ $def.slug }}-audit
  labels:
    {{- include "kjp.labels" $root | nindent 4 }}
    janitor.io/pack: {{ $def.slug }}
  annotations:
    policies.kyverno.io/description: >-
      kube-janitor {{ $def.slug }} in audit mode: reports (fail) every object that the
      delete-mode policy would delete. Deletes nothing.
spec:
  validationActions: [Audit]
  evaluation:
    admission:
      enabled: false
    background:
      enabled: true
  autogen:
    podControllers:
      controllers: []
  matchConstraints:
    {{- include "kjp.matchConstraints" (dict "root" $root "def" $def) | nindent 4 }}
  variables:
    {{- include "kjp.policyVariables" (dict "def" $def "pack" $pack) | nindent 4 }}
  validations:
    - expression: {{ printf "!(%s)" (join " && " $parts) | quote }}
      messageExpression: {{ printf "'would be deleted by kube-janitor %s (' + %s + ')'" $def.slug $when | quote }}
{{- end -}}

{{/*
Aggregated ClusterRole that lets the Kyverno cleanup controller delete a pack's kind.
Rendered only in delete mode.
*/}}
{{- define "kjp.cleanupRole" -}}
{{- $root := .root -}}
{{- $def := index (include "kjp.packDefs" $root | fromYaml) .key -}}
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kube-janitor-policies:cleanup:{{ $def.slug }}
  labels:
    {{- include "kjp.labels" $root | nindent 4 }}
    janitor.io/pack: {{ $def.slug }}
    rbac.kyverno.io/aggregate-to-cleanup-controller: "true"
rules:
  - apiGroups: [{{ $def.apiGroup | quote }}]
    resources: [{{ $def.resource }}]
    verbs: [get, list, watch, delete]
{{- end -}}

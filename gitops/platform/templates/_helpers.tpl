{{/* True when a core component's `when` condition is met by the cluster values. */}}
{{- define "platform.when" -}}
{{- $c := .root.Values.cluster -}}
{{- if not .when -}}true
{{- else if eq .when "dns" -}}{{ if ne $c.dns.provider "none" }}true{{ end }}
{{- else if eq .when "backup" -}}{{ if $c.backup.s3 }}true{{ end }}
{{- else -}}{{ fail (printf "catalog: unknown condition %q" .when) }}
{{- end -}}
{{- end -}}

{{/* Applications for one catalog component: its releases, then its config chart. */}}
{{- define "platform.component" -}}
{{- $root := .root -}}
{{- $c := .component -}}
{{- $maxWave := 0 -}}
{{- range $c.install }}
{{- $chart := .chart | default $c.chart -}}
{{- if gt (int .wave) $maxWave }}{{ $maxWave = int .wave }}{{ end }}
---
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: {{ .release }}
  namespace: {{ $root.Values.argocd.namespace }}
  annotations:
    argocd.argoproj.io/sync-wave: {{ .wave | quote }}
spec:
  project: {{ $root.Values.argocd.project }}
  destination:
    server: https://kubernetes.default.svc
    namespace: {{ .namespace }}
  source:
    {{- if $c.git }}
    repoURL: {{ $c.git.repo }}
    path: {{ $c.git.path }}
    targetRevision: {{ $c.git.revision }}
    {{- else }}
    repoURL: {{ $chart.repo }}
    chart: {{ $chart.name }}
    targetRevision: {{ $chart.version | quote }}
    helm:
      releaseName: {{ .release }}
      {{- with $root.Files.Get (printf "values/%s.yaml" .release) }}
      valuesObject:
        {{- tpl . $root | fromYaml | toYaml | nindent 8 }}
      {{- end }}
    {{- end }}
  {{- include "platform.syncPolicy" (dict "prune" (ne .prune false) "privileged" .privileged) | nindent 2 }}
{{- end }}
{{- with $c.config }}
---
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: {{ . }}-config
  namespace: {{ $root.Values.argocd.namespace }}
  annotations:
    argocd.argoproj.io/sync-wave: {{ add1 $maxWave | quote }}
spec:
  project: {{ $root.Values.argocd.project }}
  destination:
    server: https://kubernetes.default.svc
  source:
    repoURL: {{ $root.Values.gitops.repoUrl }}
    path: gitops/config/{{ . }}
    targetRevision: {{ $root.Values.gitops.revision }}
    helm:
      valuesObject:
        cluster: {{- toYaml $root.Values.cluster | nindent 10 }}
  {{- include "platform.syncPolicy" (dict "prune" true) | nindent 2 }}
{{- end }}
{{- end -}}

{{- define "platform.syncPolicy" -}}
syncPolicy:
  automated:
    prune: {{ .prune }}
    selfHeal: true
  syncOptions:
    - CreateNamespace=true
    - ServerSideApply=true
  retry:
    limit: 10
    backoff: { duration: 30s, factor: 2, maxDuration: 5m }
  {{- if .privileged }}
  # Pods here mount host paths or use host networking.
  managedNamespaceMetadata:
    labels:
      pod-security.kubernetes.io/enforce: privileged
  {{- end }}
{{- end -}}

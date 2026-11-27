{{- define "requara.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "requara.labels" -}}
app.kubernetes.io/name: requara
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "requara.selector" -}}
app.kubernetes.io/name: requara
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* Generated credentials for the bundled services: created once, then kept
     across upgrades (looked up from the existing Secret). */}}
{{- define "requara.generated" -}}
{{- $name := printf "%s-generated" (include "requara.fullname" .) -}}
{{- $old := (lookup "v1" "Secret" .Release.Namespace $name).data | default dict -}}
{{- $keys := list "postgres-password" "s3-secret-key" "bao-role-id" "bao-secret-id" -}}
{{- $out := dict -}}
{{- range $keys -}}
{{- if hasKey $old . -}}
{{- $_ := set $out . (index $old . | b64dec) -}}
{{- else if hasPrefix "bao-" . -}}
{{- $_ := set $out . uuidv4 -}}
{{- else -}}
{{- $_ := set $out . (randAlphaNum 32) -}}
{{- end -}}
{{- end -}}
{{- toYaml $out -}}
{{- end -}}

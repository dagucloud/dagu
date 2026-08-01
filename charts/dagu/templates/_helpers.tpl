{{- define "dagu.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "dagu.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "dagu.componentName" -}}
{{- $component := required "component is required" .component -}}
{{- if gt (len $component) 61 -}}
{{- fail (printf "component name %q must not exceed 61 characters" $component) -}}
{{- end -}}
{{- $maxBaseLength := int (sub 62 (len $component)) -}}
{{- $base := include "dagu.fullname" .root | trunc $maxBaseLength | trimSuffix "-" -}}
{{- printf "%s-%s" $base $component | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{- define "dagu.labels" -}}
app.kubernetes.io/name: {{ include "dagu.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end }}

{{- define "dagu.selectorLabels" -}}
app.kubernetes.io/name: {{ include "dagu.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "dagu.workerLabels" -}}
{{- $pairs := list -}}
{{- range $key, $value := . -}}
{{- $pairs = append $pairs (printf "%s=%v" $key $value) -}}
{{- end -}}
{{- join "," $pairs -}}
{{- end }}

{{- define "dagu.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end }}

{{- define "dagu.pvcName" -}}
{{- default (include "dagu.componentName" (dict "root" . "component" "data")) .Values.persistence.existingClaim -}}
{{- end }}

{{- define "dagu.extraEnv" -}}
{{- with .Values.extraEnv }}
{{ toYaml . }}
{{- end }}
{{- end }}

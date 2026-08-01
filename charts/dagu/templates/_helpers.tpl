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

{{- define "dagu.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "dagu.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end }}

{{- define "dagu.componentName" -}}
{{- $component := required "component is required" .component -}}
{{- if gt (len $component) 61 -}}
{{- fail (printf "component name %q must not exceed 61 characters" $component) -}}
{{- end -}}
{{- $base := include "dagu.fullname" .root -}}
{{- $name := printf "%s-%s" $base $component -}}
{{- if le (len $name) 63 -}}
{{- $name -}}
{{- else -}}
{{- $hash := $name | sha256sum | trunc 8 -}}
{{- printf "%s-%s" ($name | trunc 54 | trimSuffix "-") $hash -}}
{{- end -}}
{{- end }}

{{- define "dagu.distributedConfigChecksum" -}}
{{- dict "deploymentMode" .Values.deploymentMode "publicUrl" .Values.config.publicUrl "envPassthrough" .Values.config.envPassthrough "envPassthroughPrefixes" .Values.config.envPassthroughPrefixes "coordinatorHealthPort" .Values.coordinator.healthPort "workerHealthPort" .Values.worker.healthPort | toJson | sha256sum -}}
{{- end }}

{{- define "dagu.imageTag" -}}
{{- default .Chart.AppVersion .Values.image.tag -}}
{{- end }}

{{- define "dagu.versionLabel" -}}
{{- $version := include "dagu.imageTag" . | replace "+" "_" | trunc 63 | trimAll "._-" -}}
{{- default "unknown" $version -}}
{{- end }}

{{- define "dagu.labels" -}}
app.kubernetes.io/name: {{ include "dagu.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ include "dagu.versionLabel" . | quote }}
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
{{- printf "%s:%s" .Values.image.repository (include "dagu.imageTag" .) -}}
{{- end }}

{{- define "dagu.pvcName" -}}
{{- default (include "dagu.componentName" (dict "root" . "component" "data")) .Values.persistence.existingClaim -}}
{{- end }}

{{- define "dagu.extraEnv" -}}
{{- with .Values.extraEnv }}
{{ toYaml . }}
{{- end }}
{{- end }}

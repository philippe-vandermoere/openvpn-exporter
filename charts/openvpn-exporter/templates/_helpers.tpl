{{/*
Renders a value through tpl, so it may itself contain Helm template
expressions (e.g. "{{ .Release.Name }}") evaluated against .context. Handles
both plain strings and complex values (lists/maps), which must go through
toYaml first since tpl only accepts a string.
Usage: {{ include "openvpn-exporter.render" (dict "value" .Values.x "context" $) }}
*/}}
{{- define "openvpn-exporter.render" -}}
{{- if typeIs "string" .value }}
{{- tpl .value .context }}
{{- else }}
{{- tpl (.value | toYaml) .context }}
{{- end }}
{{- end -}}

{{/*
Chart name.
*/}}
{{- define "openvpn-exporter.name" -}}
{{- $nameOverride := include "openvpn-exporter.render" (dict "value" .Values.nameOverride "context" .) }}
{{- default .Chart.Name $nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name.
*/}}
{{- define "openvpn-exporter.fullname" -}}
{{- $fullnameOverride := include "openvpn-exporter.render" (dict "value" .Values.fullnameOverride "context" .) }}
{{- if $fullnameOverride }}
{{- $fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $nameOverride := include "openvpn-exporter.render" (dict "value" .Values.nameOverride "context" .) }}
{{- $name := default .Chart.Name $nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "openvpn-exporter.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "openvpn-exporter.labels" -}}
helm.sh/chart: {{ include "openvpn-exporter.chart" . }}
{{ include "openvpn-exporter.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "openvpn-exporter.selectorLabels" -}}
app.kubernetes.io/name: {{ include "openvpn-exporter.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "openvpn-exporter.serviceAccountName" -}}
{{- $name := include "openvpn-exporter.render" (dict "value" .Values.serviceAccount.name "context" .) }}
{{- if .Values.serviceAccount.create }}
{{- default (include "openvpn-exporter.fullname" .) $name }}
{{- else }}
{{- default "default" $name }}
{{- end }}
{{- end }}

{{/*
Container args: --config when config.tunnels/config.servers is non-empty
(the chart's own ConfigMap, see openvpn-exporter.volumes below), plus
extraArgs.
*/}}
{{- define "openvpn-exporter.args" -}}
{{- if or .Values.config.tunnels .Values.config.servers }}
- --config=/etc/openvpn-exporter/config.yaml
{{- end }}
{{- with .Values.extraArgs }}
{{ include "openvpn-exporter.render" (dict "value" . "context" $) }}
{{- end }}
{{- end }}

{{/*
Container env vars.
*/}}
{{- define "openvpn-exporter.env" -}}
{{- if .Values.config.listenAddress }}
- name: OPENVPN_EXPORTER_LISTEN_ADDRESS
  value: {{ .Values.config.listenAddress | quote }}
{{- end }}
{{- if .Values.config.scrapeTimeout }}
- name: OPENVPN_EXPORTER_SCRAPE_TIMEOUT
  value: {{ .Values.config.scrapeTimeout | quote }}
{{- end }}
{{- if not .Values.config.tunnels }}
{{- if .Values.config.tunnel.name }}
- name: OPENVPN_EXPORTER_TUNNEL_NAME
  value: {{ include "openvpn-exporter.render" (dict "value" .Values.config.tunnel.name "context" $) | quote }}
{{- end }}
{{- if .Values.config.tunnel.managementAddress }}
- name: OPENVPN_EXPORTER_TUNNEL_MANAGEMENT_ADDRESS
  value: {{ include "openvpn-exporter.render" (dict "value" .Values.config.tunnel.managementAddress "context" $) | quote }}
{{- end }}
{{- if .Values.config.tunnel.certPath }}
- name: OPENVPN_EXPORTER_TUNNEL_CERT_PATH
  value: {{ include "openvpn-exporter.render" (dict "value" .Values.config.tunnel.certPath "context" $) | quote }}
{{- end }}
{{- if .Values.config.tunnel.configPath }}
- name: OPENVPN_EXPORTER_TUNNEL_CONFIG_PATH
  value: {{ include "openvpn-exporter.render" (dict "value" .Values.config.tunnel.configPath "context" $) | quote }}
{{- end }}
{{- end }}
{{- if not .Values.config.servers }}
{{- if .Values.config.server.name }}
- name: OPENVPN_EXPORTER_SERVER_NAME
  value: {{ include "openvpn-exporter.render" (dict "value" .Values.config.server.name "context" $) | quote }}
{{- end }}
{{- if .Values.config.server.managementAddress }}
- name: OPENVPN_EXPORTER_SERVER_MANAGEMENT_ADDRESS
  value: {{ include "openvpn-exporter.render" (dict "value" .Values.config.server.managementAddress "context" $) | quote }}
{{- end }}
{{- if .Values.config.server.certPath }}
- name: OPENVPN_EXPORTER_SERVER_CERT_PATH
  value: {{ include "openvpn-exporter.render" (dict "value" .Values.config.server.certPath "context" $) | quote }}
{{- end }}
{{- if .Values.config.server.configPath }}
- name: OPENVPN_EXPORTER_SERVER_CONFIG_PATH
  value: {{ include "openvpn-exporter.render" (dict "value" .Values.config.server.configPath "context" $) | quote }}
{{- end }}
{{- end }}
{{- if .Values.config.targetsGlob }}
- name: OPENVPN_EXPORTER_TARGETS_GLOB
  value: {{ include "openvpn-exporter.render" (dict "value" .Values.config.targetsGlob "context" $) | quote }}
{{- end }}
{{- if .Values.config.passwordSecretName }}
- name: OPENVPN_EXPORTER_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ include "openvpn-exporter.render" (dict "value" .Values.config.passwordSecretName "context" $) }}
      key: {{ include "openvpn-exporter.render" (dict "value" .Values.config.passwordSecretKey "context" $) }}
{{- end }}
{{- with .Values.extraEnv }}
{{ include "openvpn-exporter.render" (dict "value" . "context" $) }}
{{- end }}
{{- end }}

{{/*
Volumes: the chart's own ConfigMap (see configmap.yaml) when
config.tunnels/config.servers is non-empty, plus extraVolumes.
*/}}
{{- define "openvpn-exporter.volumes" -}}
{{- if or .Values.config.tunnels .Values.config.servers }}
- name: config
  configMap:
    name: {{ include "openvpn-exporter.fullname" . }}
{{- end }}
{{- with .Values.extraVolumes }}
{{ include "openvpn-exporter.render" (dict "value" . "context" $) }}
{{- end }}
{{- end }}

{{/*
Volume mounts: pairs with openvpn-exporter.volumes above, plus
extraVolumeMounts.
*/}}
{{- define "openvpn-exporter.volumeMounts" -}}
{{- if or .Values.config.tunnels .Values.config.servers }}
- name: config
  mountPath: /etc/openvpn-exporter
  readOnly: true
{{- end }}
{{- with .Values.extraVolumeMounts }}
{{ include "openvpn-exporter.render" (dict "value" . "context" $) }}
{{- end }}
{{- end }}

{{/*
Shared pod template (metadata + spec), included by both the Deployment
and the DaemonSet so the container/volumes/probes definition only lives
in one place.
*/}}
{{- define "openvpn-exporter.podTemplate" -}}
metadata:
  labels:
    {{- include "openvpn-exporter.selectorLabels" . | nindent 4 }}
    {{- with .Values.controller.podLabels }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
  {{- if or .Values.config.tunnels .Values.config.servers .Values.controller.podAnnotations }}
  annotations:
    {{- if or .Values.config.tunnels .Values.config.servers }}
    checksum/config: {{ include (print $.Template.BasePath "/configmap.yaml") . | sha256sum }}
    {{- end }}
    {{- with .Values.controller.podAnnotations }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
  {{- end }}
spec:
  serviceAccountName: {{ include "openvpn-exporter.serviceAccountName" . }}
  securityContext:
    {{- toYaml .Values.controller.podSecurityContext | nindent 4 }}
  {{- if .Values.controller.hostNetwork }}
  hostNetwork: true
  {{- end }}
  {{- if .Values.controller.dnsPolicy }}
  dnsPolicy: {{ .Values.controller.dnsPolicy }}
  {{- end }}
  {{- with .Values.imagePullSecrets }}
  imagePullSecrets:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  containers:
    - name: openvpn-exporter
      image: "{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}"
      imagePullPolicy: {{ .Values.image.pullPolicy }}
      securityContext:
        {{- toYaml .Values.controller.containerSecurityContext | nindent 8 }}
      {{- with (include "openvpn-exporter.args" . | trim) }}
      args:
        {{- . | nindent 8 }}
      {{- end }}
      {{- with (include "openvpn-exporter.env" . | trim) }}
      env:
        {{- . | nindent 8 }}
      {{- end }}
      ports:
        - name: http
          containerPort: {{ .Values.service.port }}
          protocol: TCP
      {{- if .Values.livenessProbe.enabled }}
      livenessProbe:
        httpGet:
          path: {{ .Values.livenessProbe.httpGet.path }}
          port: {{ .Values.livenessProbe.httpGet.port }}
        initialDelaySeconds: {{ .Values.livenessProbe.initialDelaySeconds }}
        periodSeconds: {{ .Values.livenessProbe.periodSeconds }}
        timeoutSeconds: {{ .Values.livenessProbe.timeoutSeconds }}
        failureThreshold: {{ .Values.livenessProbe.failureThreshold }}
      {{- end }}
      {{- if .Values.readinessProbe.enabled }}
      readinessProbe:
        httpGet:
          path: {{ .Values.readinessProbe.httpGet.path }}
          port: {{ .Values.readinessProbe.httpGet.port }}
        initialDelaySeconds: {{ .Values.readinessProbe.initialDelaySeconds }}
        periodSeconds: {{ .Values.readinessProbe.periodSeconds }}
        timeoutSeconds: {{ .Values.readinessProbe.timeoutSeconds }}
        failureThreshold: {{ .Values.readinessProbe.failureThreshold }}
      {{- end }}
      {{- with .Values.controller.resources }}
      resources:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with (include "openvpn-exporter.volumeMounts" . | trim) }}
      volumeMounts:
        {{- . | nindent 8 }}
      {{- end }}
  {{- with (include "openvpn-exporter.volumes" . | trim) }}
  volumes:
    {{- . | nindent 4 }}
  {{- end }}
  {{- with .Values.controller.nodeSelector }}
  nodeSelector:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- with .Values.controller.tolerations }}
  tolerations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- with .Values.controller.affinity }}
  affinity:
    {{- toYaml . | nindent 4 }}
  {{- end }}
{{- end }}

{{/*
Chart name.
*/}}
{{- define "openvpn-exporter.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name.
*/}}
{{- define "openvpn-exporter.fullname" -}}
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
{{- if .Values.serviceAccount.create }}
{{- default (include "openvpn-exporter.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Container args: none by default. The chart only supports single-tunnel,
env-var-driven configuration (see openvpn-exporter.env below) — anything
beyond that (e.g. a mounted multi-tunnel config.yaml and its --config flag)
is left to extraArgs/extraVolumes/extraVolumeMounts.
*/}}
{{- define "openvpn-exporter.args" -}}
{{- with .Values.extraArgs }}
{{ toYaml . }}
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
{{- if .Values.config.tunnel.name }}
- name: OPENVPN_EXPORTER_TUNNEL_NAME
  value: {{ .Values.config.tunnel.name | quote }}
{{- end }}
{{- if .Values.config.tunnel.managementAddress }}
- name: OPENVPN_EXPORTER_TUNNEL_MANAGEMENT_ADDRESS
  value: {{ .Values.config.tunnel.managementAddress | quote }}
{{- end }}
{{- if .Values.config.tunnel.certPath }}
- name: OPENVPN_EXPORTER_TUNNEL_CERT_PATH
  value: {{ .Values.config.tunnel.certPath | quote }}
{{- end }}
{{- if .Values.config.tunnel.configPath }}
- name: OPENVPN_EXPORTER_TUNNEL_CONFIG_PATH
  value: {{ .Values.config.tunnel.configPath | quote }}
{{- end }}
{{- if .Values.config.passwordSecretName }}
- name: OPENVPN_EXPORTER_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ .Values.config.passwordSecretName }}
      key: {{ .Values.config.passwordSecretKey }}
{{- end }}
{{- with .Values.extraEnv }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Volumes: extraVolumes only — the chart doesn't generate any volume of its
own (no multi-tunnel config.yaml/ConfigMap support, see
openvpn-exporter.args above).
*/}}
{{- define "openvpn-exporter.volumes" -}}
{{- with .Values.extraVolumes }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Volume mounts: extraVolumeMounts only.
*/}}
{{- define "openvpn-exporter.volumeMounts" -}}
{{- with .Values.extraVolumeMounts }}
{{ toYaml . }}
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
  {{- with .Values.controller.podAnnotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
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

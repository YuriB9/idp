{{/*
ConfigMap несекретной конфигурации компонента (адреса, namespace, TTL, log-level).
Рендерится из .component.env как есть. Секреты сюда НЕ попадают — см. idp-lib.secret.
*/}}
{{- define "idp-lib.configmap" -}}
{{- $c := .component -}}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .name }}-env
  labels:
    {{- include "idp-lib.labels" . | nindent 4 }}
data:
  {{- range $k, $v := ($c.env | default dict) }}
  {{ $k }}: {{ $v | quote }}
  {{- end }}
{{- end -}}

{{/*
Secret компонента (stringData). Реальные значения НЕ коммитятся — в репозитории
только плейсхолдеры values.example.yaml, прод-значения подаются вне git (--values
/ CI-secret). Рендерится только при непустом .component.secretEnv.
*/}}
{{- define "idp-lib.secret" -}}
{{- $c := .component -}}
{{- if $c.secretEnv -}}
apiVersion: v1
kind: Secret
metadata:
  name: {{ .name }}-secret
  labels:
    {{- include "idp-lib.labels" . | nindent 4 }}
type: Opaque
stringData:
  {{- range $k, $v := $c.secretEnv }}
  {{ $k }}: {{ $v | quote }}
  {{- end }}
{{- end -}}
{{- end -}}

{{/*
Общие хелперы платформы IDP (ADR-0024). Все шаблоны вызываются с контекстом-словарём:
  (dict "root" $ "name" "<компонент>" "component" .Values.components.<компонент>)
где .root — корневой контекст релиза ($), .name — имя компонента, .component —
его секция values.
*/}}

{{/* Полные labels ресурса (рекомендованные app.kubernetes.io/*). */}}
{{- define "idp-lib.labels" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/part-of: idp
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .root.Chart.Name .root.Chart.Version }}
{{- end -}}

{{/* Селекторные labels (стабильное подмножество, не меняется между релизами). */}}
{{- define "idp-lib.selectorLabels" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
{{- end -}}

{{/*
Полная ссылка на образ: registry/repository:tag. registry и tag компонента
переопределяют глобальные дефолты (.Values.global.image.*). Для внешних образов
(oauth2-proxy) registry/repository/tag задаются явно.
*/}}
{{- define "idp-lib.image" -}}
{{- $g := .root.Values.global.image -}}
{{- $img := .component.image -}}
{{- $registry := $img.registry | default $g.registry -}}
{{- $tag := $img.tag | default $g.tag -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry $img.repository $tag -}}
{{- else -}}
{{- printf "%s:%s" $img.repository $tag -}}
{{- end -}}
{{- end -}}

{{/*
securityContext контейнера: non-root, без эскалации привилегий,
read-only-rootfs, сброшенные capabilities, seccomp RuntimeDefault.
*/}}
{{- define "idp-lib.containerSecurityContext" -}}
runAsNonRoot: true
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop:
    - ALL
seccompProfile:
  type: RuntimeDefault
{{- end -}}

{{/*
Fail-closed для сервисов с аутентификацией (.component.auth=true): если
AUTH_DISABLED != "true", JWKS_URL обязателен и должен быть https. Проверка
выполняется на этапе рендера (template-time) — пустое/невалидное значение роняет
рендер, а не выпускает под (соответствует os.Exit(1) в pkg/auth).
*/}}
{{- define "idp-lib.assertAuth" -}}
{{- if .component.auth -}}
{{- $env := .component.env | default dict -}}
{{- $disabled := eq (toString (get $env "AUTH_DISABLED" | default "false")) "true" -}}
{{- if not $disabled -}}
{{- $jwks := get $env "JWKS_URL" -}}
{{- if not $jwks -}}
{{- fail (printf "компонент %s: JWKS_URL обязателен при включённой аутентификации (fail-closed)" .name) -}}
{{- end -}}
{{- if not (hasPrefix "https://" $jwks) -}}
{{- fail (printf "компонент %s: JWKS_URL должен быть https (fail-closed), получено %q" .name $jwks) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

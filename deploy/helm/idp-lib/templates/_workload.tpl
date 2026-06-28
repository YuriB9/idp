{{/*
Сборка стандартного набора ресурсов компонента: ConfigMap, Secret (если задан),
Deployment, Service и HPA (если включён). Пустые документы (отсутствующий Secret/
HPA) исключаются, чтобы не порождать пустые YAML-доки. Вызывается из тонких
шаблонов umbrella-чарта одной строкой.
*/}}
{{- define "idp-lib.workload" -}}
{{- $docs := list -}}
{{- $docs = append $docs (include "idp-lib.serviceaccount" .) -}}
{{- $docs = append $docs (include "idp-lib.configmap" .) -}}
{{- $secret := include "idp-lib.secret" . -}}
{{- if trim $secret -}}{{- $docs = append $docs $secret -}}{{- end -}}
{{- $docs = append $docs (include "idp-lib.deployment" .) -}}
{{- $docs = append $docs (include "idp-lib.service" .) -}}
{{- $hpa := include "idp-lib.hpa" . -}}
{{- if trim $hpa -}}{{- $docs = append $docs $hpa -}}{{- end -}}
{{- join "\n---\n" $docs -}}
{{- end -}}

{{/*
ServiceAccount компонента — даёт стабильную identity для mTLS/AuthorizationPolicy
Istio (principal cluster.local/ns/<ns>/sa/<name>, ADR-0025). По умолчанию у пода
был бы SA "default", непригодный для identity-based authz.
*/}}
{{- define "idp-lib.serviceaccount" -}}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ .name }}
  labels:
    {{- include "idp-lib.labels" . | nindent 4 }}
{{- end -}}

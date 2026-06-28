{{/*
HorizontalPodAutoscaler компонента. Рендерится только при .component.hpa.enabled.
В MVP применяется к devinfra-worker (масштабируется отдельно от API, ADR-0024).
*/}}
{{- define "idp-lib.hpa" -}}
{{- $c := .component -}}
{{- if and $c.hpa $c.hpa.enabled -}}
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: {{ .name }}
  labels:
    {{- include "idp-lib.labels" . | nindent 4 }}
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: {{ .name }}
  minReplicas: {{ $c.hpa.minReplicas | default 1 }}
  maxReplicas: {{ $c.hpa.maxReplicas | default 5 }}
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: {{ $c.hpa.targetCPUUtilizationPercentage | default 70 }}
{{- end -}}
{{- end -}}

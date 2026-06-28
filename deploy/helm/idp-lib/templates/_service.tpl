{{/*
Service компонента (ClusterIP). Экспонирует http-порт и, при наличии, grpc-порт.
Имена портов (http/grpc) используются Istio для определения протокола.
*/}}
{{- define "idp-lib.service" -}}
{{- $c := .component -}}
apiVersion: v1
kind: Service
metadata:
  name: {{ .name }}
  labels:
    {{- include "idp-lib.labels" . | nindent 4 }}
spec:
  type: ClusterIP
  selector:
    {{- include "idp-lib.selectorLabels" . | nindent 4 }}
  ports:
    - name: http
      port: {{ $c.httpPort }}
      targetPort: {{ $c.httpPort }}
    {{- if $c.grpcPort }}
    - name: grpc
      port: {{ $c.grpcPort }}
      targetPort: {{ $c.grpcPort }}
    {{- end }}
{{- end -}}

{{/*
Deployment компонента платформы. Собирает образ, порты, env (из ConfigMap и
Secret через envFrom), probes на /healthz и /readyz, securityContext non-root и
ресурсы. Перед рендером выполняет fail-closed-проверку аутентификации.
*/}}
{{- define "idp-lib.deployment" -}}
{{- include "idp-lib.assertAuth" . -}}
{{- $c := .component -}}
{{- $probes := $c.probes | default dict -}}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .name }}
  labels:
    {{- include "idp-lib.labels" . | nindent 4 }}
spec:
  replicas: {{ $c.replicas | default 1 }}
  selector:
    matchLabels:
      {{- include "idp-lib.selectorLabels" . | nindent 6 }}
  template:
    metadata:
      labels:
        {{- include "idp-lib.selectorLabels" . | nindent 8 }}
      annotations:
        # Sidecar Istio стартует раньше приложения — важно для раннего исходящего
        # трафика и порядка готовности (ADR-0025).
        proxy.istio.io/config: '{"holdApplicationUntilProxyStarts": true}'
        {{- with $c.podAnnotations }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
    spec:
      serviceAccountName: {{ .name }}
      securityContext:
        runAsNonRoot: true
        seccompProfile:
          type: RuntimeDefault
      containers:
        - name: {{ .name }}
          image: {{ include "idp-lib.image" . }}
          imagePullPolicy: {{ .root.Values.global.image.pullPolicy | default "IfNotPresent" }}
          {{- with $c.args }}
          args:
            {{- toYaml . | nindent 12 }}
          {{- end }}
          ports:
            - name: http
              containerPort: {{ $c.httpPort }}
            {{- if $c.grpcPort }}
            - name: grpc
              containerPort: {{ $c.grpcPort }}
            {{- end }}
          envFrom:
            - configMapRef:
                name: {{ .name }}-env
            {{- if $c.secretEnv }}
            - secretRef:
                name: {{ .name }}-secret
            {{- end }}
          {{- $probePort := $c.probePort | default $c.httpPort }}
          livenessProbe:
            httpGet:
              path: {{ $probes.liveness | default "/healthz" }}
              port: {{ $probePort }}
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: {{ $probes.readiness | default "/readyz" }}
              port: {{ $probePort }}
            periodSeconds: 10
          startupProbe:
            httpGet:
              path: {{ $probes.startup | default $probes.readiness | default "/readyz" }}
              port: {{ $probePort }}
            failureThreshold: 30
            periodSeconds: 5
          securityContext:
            {{- include "idp-lib.containerSecurityContext" . | nindent 12 }}
          resources:
            {{- toYaml ($c.resources | default .root.Values.global.resources) | nindent 12 }}
          {{- if $c.tmpDir }}
          volumeMounts:
            - name: tmp
              mountPath: /tmp
          {{- end }}
      {{- if $c.tmpDir }}
      volumes:
        # read-only-rootfs требует writable /tmp для процессов, пишущих временные
        # файлы (напр. nginx).
        - name: tmp
          emptyDir: {}
      {{- end }}
{{- end -}}

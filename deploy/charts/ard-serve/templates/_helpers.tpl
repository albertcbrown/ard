{{- define "ard-serve.name" -}}ard-{{ .Values.instance.name }}{{- end -}}

{{- define "ard-serve.labels" -}}
app.kubernetes.io/name: ard
app.kubernetes.io/instance: {{ include "ard-serve.name" . }}
app.kubernetes.io/part-of: ard
{{- end -}}

{{- define "ard-serve.selector" -}}
app.kubernetes.io/name: ard
app.kubernetes.io/instance: {{ include "ard-serve.name" . }}
{{- end -}}

{{- define "ard-serve.image" -}}
{{- $d := .Values.image.digest | default "" -}}
{{- if not (regexMatch "^sha256:[0-9a-f]{64}$" $d) -}}
{{- fail (printf "image.digest must be sha256:<64 hex> (the image pin), got %q" $d) -}}
{{- end -}}
{{ .Values.image.repository }}@{{ $d }}
{{- end -}}

{{- define "ard-serve.guard" -}}
{{- if ne (toString .Values.instance.dryRun) "true" -}}
{{- fail "instance.dryRun must be true: live ard serve in a cluster needs the k8s executor (#54) and a Lease lock" -}}
{{- end -}}
{{- if not (regexMatch "^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$" .Values.instance.repo) -}}
{{- fail (printf "instance.repo must be owner/name, got %q" .Values.instance.repo) -}}
{{- end -}}
{{- end -}}

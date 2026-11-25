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

{{- define "ard-serve.k8s" -}}{{ if eq (toString .Values.executor.kind) "k8s" }}true{{ end }}{{- end -}}

{{- define "ard-serve.jobsNamespace" -}}
{{- .Values.executor.jobsNamespace | default (printf "ard-jobs-%s" .Values.instance.name) -}}
{{- end -}}

{{- /* Every job pod carries these (internal/jobpod: LabelPartOf, LabelComponent, LabelInstance; --k8s-instance is
ard-serve.name). */ -}}
{{- define "ard-serve.jobSelector" -}}
app.kubernetes.io/part-of: ard
app.kubernetes.io/component: ard-job
app.kubernetes.io/instance: {{ include "ard-serve.name" . }}
{{- end -}}

{{- /* Egress never reaches these: other namespaces, private and CGNAT ranges, link-local metadata (#276). */ -}}
{{- define "ard-serve.exceptRanges" -}}
- 10.0.0.0/8
- 172.16.0.0/12
- 192.168.0.0/16
- 100.64.0.0/10
- 169.254.0.0/16
{{- end -}}

{{- define "ard-serve.agentImage" -}}
{{- $d := .Values.agentImage.digest | default "" -}}
{{- if not (regexMatch "^sha256:[0-9a-f]{64}$" $d) -}}
{{- fail (printf "agentImage.digest must be sha256:<64 hex> with executor k8s, got %q" $d) -}}
{{- end -}}
{{ .Values.agentImage.repository }}@{{ $d }}
{{- end -}}

{{- /* Checks the executor values. Everything here is interpolated into NetworkPolicies or CEL, so each value is
matched against a strict pattern first. */ -}}
{{- define "ard-serve.k8sGuard" -}}
{{- $k := include "ard-serve.k8s" . -}}
{{- $kind := toString .Values.executor.kind -}}
{{- if not (has $kind (list "local" "k8s")) -}}
{{- fail (printf "executor.kind must be local or k8s, got %q" $kind) -}}
{{- end -}}
{{- $mode := toString .Values.storage.accessMode -}}
{{- if not (has $mode (list "ReadWriteOnce" "ReadWriteOncePod")) -}}
{{- fail (printf "storage.accessMode must be ReadWriteOnce or ReadWriteOncePod, got %q" $mode) -}}
{{- end -}}
{{- if ne (toString .Values.instance.dryRun) "true" -}}
{{- if not $k -}}
{{- fail "instance.dryRun: false needs executor.kind: k8s: the local executor would run agents in the pod that holds the App key" -}}
{{- end -}}
{{- if ne $mode "ReadWriteOncePod" -}}
{{- fail "instance.dryRun: false needs storage.accessMode: ReadWriteOncePod: the leader lock is an flock on the home volume" -}}
{{- end -}}
{{- end -}}
{{- if $k -}}
{{- if not (regexMatch "^[a-z0-9]([a-z0-9-]{0,40}[a-z0-9])?$" (toString .Values.instance.name)) -}}
{{- fail (printf "instance.name must be a DNS label of at most 42 characters, got %q" (toString .Values.instance.name)) -}}
{{- end -}}
{{- $ns := include "ard-serve.jobsNamespace" . -}}
{{- if not (regexMatch "^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$" $ns) -}}
{{- fail (printf "executor.jobsNamespace must be a DNS label, got %q" $ns) -}}
{{- end -}}
{{- if eq $ns .Release.Namespace -}}
{{- fail (printf "executor.jobsNamespace %q must differ from the release namespace" $ns) -}}
{{- end -}}
{{- $_ := include "ard-serve.agentImage" . -}}
{{- range $n := list "resultPort" "agentUID" "sidecarUID" -}}
{{- if not (regexMatch "^[1-9][0-9]{0,4}$" (toString (index $.Values.jobs $n))) -}}
{{- fail (printf "jobs.%s must be a positive integer, got %q" $n (toString (index $.Values.jobs $n))) -}}
{{- end -}}
{{- end -}}
{{- if gt (int .Values.jobs.resultPort) 65535 -}}
{{- fail (printf "jobs.resultPort must be at most 65535, got %v" .Values.jobs.resultPort) -}}
{{- end -}}
{{- if eq (toString .Values.jobs.agentUID) (toString .Values.jobs.sidecarUID) -}}
{{- fail "jobs.agentUID must differ from jobs.sidecarUID: pre and post hold the job's secrets" -}}
{{- end -}}
{{- if not .Values.apiServer -}}
{{- fail "apiServer must list the kube-apiserver endpoints ({cidr, port}) with executor k8s" -}}
{{- end -}}
{{- range .Values.apiServer -}}
{{- if not (regexMatch "^([0-9]{1,3}\\.){3}[0-9]{1,3}/[0-9]{1,2}$" (toString .cidr)) -}}
{{- fail (printf "apiServer cidr must be an IPv4 CIDR, got %q" (toString .cidr)) -}}
{{- end -}}
{{- if not (regexMatch "^[1-9][0-9]{0,4}$" (toString .port)) -}}
{{- fail (printf "apiServer port must be a positive integer, got %q" (toString .port)) -}}
{{- end -}}
{{- end -}}
{{- if not .Values.jobs.githubCIDRs -}}
{{- fail "jobs.githubCIDRs must list GitHub's IPv4 CIDRs with executor k8s (deploy/github-cidrs.sh)" -}}
{{- end -}}
{{- range concat (.Values.jobs.githubCIDRs | default list) (.Values.jobs.modelAPICIDRs | default list) -}}
{{- if not (regexMatch "^([0-9]{1,3}\\.){3}[0-9]{1,3}/[0-9]{1,2}$" (toString .)) -}}
{{- fail (printf "jobs.githubCIDRs and jobs.modelAPICIDRs take IPv4 CIDRs, got %q" (toString .)) -}}
{{- end -}}
{{- end -}}
{{- if not (regexMatch "^[a-z=0-9.,-]*$" (toString .Values.jobs.resources)) -}}
{{- fail (printf "jobs.resources must be name=quantity pairs, got %q" (toString .Values.jobs.resources)) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "ard-serve.guard" -}}
{{- include "ard-serve.k8sGuard" . -}}
{{- if not (regexMatch "^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$" .Values.instance.repo) -}}
{{- fail (printf "instance.repo must be owner/name, got %q" .Values.instance.repo) -}}
{{- end -}}
{{- end -}}

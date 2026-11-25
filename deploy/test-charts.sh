#!/usr/bin/env bash
# Offline tests of the two Argo CD charts under deploy/:
#   deploy/argocd            ard-root, the app of apps: one child Application per instance, at the pinned commit
#   deploy/charts/ard-serve  one `ard serve` (Deployment, PVC, NetworkPolicy, ExternalSecret; with executor k8s also
#                            the jobs namespace, its NetworkPolicies, quota, RBAC and admission policy)
# ard-serve is rendered with the valuesObject the ard-root child passes, as Argo CD would. The default render must
# equal deploy/testdata/chart/ard-serve-default.yaml byte for byte.
# Needs helm and yq (mikefarah v4). Prints PASS:/FAIL: per case; exit 0 ok, 1 a case failed, 2 tooling.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ROOT_CHART="$ROOT/deploy/argocd"
SERVE_CHART="$ROOT/deploy/charts/ard-serve"
for t in helm yq; do command -v "$t" >/dev/null 2>&1 || { echo "ERROR: $t is required" >&2; exit 2; }; done

work="$(mktemp -d)" || { echo "ERROR: mktemp failed" >&2; exit 2; }
trap 'rm -rf "$work"' EXIT
failures=0
pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1${2:+ - $2}" >&2; failures=$((failures + 1)); }
check() { local label="$1"; shift; if "$@"; then pass "$label"; else fail "$label"; fi; }

DIGEST="sha256:$(printf '0123456789abcdef%.0s' 1 2 3 4)"
COMMIT="0123456789abcdef0123456789abcdef01234567"
ITEM="abcdefghijklmnopqrstuvwxyz"
AGENT_DIGEST="sha256:$(printf 'fedcba9876543210%.0s' 1 2 3 4)"

# ---- ard-root with a pin and site values, as a deployment repository gives them -----------------------------------
cat >"$work/pin.yaml" <<EOF
image: {repository: ghcr.io/albertcbrown/ard, digest: "$DIGEST", commit: "$COMMIT"}
agentImage: {repository: ghcr.io/albertcbrown/ard-agent, digest: "$AGENT_DIGEST"}
EOF
cat >"$work/site.yaml" <<EOF
site:
  githubApp:
    secretName: ard-github-app
    externalSecret: {enabled: true, storeKind: ClusterSecretStore, storeName: example-store, itemId: $ITEM, itemTitle: ard GitHub App, refreshInterval: 4h}
  storage: {size: 7Gi, storageClassName: example-sc}
EOF
if ! helm template ard-root "$ROOT_CHART" -f "$work/pin.yaml" -f "$work/site.yaml" >"$work/root.yaml" 2>"$work/root.err"; then
  echo "ERROR: ard-root does not render: $(cat "$work/root.err")" >&2; exit 1
fi
child() { yq "select(.kind == \"Application\" and .metadata.name == \"ard-albertcbrown-ard\") | $1" "$work/root.yaml"; }

check "child targetRevision is the pinned commit" test "$(child .spec.source.targetRevision)" = "$COMMIT"
check "child Application is named ard-* in project ard" test "$(child '.metadata.name + " " + .spec.project')" = "ard-albertcbrown-ard ard"
check "child Application syncs with FailOnSharedResource=true" \
  test "$(child '.spec.syncPolicy.syncOptions[] | select(. == "FailOnSharedResource=true")')" = "FailOnSharedResource=true"
check "child Application deploys only into namespace ard" test "$(child .spec.destination.namespace)" = "ard"

# ---- empty pin: nothing to deploy ----------------------------------------------------------------------------------
empty_ok() {
  helm template ard-root "$ROOT_CHART" >"$work/empty.yaml" 2>"$work/empty.err" || { cat "$work/empty.err" >&2; return 1; }
  [ ! -s "$work/empty.err" ] || { cat "$work/empty.err" >&2; return 1; }
  [ "$(yq 'select(.kind == "Application") | .metadata.name' "$work/empty.yaml" | grep -c . || true)" = "0" ]
}
check "empty digest renders no child Application" empty_ok

# ---- malformed pins are refused --------------------------------------------------------------------------------------
check "malformed digest refused" bash -c "! helm template r '$ROOT_CHART' --set image.digest=sha256:abc --set image.commit=$COMMIT >/dev/null 2>&1"
check "short commit refused" bash -c "! helm template r '$ROOT_CHART' --set image.digest=$DIGEST --set image.commit=abc1234 >/dev/null 2>&1"

# ---- ard-serve with the child's values ------------------------------------------------------------------------------
child '.spec.source.helm.valuesObject' >"$work/child-values.yaml"
if ! helm template ard-albertcbrown-ard "$SERVE_CHART" -f "$work/child-values.yaml" \
     --namespace ard >"$work/serve.yaml" 2>"$work/serve.err"; then
  echo "ERROR: ard-serve does not render: $(cat "$work/serve.err")" >&2; exit 1
fi
dep() { yq "select(.kind == \"Deployment\") | $1" "$work/serve.yaml" | sed '/^$/d'; }
ctr() { dep ".spec.template.spec.containers[] | select(.name == \"ard\") | $1"; }

check "deployment image is the pinned digest" test "$(ctr .image)" = "ghcr.io/albertcbrown/ard@$DIGEST"
check "pod has no other container or init container" test "$(dep '[.spec.template.spec.containers[].name, (.spec.template.spec.initContainers // [])[].name] | join(",")')" = "ard"
check "deployment is one replica with Recreate" test "$(dep '(.spec.replicas | tostring) + " " + .spec.strategy.type')" = "1 Recreate"
args="$(ctr '.args | join(" ")')"
check "serve args include --dry-run --repo albertcbrown/ard" \
  test "$args" = "serve --dry-run --repo albertcbrown/ard --loops scanners,stale,reaper"
check "serve runs only the scanners, stale and reaper loops" \
  bash -c "printf '%s\n' \"$args\" | grep -Eq -- '--loops scanners,stale,reaper( |\$)'"
check "HOME is /home/ard" test "$(ctr '.env[] | select(.name == "HOME") | .value')" = "/home/ard"
claim="$(dep '.spec.template.spec.volumes[] | select(.name == "home") | .persistentVolumeClaim.claimName')"
pvc="$(yq 'select(.kind == "PersistentVolumeClaim") | .metadata.name' "$work/serve.yaml")"
mount="$(ctr '.volumeMounts[] | select(.name == "home") | .mountPath')"
check "/home/ard is the PVC mount" test "$mount $claim" = "/home/ard $pvc"
check "PVC is kept when the Application is deleted" \
  test "$(yq 'select(.kind == "PersistentVolumeClaim") | .metadata.annotations["argocd.argoproj.io/sync-options"]' "$work/serve.yaml")" = "Prune=false,Delete=false"
check "pod mounts no service account token and runs as 65532" \
  test "$(dep '(.spec.template.spec.automountServiceAccountToken | tostring) + " " + (.spec.template.spec.securityContext.runAsUser | tostring)')" = "false 65532"

np() { yq "select(.kind == \"NetworkPolicy\") | $1" "$work/serve.yaml"; }
np_ok() {
  [ "$(np '.spec.policyTypes | sort | join(",")')" = "Egress,Ingress" ] || return 1
  [ "$(np '.spec.ingress | length')" = "0" ] || return 1
  [ "$(np '.spec.egress | length')" = "2" ] || return 1
  [ "$(np '.spec.egress | map(.to | length) | join(",")')" = "1,1" ] || return 1
  [ "$(np '.spec.egress[1] | has("ports")')" = "false" ] || return 1
  [ "$(np '.spec.egress[0].ports | map(.port) | join(",")')" = "53,53" ] || return 1
  [ "$(np '.spec.egress[0].to[0].namespaceSelector.matchLabels["kubernetes.io/metadata.name"]')" = "kube-system" ] || return 1
  [ "$(np '.spec.egress[1].to[0].ipBlock.cidr')" = "0.0.0.0/0" ] || return 1
  [ "$(np '.spec.egress[1].to[0].ipBlock.except | sort | join(",")')" = "10.0.0.0/8,100.64.0.0/10,169.254.0.0/16,172.16.0.0/12,192.168.0.0/16" ] || return 1
  [ "$(np '.spec.podSelector.matchLabels | to_entries | map(.key + "=" + .value) | sort | join(",")')" = \
    "$(dep '.spec.template.metadata.labels | to_entries | map(.key + "=" + .value) | sort | .[]' | grep -E '^app.kubernetes.io/(name|instance)=' | paste -sd, -)" ] || return 1
}
check "networkpolicy denies ingress and private egress" np_ok

es() { yq "select(.kind == \"ExternalSecret\") | $1" "$work/serve.yaml"; }
es_ok() {
  [ "$(es .metadata.name)" = "ard-github-app" ] || return 1
  [ "$(es '.spec.secretStoreRef.kind + "/" + .spec.secretStoreRef.name')" = "ClusterSecretStore/example-store" ] || return 1
  [ "$(es '.spec.refreshPolicy + " " + .spec.refreshInterval')" = "Periodic 4h" ] || return 1
  [ "$(es '.spec.dataFrom[0].extract.key')" = "$ITEM" ] || return 1
  [ "$(es '.spec.target.template.data | keys | sort | join(" ")')" = "ARD_GITHUB_APP_ID ARD_GITHUB_INSTALLATION_ID private-key.pem" ] || return 1
  # the Deployment reads exactly those keys from that Secret
  [ "$(ctr '.env[] | select(.valueFrom) | .valueFrom.secretKeyRef.name + ":" + .valueFrom.secretKeyRef.key' | sort | paste -sd' ' -)" = \
    "ard-github-app:ARD_GITHUB_APP_ID ard-github-app:ARD_GITHUB_INSTALLATION_ID" ] || return 1
  [ "$(dep '.spec.template.spec.volumes[] | select(.name == "github-app") | .secret.items[0].key')" = "private-key.pem" ] || return 1
}
check "externalsecret ard-github-app renders keys ARD_GITHUB_APP_ID ARD_GITHUB_INSTALLATION_ID private-key.pem" es_ok

helm template x "$SERVE_CHART" --set image.digest="$DIGEST" >"$work/noes.yaml" 2>&1
check "externalSecret disabled renders no ExternalSecret" test "$(yq 'select(.kind == "ExternalSecret") | .metadata.name' "$work/noes.yaml" | grep -c . || true)" = "0"
check "externalSecret enabled without a store refused" bash -c "! helm template x '$SERVE_CHART' --set image.digest=$DIGEST --set githubApp.externalSecret.enabled=true --set githubApp.externalSecret.itemId=$ITEM >/dev/null 2>&1"
check "site values reach the child: storage class and size" \
  test "$(yq 'select(.kind == "PersistentVolumeClaim") | .spec.storageClassName + " " + .spec.resources.requests.storage' "$work/serve.yaml")" = "example-sc 7Gi"

# ---- guards ----------------------------------------------------------------------------------------------------------
check "dryRun false with the default executor refused" bash -c "! helm template x '$SERVE_CHART' --set image.digest=$DIGEST --set instance.dryRun=false >/dev/null 2>&1"
check "missing digest refused" bash -c "! helm template x '$SERVE_CHART' >/dev/null 2>&1"
check "image tag instead of digest refused" bash -c "! helm template x '$SERVE_CHART' --set image.digest=main >/dev/null 2>&1"

# ---- default (executor local) render is unchanged -------------------------------------------------------------------
# deploy/testdata/chart/ard-serve-default.yaml is the render above as it was before the k8s executor objects (#319).
# Regenerate it only for a deliberate change to the default render, and say so in the PR.
check "default render equals deploy/testdata/chart/ard-serve-default.yaml" \
  diff -u "$ROOT/deploy/testdata/chart/ard-serve-default.yaml" "$work/serve.yaml"
kinds() { yq '.kind' "$1" | grep -v '^---$' | sed '/^$/d' | sort | paste -sd, -; }
check "default render has only Deployment, ExternalSecret, NetworkPolicy, PVC" \
  test "$(kinds "$work/serve.yaml")" = "Deployment,ExternalSecret,NetworkPolicy,PersistentVolumeClaim"

# ---- executor k8s --------------------------------------------------------------------------------------------------
AGENT="sha256:$(printf 'fedcba9876543210%.0s' 1 2 3 4)"
cat >"$work/k8s.yaml" <<EOF
image: {digest: "$DIGEST"}
instance: {dryRun: false}
executor: {kind: k8s}
agentImage: {digest: "$AGENT"}
apiServer: [{cidr: 172.18.0.2/32, port: 6443}]
storage: {accessMode: ReadWriteOncePod}
jobs: {githubCIDRs: [140.82.112.0/20, 192.30.252.0/22], modelAPICIDRs: [160.79.104.0/23]}
EOF
if ! helm template ard-albertcbrown-ard "$SERVE_CHART" -f "$work/k8s.yaml" --namespace ard >"$work/k8s-out.yaml" 2>"$work/k8s.err"; then
  echo "ERROR: ard-serve does not render with executor k8s: $(cat "$work/k8s.err")" >&2; exit 1
fi
JNS=ard-jobs-albertcbrown-ard
k() { yq "select($1) | $2" "$work/k8s-out.yaml" | sed -e '/^$/d' -e '/^---$/d'; }
kdep() { k '.kind == "Deployment"' "$1"; }
kctr() { kdep ".spec.template.spec.containers[] | select(.name == \"ard\") | $1"; }
knp() { k ".kind == \"NetworkPolicy\" and .metadata.name == \"$1\"" "$2"; }
labels() { sed 's/: /=/' | sort | paste -sd, -; }

check "k8s render has the executor objects and no Service" test "$(kinds "$work/k8s-out.yaml")" = \
  "Deployment,LimitRange,Namespace,NetworkPolicy,NetworkPolicy,NetworkPolicy,NetworkPolicy,NetworkPolicy,PersistentVolumeClaim,ResourceQuota,Role,RoleBinding,ServiceAccount,ServiceAccount,ValidatingAdmissionPolicy,ValidatingAdmissionPolicyBinding"
check "no Service in any render" bash -c "! grep -q '^kind: Service\$' '$work/serve.yaml' '$work/k8s-out.yaml' '$work/noes.yaml'"

ns_ok() {
  [ "$(k '.kind == "Namespace"' .metadata.name)" = "$JNS" ] || return 1
  [ "$(k '.kind == "Namespace"' '.metadata.labels | to_entries | map(select(.key | test("^pod-security"))) | map(.key + "=" + .value) | sort | join(",")')" = \
    "pod-security.kubernetes.io/audit-version=v1.30,pod-security.kubernetes.io/audit=restricted,pod-security.kubernetes.io/enforce-version=v1.30,pod-security.kubernetes.io/enforce=restricted,pod-security.kubernetes.io/warn-version=v1.30,pod-security.kubernetes.io/warn=restricted" ] || return 1
  # every namespaced executor object lives in the jobs namespace, except the controller's ServiceAccount
  [ "$(k '.kind != "Namespace" and (.kind | test("^ValidatingAdmission") | not) and .metadata.namespace != null' '.metadata.namespace' | sort -u)" = "$JNS" ] || return 1
}
check "jobs namespace enforces Pod Security restricted (v1.30)" ns_ok
check "kubeVersion is >=1.30.0-0" test "$(yq .kubeVersion "$SERVE_CHART/Chart.yaml")" = ">=1.30.0-0"

knp_ok() {
  # ard serve: still no ingress; egress adds the job pods' result port and the apiserver
  local n=ard-albertcbrown-ard
  [ "$(knp $n '.spec.ingress | length')" = "0" ] || return 1
  [ "$(knp $n '.spec.egress | length')" = "4" ] || return 1
  [ "$(knp $n '.spec.egress[2].to[0].namespaceSelector.matchLabels["kubernetes.io/metadata.name"]')" = "$JNS" ] || return 1
  [ "$(knp $n '.spec.egress[2].to[0].podSelector.matchLabels' | labels)" = \
    "app.kubernetes.io/component=ard-job,app.kubernetes.io/instance=ard-albertcbrown-ard,app.kubernetes.io/part-of=ard" ] || return 1
  [ "$(knp $n '.spec.egress[2].ports | map(.protocol + "/" + (.port | tostring)) | join(",")')" = "TCP/8080" ] || return 1
  [ "$(knp $n '.spec.egress[3].to[0].ipBlock.cidr + " " + (.spec.egress[3].ports[0].port | tostring)')" = "172.18.0.2/32 6443" ] || return 1
}
check "ard-serve networkpolicy: no ingress; egress to job result port and apiserver" knp_ok

jnp_ok() {
  # default-deny: everything, both directions, no rules
  [ "$(knp default-deny '.spec | (.podSelector | length | tostring) + " " + (.policyTypes | sort | join(",")) + " " + (has("ingress") | tostring) + (has("egress") | tostring)')" = \
    "0 Egress,Ingress falsefalse" ] || return 1
  local sel; sel="$(kdep '.spec.selector.matchLabels' | labels)"
  # ingress: one peer (the ard-serve pods in the release namespace), one port (the result port)
  [ "$(knp ard-job-ingress '.spec.ingress | length')" = "1" ] || return 1
  [ "$(knp ard-job-ingress '.spec.ingress[0].from | length')" = "1" ] || return 1
  [ "$(knp ard-job-ingress '.spec.ingress[0].from[0].namespaceSelector.matchLabels["kubernetes.io/metadata.name"]')" = "ard" ] || return 1
  [ "$(knp ard-job-ingress '.spec.ingress[0].from[0].podSelector.matchLabels' | labels)" = "$sel" ] || return 1
  [ "$(knp ard-job-ingress '.spec.ingress[0].ports | map(.protocol + "/" + (.port | tostring)) | join(",")')" = "TCP/$(yq .jobs.resultPort "$SERVE_CHART/values.yaml")" ] || return 1
  [ "$(knp ard-job-ingress '.spec.policyTypes | join(",")')" = "Ingress" ] || return 1
  # base egress: DNS, and GitHub plus model API on 443 only
  [ "$(knp ard-job-base-egress '[.spec.egress[].ports[].port] | join(",")')" = "53,53,443" ] || return 1
  [ "$(knp ard-job-base-egress '[.spec.egress[1].to[].ipBlock.cidr] | join(",")')" = "140.82.112.0/20,192.30.252.0/22,160.79.104.0/23" ] || return 1
  [ "$(knp ard-job-base-egress '.spec.policyTypes | join(",")')" = "Egress" ] || return 1
  # internet: only pods not labelled network=false; same except list as ard serve
  [ "$(knp ard-job-internet '.spec.podSelector.matchExpressions | map(.key + " " + .operator + " " + (.values | join(","))) | join(";")')" = \
    "ard.albertcbrown/network NotIn false" ] || return 1
  [ "$(knp ard-job-internet '.spec.egress | length')" = "1" ] || return 1
  [ "$(knp ard-job-internet '.spec.egress[0].to[0].ipBlock.except | sort | join(",")')" = \
    "$(knp ard-albertcbrown-ard '.spec.egress[1].to[0].ipBlock.except | sort | join(",")')" ] || return 1
  [ "$(knp ard-job-internet '.spec.egress[0] | has("ports")')" = "false" ] || return 1
  # every job policy selects exactly the job pods
  for n in ard-job-ingress ard-job-base-egress ard-job-internet; do
    [ "$(knp $n '.spec.podSelector.matchLabels' | labels)" = \
      "app.kubernetes.io/component=ard-job,app.kubernetes.io/instance=ard-albertcbrown-ard,app.kubernetes.io/part-of=ard" ] || return 1
  done
}
check "jobs networkpolicies: default-deny, ingress from ard serve on the result port, DNS/GitHub/model egress, internet" jnp_ok

rbac_ok() {
  # exactly the calls of internal/executor/kube/api.Client that internal/executor/kube makes
  [ "$(k '.kind == "Role"' '.rules[] | (.apiGroups | join(",")) + "|" + (.resources | join(",")) + "|" + (.verbs | sort | join(","))' | sort | paste -sd' ' -)" = \
    "|pods/log|get |pods|create,delete,get,list |secrets|create,delete" ] || return 1
  [ "$(k '.kind == "Role"' '.rules[].verbs[]' | grep -Ec '^(\*|watch|patch|update|deletecollection|escalate|bind|impersonate)$')" = "0" ] || return 1
  [ "$(k '.kind == "Role"' '.rules[].resources[]' | grep -Ec '^(\*|pods/exec|pods/attach|pods/portforward|services|persistentvolumeclaims)$')" = "0" ] || return 1
  [ "$(k '.kind == "RoleBinding"' '.roleRef.kind + "/" + .roleRef.name')" = "Role/$(k '.kind == "Role"' .metadata.name)" ] || return 1
  [ "$(k '.kind == "RoleBinding"' '.subjects | map(.kind + "/" + .namespace + "/" + .name) | join(",")')" = "ServiceAccount/ard/ard-albertcbrown-ard" ] || return 1
  [ "$(kdep .spec.template.spec.serviceAccountName)" = "ard-albertcbrown-ard" ] || return 1
  [ "$(k '.kind == "ServiceAccount"' '(.metadata.namespace // "-") + "/" + .metadata.name + "=" + (.automountServiceAccountToken | tostring)' | sort | paste -sd, -)" = \
    "-/ard-albertcbrown-ard=false,$JNS/ard-job=false" ] || return 1
}
check "Role is pods create/delete/get/list, pods/log get, secrets create/delete; bound to the controller SA" rbac_ok

kdep_ok() {
  [ "$(kdep '.spec.template.spec.automountServiceAccountToken')" = "false" ] || return 1
  # the only Secret the controller mounts is the App key; the API token is a projected, expiring token
  [ "$(kdep '.spec.template.spec.volumes[] | select(.secret) | .name')" = "github-app" ] || return 1
  [ "$(kdep '.spec.template.spec.volumes[] | select(.projected) | .name + " " + (.projected.sources[] | select(.serviceAccountToken) | .serviceAccountToken.expirationSeconds | tostring)')" = "kube-api-access 3600" ] || return 1
  [ "$(kctr '.volumeMounts[] | select(.name == "kube-api-access") | .mountPath + " " + (.readOnly | tostring)')" = "/var/run/secrets/kubernetes.io/serviceaccount true" ] || return 1
  [ "$(kctr '.ports // [] | length')" = "0" ] || return 1
  [ "$(kctr '.args | join(" ")')" = "serve --repo albertcbrown/ard --loops scanners,stale,reaper" ] || return 1
  [ "$(kctr '.env[] | select(.name | test("^ARD_K8S_")) | .name + "=" + .value' | sort | paste -sd' ' -)" = \
    "ARD_K8S_AGENT_IMAGE=ghcr.io/albertcbrown/ard-agent@$AGENT ARD_K8S_INSTANCE=ard-albertcbrown-ard ARD_K8S_NAMESPACE=$JNS ARD_K8S_POD_IMAGE=ghcr.io/albertcbrown/ard@$DIGEST" ] || return 1
  [ "$(k '.kind == "PersistentVolumeClaim"' '.spec.accessModes | join(",")')" = "ReadWriteOncePod" ] || return 1
}
check "live k8s deployment: no --dry-run, projected token, ARD_K8S_* env, ReadWriteOncePod home" kdep_ok

quota_ok() {
  [ "$(k '.kind == "ResourceQuota"' '.spec.hard | keys | sort | join(",")')" = \
    "limits.ephemeral-storage,limits.memory,pods,requests.cpu,requests.ephemeral-storage,requests.memory,secrets" ] || return 1
  [ "$(k '.kind == "LimitRange"' '.spec.limits | map(.type + ":" + (keys | sort | join("/"))) | join(",")')" = "Container:default/defaultRequest/max/type" ] || return 1
}
check "jobs namespace has a ResourceQuota and a Container LimitRange" quota_ok

vap_ok() {
  local vap='.kind == "ValidatingAdmissionPolicy"' vapb='.kind == "ValidatingAdmissionPolicyBinding"'
  [ "$(k "$vap" '.spec.failurePolicy')" = "Fail" ] || return 1
  [ "$(k "$vap" '.spec.matchConstraints.resourceRules | map((.operations | join(",")) + ":" + (.resources | join(","))) | join(" ")')" = \
    "CREATE:pods UPDATE:pods/ephemeralcontainers" ] || return 1
  [ "$(k "$vapb" '.spec.policyName')" = "$(k "$vap" .metadata.name)" ] || return 1
  [ "$(k "$vapb" '.spec.validationActions | join(",")')" = "Deny" ] || return 1
  [ "$(k "$vapb" '.spec.matchResources.namespaceSelector.matchLabels["kubernetes.io/metadata.name"]')" = "$JNS" ] || return 1
  # the UIDs the policy pins are the chart's, and every rule carries a message
  [ "$(k "$vap" '.spec.validations[] | select(.message | test("uid")) | .expression' | grep -Ec "'agent' \\? 65532 : 65533")" = "1" ] || return 1
  [ "$(k "$vap" '.spec.validations | map(select((.message // "") == "")) | length')" = "0" ] || return 1
}
check "ValidatingAdmissionPolicy fails closed and binds to the jobs namespace with Deny" vap_ok

helm template x "$SERVE_CHART" -f "$work/k8s.yaml" --set instance.dryRun=true --set storage.accessMode=ReadWriteOnce >"$work/k8s-dry.yaml" 2>&1
check "k8s with dryRun true keeps --dry-run and allows ReadWriteOnce" \
  test "$(yq 'select(.kind == "Deployment") | .spec.template.spec.containers[0].args[1]' "$work/k8s-dry.yaml")" = "--dry-run"

# ---- ard-root passes executor and the agent image pin through ------------------------------------------------------
cat >"$work/root-k8s.yaml" <<EOF
agentImage: {digest: "$AGENT"}
instances: [{name: albertcbrown-ard, repo: albertcbrown/ard, dryRun: false, executor: {kind: k8s}}]
EOF
helm template ard-root "$ROOT_CHART" -f "$work/pin.yaml" -f "$work/site.yaml" -f "$work/root-k8s.yaml" >"$work/root-k8s-out.yaml" 2>&1
check "ard-root passes executor and agentImage to the child" test \
  "$(yq 'select(.kind == "Application") | .spec.source.helm.valuesObject | .executor.kind + " " + .agentImage.digest + " " + (.instance.dryRun | tostring)' "$work/root-k8s-out.yaml")" = "k8s $AGENT false"
check "ard-root refuses a malformed agent digest" bash -c "! helm template r '$ROOT_CHART' -f '$work/pin.yaml' --set agentImage.digest=sha256:abc >/dev/null 2>&1"

# ---- executor guards --------------------------------------------------------------------------------------------------
# Refused by the chart's own guard (a fail with a message), not by some other template error.
by_guard() { ! helm template x "$SERVE_CHART" -f "$work/k8s.yaml" "$@" >/dev/null 2>"$work/refused.err" && grep -q 'execution error' "$work/refused.err"; }
refused() { local label="$1"; shift; check "$label refused" by_guard "$@"; }
refused "live with executor local" --set executor.kind=local
refused "unknown executor" --set executor.kind=docker
refused "k8s without apiServer" --set 'apiServer=null'
refused "k8s without githubCIDRs" --set 'jobs.githubCIDRs=null'
refused "live with ReadWriteOnce home" --set storage.accessMode=ReadWriteOnce
refused "k8s without an agent digest" --set agentImage.digest=
refused "agent image by tag" --set agentImage.digest=latest
refused "agent image repository with a tag" --set agentImage.repository=ghcr.io/albertcbrown/ard-agent:v1
refused "equal agent and sidecar UIDs" --set jobs.sidecarUID=65532
refused "non-integer UID" --set jobs.agentUID=root
refused "non-integer result port" --set jobs.resultPort=http
refused "a githubCIDRs entry that is not an IPv4 CIDR" --set 'jobs.githubCIDRs={140.82.112.0/20,github.com}'
refused "an apiServer cidr that is not an IPv4 CIDR" --set 'apiServer[0].cidr=kubernetes.default'
refused "jobs namespace equal to the release namespace" --namespace ard --set executor.jobsNamespace=ard
refused "an instance name that is not a DNS label" --set instance.name=Bad_Name


# ---- the agent image pin with executor local (#320) ----------------------------------------------------------------
# pin.yaml carries an agentImage pin: ard-root passes it on, and executor local renders no ARD_K8S_* env for it.
check "child passes the agent image pin to ard-serve" \
  test "$(child '.spec.source.helm.valuesObject.agentImage.repository + "@" + .spec.source.helm.valuesObject.agentImage.digest')" = "ghcr.io/albertcbrown/ard-agent@$AGENT_DIGEST"
check "executor local renders no ARD_K8S_* env even with an agent pin" \
  test "$(ctr '.env[] | select(.name | test("^ARD_K8S_")) | .name' | grep -c . || true)" = "0"
check "ard-root without an agent pin passes none to the child" \
  bash -c "helm template r '$ROOT_CHART' --set image.digest=$DIGEST --set image.commit=$COMMIT | yq 'select(.kind == \"Application\") | .spec.source.helm.valuesObject | has(\"agentImage\")' | grep -qx false"
check "k8s pod spec has no imagePullSecrets (public images)" test "$(kdep '.spec.template.spec.imagePullSecrets // [] | length')" = "0"

[ "$failures" -eq 0 ] || { echo "$failures case(s) failed" >&2; exit 1; }

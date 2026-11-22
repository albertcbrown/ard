#!/usr/bin/env bash
# Offline tests of the two Argo CD charts under deploy/:
#   deploy/argocd            ard-root, the app of apps: one child Application per instance, at the pinned commit
#   deploy/charts/ard-serve  one `ard serve --dry-run` (Deployment, PVC, NetworkPolicy, ExternalSecret)
# ard-serve is rendered with the valuesObject the ard-root child passes, as Argo CD would.
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

# ---- ard-root with a pin and site values, as a deployment repository gives them -----------------------------------
cat >"$work/pin.yaml" <<EOF
image: {repository: ghcr.io/albertcbrown/ard, digest: "$DIGEST", commit: "$COMMIT"}
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
  test "$args" = "serve --dry-run --repo albertcbrown/ard"
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
check "dryRun false refused" bash -c "! helm template x '$SERVE_CHART' --set image.digest=$DIGEST --set instance.dryRun=false >/dev/null 2>&1"
check "missing digest refused" bash -c "! helm template x '$SERVE_CHART' >/dev/null 2>&1"
check "image tag instead of digest refused" bash -c "! helm template x '$SERVE_CHART' --set image.digest=main >/dev/null 2>&1"

[ "$failures" -eq 0 ] || { echo "$failures case(s) failed" >&2; exit 1; }

# Role images on the Kubernetes executor (#245, #448)

How to turn on in-cluster role image builds for an `ard serve` that runs the
Kubernetes executor. With it on, a role that has a `Dockerfile` (the starter
scanners and `reviewers/security-review`) runs on its own image instead of the
plain agent image. See
[docs/proposal.md: Executors and the Reaper](../proposal.md#executors-and-the-reaper)
and [Delivery](../proposal.md#delivery).

The chart side is #448; ard serve (#449) uses it: when a job's role has a
`Dockerfile`, it checks the registry for the role's content tag, builds the
image in a builder pod if it is missing, and runs the job on the image by
digest. A failed or timed-out build fails the job with its cause.

## What the chart creates

With `roleImages.enabled: true` and `executor.kind: k8s`:

- the namespace `ard-build-<instance>` (or `roleImages.buildNamespace`), with
  default-deny NetworkPolicies plus DNS and internet egress for builder pods,
  a ResourceQuota and a LimitRange;
- the `ard-build` ServiceAccount (no token) and a Role for ard serve: pods
  create/delete/get/list, pods/log get, secrets create/delete;
- a ValidatingAdmissionPolicy that admits only builder pods: one `buildkit`
  container running exactly `roleImages.builder.image` as
  `roleImages.builder.uid`, not privileged, no host namespaces, mounting only
  the push Secret and its own Secret;
- optionally an ExternalSecret for the push credential.

The build namespace enforces Pod Security **privileged** (and warns and audits
at baseline): rootless BuildKit needs seccomp and AppArmor Unconfined to make
user namespaces. The admission policy is what keeps that namespace narrow.

## 1. Create the push token

ghcr accepts pushes only from a classic personal access token (or
`GITHUB_TOKEN` in Actions). A GitHub App token or a fine-grained PAT cannot
push packages.

1. As the account that owns `ghcr.io/<owner>`, create a classic token with
   only `write:packages` (it implies `read:packages`). Give it an expiry and
   note it in your calendar.
2. Store it in the secret store the cluster reads, as one item with two
   fields: `username` (the GitHub login) and `token`.

Never paste the token into values files, the deployment repository or an
issue.

## 2. Allow the namespace in Argo CD

The deployment repository's AppProject must allow the build namespace as a
destination, next to the jobs namespace. The cluster-scoped kinds
(`Namespace`, `ValidatingAdmissionPolicy`, `ValidatingAdmissionPolicyBinding`)
are already allowed for the jobs namespace.

## 3. Set the values

In the deployment repository's site values (or one instance's):

```yaml
site:
  roleImages:
    enabled: true
    repository: ghcr.io/<owner>/ard
    pushSecret:
      secretName: ard-role-image-push
      externalSecret:
        enabled: true
        storeName: <store>
        itemId: <item id>
        itemTitle: ghcr role image push
```

To create the Secret yourself instead, leave `externalSecret.enabled` false
and create a `kubernetes.io/dockerconfigjson` Secret of that name in the build
namespace.

Keep `roleImages.builder.image` at the chart's pin unless you are upgrading
BuildKit; it must stay `<repository>@sha256:<digest>`.

## 4. Make the packages public

Role images are pulled with no `imagePullSecrets`, so each package must be
public. A new ghcr package under a user account starts private. After the
first build of each role pushes `ghcr.io/<owner>/ard/<type>-<name>`:

1. Open the package on GitHub (Your profile, Packages).
2. Package settings, Danger Zone, Change visibility, Public.

Until a package is public, a job pod that uses it fails with `ImagePullBackOff`.

Do not publish an image you may not redistribute. The `trufflehog` scanners
are AGPL and stay off under the Kubernetes executor; do not enable them there.

## 5. Check

```sh
kubectl get ns ard-build-<instance> --show-labels
kubectl -n ard-build-<instance> get secret ard-role-image-push -o jsonpath='{.type}'
kubectl get validatingadmissionpolicy ard-<instance>-build
```

The Secret's type must be `kubernetes.io/dockerconfigjson`. Do not print its
data.

## Rotate or turn off

- **Rotate:** update the token in the store; the ExternalSecret refreshes
  within `refreshInterval` (4h by default). Revoke the old token afterwards.
- **Turn off:** set `roleImages.enabled: false`. Argo CD prunes the build
  namespace and its Secret, and roles run on the agent image again.

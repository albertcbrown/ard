//go:build integration && unix

// Package e2e is the Kubernetes executor's end-to-end test on a kind cluster
// (make kind-e2e). It is opt-in: every TestKind* entry point skips unless
// ARD_KIND_E2E=1, and with it set a missing tool or a CNI that does not enforce
// NetworkPolicy fails the test.
//
// The controller must run in-cluster (job ingress admits only ard-serve's pods),
// so the test runs in two modes. The outer mode (TestKind, on the host) creates
// the cluster, builds and pushes the ard image and a harness image (the ard image
// plus this test binary), installs the chart and scales its Deployment to zero.
// It then runs each inner test (TestInner*, ARD_E2E_INNER=1) as a Pod made from
// the Deployment's pod template, so the harness has the chart's labels, ServiceAccount,
// projected token and volume. The inner mode drives the Kubernetes executor with a
// stub agent against a fixture repository served by the harness itself.
//
// Environment: ARD_KIND_E2E=1 (required); ARD_KIND_CLUSTER (use this existing kind
// cluster, which must already trust the registry named by ARD_KIND_REGISTRY);
// ARD_KIND_REGISTRY (default localhost:5001); ARD_KIND_NODE_IMAGE (kind node
// image, default kind's own); ARD_KIND_KEEP=1 (keep cluster and registry).
package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	release    = "e2e"
	instance   = "ard-" + release      // chart: ard-<instance.name>
	ctlNS      = "ard"                 // the chart's namespace
	jobsNS     = "ard-jobs-" + release // the chart's jobs namespace
	fixtureRep = "e2e/fixture"         // owner/name of the fixture repo
	gitPort    = 8081                  // the harness's git server
	testCIDR   = "192.0.2.1/32"        // TEST-NET-1: stands in for GitHub's and the model API's ranges
	registry   = "ard-e2e-registry"    // registry container name
	clusterDef = "ard-e2e"
)

// ---------------------------------------------------------------- outer mode

type env struct {
	t        *testing.T
	cluster  string
	registry string // host:port as the host and the nodes both name it
	ardRef   string // registry/ard@sha256:...
	harness  string // registry/harness@sha256:...
	apiSrv   []string
}

func run(t *testing.T, timeout time.Duration, stdin []byte, name string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out), err
}

func must(t *testing.T, timeout time.Duration, stdin []byte, name string, args ...string) string {
	t.Helper()
	out, err := run(t, timeout, stdin, name, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (e *env) kubectl(args ...string) (string, error) {
	return run(e.t, 3*time.Minute, nil, "kubectl", append([]string{"--context", "kind-" + e.cluster}, args...)...)
}

func (e *env) kubectlIn(stdin string, args ...string) (string, error) {
	return run(e.t, 3*time.Minute, []byte(stdin), "kubectl", append([]string{"--context", "kind-" + e.cluster}, args...)...)
}

// dump prints what a human needs after a failure.
func (e *env) dump(ns string) {
	for _, a := range [][]string{{"-n", ns, "get", "pods", "-o", "wide"}, {"-n", ns, "describe", "pods"}, {"-n", ns, "get", "events", "--sort-by=.lastTimestamp"}} {
		out, _ := e.kubectl(a...)
		e.t.Logf("kubectl %s:\n%s", strings.Join(a, " "), out)
	}
}

func TestKind(t *testing.T) {
	if os.Getenv("ARD_KIND_E2E") != "1" {
		t.Skip("set ARD_KIND_E2E=1 to run the kind end-to-end test (needs docker, kind, kubectl and helm)")
	}
	for _, tool := range []string{"docker", "kind", "kubectl", "helm", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("ARD_KIND_E2E=1 but %s is not on PATH", tool)
		}
	}
	e := &env{t: t, cluster: os.Getenv("ARD_KIND_CLUSTER"), registry: os.Getenv("ARD_KIND_REGISTRY")}
	if e.registry == "" {
		e.registry = "localhost:5001"
	}
	if e.cluster == "" {
		e.cluster = clusterDef
		e.createCluster()
	}
	e.buildImages()
	e.install()
	// Scenarios share the cluster and run in this order; each is a subtest.
	t.Run("NetworkPolicyEnforced", e.networkPolicyEnforced)
	t.Run("Admission", e.admission)
	t.Run("HappyPath", func(t *testing.T) { e.inner(t, "TestInnerHappyPath", nil) })
	t.Run("ReadWriteOncePod", e.rwop)
}

func (e *env) createCluster() {
	t := e.t
	if os.Getenv("ARD_KIND_KEEP") != "1" {
		t.Cleanup(func() {
			run(t, 5*time.Minute, nil, "kind", "delete", "cluster", "--name", e.cluster)
			run(t, time.Minute, nil, "docker", "rm", "-f", registry)
		})
	}
	// A leftover registry from a kept run is reused.
	if out, _ := run(t, 30*time.Second, nil, "docker", "inspect", "-f", "{{.State.Running}}", registry); strings.TrimSpace(out) != "true" {
		run(t, time.Minute, nil, "docker", "rm", "-f", registry)
		must(t, 5*time.Minute, nil, "docker", "run", "-d", "--restart=always", "-p", "127.0.0.1:5001:5000", "--name", registry, registryImage)
	}
	img := os.Getenv("ARD_KIND_NODE_IMAGE")
	if img == "" {
		img = kindNodeImage
	}
	cfg := "kind: Cluster\napiVersion: kind.x-k8s.io/v1alpha4\nnodes:\n- role: control-plane\n  image: " + img + "\n"
	f := filepath.Join(t.TempDir(), "kind.yaml")
	if err := os.WriteFile(f, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, 10*time.Minute, nil, "kind", "create", "cluster", "--name", e.cluster, "--config", f, "--wait", "3m")
	// The nodes pull localhost:5001 from the registry container over the kind network.
	run(t, time.Minute, nil, "docker", "network", "connect", "kind", registry)
	for _, n := range strings.Fields(must(t, time.Minute, nil, "kind", "get", "nodes", "--name", e.cluster)) {
		// kind node images since kind 0.20 (checked: kindNodeImage, kind v0.33.0) set
		// config_path = "/etc/containerd/certs.d", so a hosts.toml there is enough and
		// containerd needs no restart. Fail early with a clear message if a custom
		// ARD_KIND_NODE_IMAGE does not, instead of a pull error later.
		if _, err := run(t, time.Minute, nil, "docker", "exec", n, "grep", "-q", `config_path = "/etc/containerd/certs.d"`, "/etc/containerd/config.toml"); err != nil {
			t.Fatalf("node %s: containerd config_path is not /etc/containerd/certs.d (node image %s); the localhost:5001 registry mirror would not apply", n, img)
		}
		must(t, time.Minute, nil, "docker", "exec", n, "mkdir", "-p", "/etc/containerd/certs.d/localhost:5001")
		must(t, time.Minute, []byte("[host.\"http://"+registry+":5000\"]\n"), "docker", "exec", "-i", n, "sh", "-c",
			"cat > /etc/containerd/certs.d/localhost:5001/hosts.toml")
	}
}

// Pinned fixture images (no tags). kindNodeImage is the node image kind v0.33.0
// lists for Kubernetes 1.37 (the chart needs at least 1.30); registryImage is
// registry 2.8.3's index digest. ARD_KIND_NODE_IMAGE overrides the node image.
const (
	kindNodeImage = "kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5"
	registryImage = "registry:2.8.3@sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373"
)

var digestRE = regexp.MustCompile(`digest: (sha256:[0-9a-f]{64})`)

// push pushes tag and returns repo@digest.
func (e *env) push(tag string) string {
	out := must(e.t, 10*time.Minute, nil, "docker", "push", tag)
	m := digestRE.FindStringSubmatch(out)
	if m == nil {
		e.t.Fatalf("no digest in docker push output:\n%s", out)
	}
	return tag[:strings.LastIndex(tag, ":")] + "@" + m[1]
}

func (e *env) buildImages() {
	t := e.t
	root := repoRoot(t)
	plat := "linux/" + runtime.GOARCH
	ard := e.registry + "/ard:e2e"
	must(t, 20*time.Minute, nil, "docker", "build", "--platform", plat, "-t", ard, root)
	e.ardRef = e.push(ard)

	dir := t.TempDir()
	bin := filepath.Join(dir, "e2e.test")
	cmd := exec.Command("go", "test", "-c", "-tags", "integration", "-o", bin, "./internal/executor/kube/e2e/")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build harness binary: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM "+e.ardRef+"\nCOPY e2e.test /usr/local/bin/e2e.test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := e.registry + "/harness:e2e"
	must(t, 10*time.Minute, nil, "docker", "build", "--platform", plat, "-t", h, dir)
	e.harness = e.push(h)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for d := wd; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		if d == filepath.Dir(d) {
			t.Fatal("go.mod not found")
		}
	}
}

func digestOf(ref string) string { return ref[strings.Index(ref, "@")+1:] }
func repoOf(ref string) string   { return ref[:strings.Index(ref, "@")] }

func (e *env) install() {
	t := e.t
	// The API server's address, which NetworkPolicy sees after the Service is resolved.
	out, err := e.kubectl("-n", "default", "get", "endpoints", "kubernetes", "-o", "jsonpath={.subsets[0].addresses[0].ip}:{.subsets[0].ports[0].port}")
	if err != nil {
		t.Fatal(err)
	}
	ip, port, _ := strings.Cut(strings.TrimSpace(out), ":")
	e.apiSrv = []string{ip, port}
	if o, err := e.kubectl("create", "namespace", ctlNS); err != nil && !strings.Contains(o, "AlreadyExists") {
		t.Fatal(err)
	}
	// The chart's App Secret is only mounted, never read: dummy values.
	app := fmt.Sprintf("apiVersion: v1\nkind: Secret\nmetadata: {name: ard-github-app, namespace: %s}\nstringData:\n"+
		"  ARD_GITHUB_APP_ID: \"1\"\n  ARD_GITHUB_INSTALLATION_ID: \"1\"\n  private-key.pem: dummy\n", ctlNS)
	if _, err := e.kubectlIn(app, "apply", "-f", "-"); err != nil {
		t.Fatal(err)
	}
	args := []string{"--kube-context", "kind-" + e.cluster, "upgrade", "--install", release, filepath.Join(repoRoot(t), "deploy/charts/ard-serve"),
		"-n", ctlNS,
		"--set", "instance.name=" + release, "--set", "instance.dryRun=false", "--set", "executor.kind=k8s",
		"--set", "storage.accessMode=ReadWriteOncePod", "--set", "storage.size=1Gi",
		"--set", "image.repository=" + repoOf(e.ardRef), "--set", "image.digest=" + digestOf(e.ardRef),
		"--set", "agentImage.repository=" + repoOf(e.ardRef), "--set", "agentImage.digest=" + digestOf(e.ardRef),
		"--set", "apiServer[0].cidr=" + ip + "/32", "--set", "apiServer[0].port=" + port,
		"--set", "jobs.githubCIDRs[0]=" + testCIDR, "--set", "jobs.modelAPICIDRs[0]=" + testCIDR}
	// No --wait: the Deployment's pod would wait for its PVC's first consumer, and is scaled to zero next.
	must(t, 5*time.Minute, nil, "helm", args...)
	if _, err := e.kubectl("-n", ctlNS, "scale", "deploy", instance, "--replicas=0"); err != nil {
		t.Fatal(err)
	}
	// Test-only fixture, not in the chart: job pods may reach the harness's git port, and the harness accepts them.
	np := fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: e2e-job-egress-git, namespace: %[1]s}
spec:
  podSelector: {matchLabels: {app.kubernetes.io/component: ard-job}}
  policyTypes: [Egress]
  egress:
  - to:
    - namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: %[2]s}}
      podSelector: {matchLabels: {ard-e2e/git: "true"}}
    ports: [{protocol: TCP, port: %[3]d}]
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: {name: e2e-harness-ingress-git, namespace: %[2]s}
spec:
  podSelector: {matchLabels: {ard-e2e/git: "true"}}
  policyTypes: [Ingress]
  ingress:
  - from:
    - namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: %[1]s}}
    ports: [{protocol: TCP, port: %[3]d}]
`, jobsNS, ctlNS, gitPort)
	if _, err := e.kubectlIn(np, "apply", "-f", "-"); err != nil {
		t.Fatal(err)
	}
}

// harnessPod is the chart Deployment's pod, rebuilt as a one-shot Pod that runs one inner test.
func (e *env) harnessPod(name, testName string, extraEnv map[string]string) string {
	t := e.t
	raw, err := e.kubectl("-n", ctlNS, "get", "deploy", instance, "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Spec struct {
			Template struct {
				Metadata map[string]any `json:"metadata"`
				Spec     map[string]any `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}
	spec := d.Spec.Template.Spec
	labels, _ := d.Spec.Template.Metadata["labels"].(map[string]any)
	if labels == nil {
		labels = map[string]any{}
	}
	labels["ard-e2e/git"] = "true"
	spec["restartPolicy"] = "Never"
	c := spec["containers"].([]any)[0].(map[string]any)
	c["image"] = e.harness
	c["command"] = []any{"/usr/local/bin/e2e.test"}
	c["args"] = []any{"-test.run", "^" + testName + "$", "-test.v", "-test.timeout", "15m"}
	envs, _ := c["env"].([]any)
	add := func(n, v string) { envs = append(envs, map[string]any{"name": n, "value": v}) }
	add("ARD_E2E_INNER", "1")
	add("ARD_E2E_GIT_PORT", fmt.Sprint(gitPort))
	add("ARD_K8S_AGENT_IMAGE", e.ardRef) // the stub agent runs in the ard image's sh
	for k, v := range extraEnv {
		add(k, v)
	}
	envs = append(envs, map[string]any{"name": "ARD_E2E_POD_IP", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "status.podIP"}}})
	c["env"] = envs
	pod := map[string]any{"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name, "namespace": ctlNS, "labels": labels}, "spec": spec}
	b, _ := json.Marshal(pod)
	return string(b)
}

func (e *env) phase(ns, name string) string {
	out, _ := e.kubectl("-n", ns, "get", "pod", name, "-o", "jsonpath={.status.phase}")
	return strings.TrimSpace(out)
}

func (e *env) waitPhase(ns, name string, d time.Duration, want ...string) string {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		p := e.phase(ns, name)
		for _, w := range want {
			if p == w {
				return p
			}
		}
		time.Sleep(3 * time.Second)
	}
	return e.phase(ns, name)
}

// startInner creates the harness pod for an inner test.
func (e *env) startInner(t *testing.T, name, testName string, extraEnv map[string]string) {
	t.Helper()
	if _, err := e.kubectlIn(e.harnessPod(name, testName, extraEnv), "apply", "-f", "-"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.kubectl("-n", ctlNS, "delete", "pod", name, "--wait=false", "--grace-period=0", "--force") })
}

// inner runs one inner test to completion and asserts the pod succeeded.
func (e *env) inner(t *testing.T, testName string, extraEnv map[string]string) {
	t.Helper()
	name := "e2e-" + strings.ToLower(strings.TrimPrefix(testName, "TestInner"))
	e.startInner(t, name, testName, extraEnv)
	e.finishInner(t, name)
}

func (e *env) finishInner(t *testing.T, name string) {
	t.Helper()
	p := e.waitPhase(ctlNS, name, 15*time.Minute, "Succeeded", "Failed")
	logs, _ := e.kubectl("-n", ctlNS, "logs", name)
	t.Logf("harness log:\n%s", logs)
	if p != "Succeeded" {
		e.dump(ctlNS)
		e.dump(jobsNS)
		t.Fatalf("harness pod %s ended %q", name, p)
	}
}

// ----------------------------------------------------------------- scenarios

// networkPolicyEnforced fails loudly on a CNI that ignores NetworkPolicy: a
// default-deny egress policy must block a probe that an unrestricted pod passes.
func (e *env) networkPolicyEnforced(t *testing.T) {
	ns := "ard-e2e-probe"
	e.kubectl("create", "namespace", ns)
	t.Cleanup(func() { e.kubectl("delete", "namespace", ns, "--wait=false") })
	target := "https://" + net.JoinHostPort(e.apiSrv[0], e.apiSrv[1]) + "/version"
	probe := func(name string) bool {
		pod := fmt.Sprintf(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":%q,"namespace":%q},"spec":{"restartPolicy":"Never",
"containers":[{"name":"p","image":%q,"command":["curl","-sk","-m","8","-o","/dev/null",%q]}]}}`, name, ns, e.ardRef, target)
		if _, err := e.kubectlIn(pod, "apply", "-f", "-"); err != nil {
			t.Fatal(err)
		}
		p := e.waitPhase(ns, name, 3*time.Minute, "Succeeded", "Failed")
		if p != "Succeeded" && p != "Failed" {
			e.dump(ns)
			t.Fatalf("probe %s ended %q", name, p)
		}
		return p == "Succeeded"
	}
	if !probe("control") {
		t.Fatalf("control probe cannot reach the API server at %s", target)
	}
	deny := fmt.Sprintf("apiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata: {name: default-deny, namespace: %s}\nspec: {podSelector: {}, policyTypes: [Egress]}\n", ns)
	if _, err := e.kubectlIn(deny, "apply", "-f", "-"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Second)
	if probe("denied") {
		t.Fatal("the CNI does not enforce NetworkPolicy: a default-deny egress policy did not block the probe")
	}
}

// admission checks the jobs namespace's policies with server-side dry runs.
func (e *env) admission(t *testing.T) {
	base := func() map[string]any {
		sc := func(uid int) map[string]any {
			return map[string]any{"runAsUser": uid, "runAsNonRoot": true, "allowPrivilegeEscalation": false,
				"capabilities": map[string]any{"drop": []any{"ALL"}}, "seccompProfile": map[string]any{"type": "RuntimeDefault"}}
		}
		ctr := func(name string, uid int) map[string]any {
			return map[string]any{"name": name, "image": e.ardRef, "securityContext": sc(uid)}
		}
		return map[string]any{"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]any{"name": "ard-job-probe", "namespace": jobsNS, "labels": map[string]any{
				"app.kubernetes.io/part-of": "ard", "app.kubernetes.io/component": "ard-job", "app.kubernetes.io/instance": instance,
				"ard.albertcbrown/repo": "x", "ard.albertcbrown/job": "x", "ard.albertcbrown/network": "false"}},
			"spec": map[string]any{"serviceAccountName": "ard-job", "automountServiceAccountToken": false, "restartPolicy": "Never",
				"initContainers": []any{ctr("pre", 65533)},
				"containers":     []any{ctr("agent", 65532), ctr("post", 65533)}}}
	}
	apply := func(p map[string]any) (string, error) {
		b, _ := json.Marshal(p)
		return e.kubectlIn(string(b), "apply", "--dry-run=server", "-f", "-")
	}
	spec := func(p map[string]any) map[string]any { return p["spec"].(map[string]any) }
	ctrs := func(p map[string]any) []any { return spec(p)["containers"].([]any) }
	if out, err := apply(base()); err != nil {
		t.Fatalf("a pod that follows the contract is refused (control): %v\n%s", err, out)
	}
	const vap, psa = "ValidatingAdmissionPolicy", "PodSecurity"
	cases := []struct {
		name string
		want string
		mut  func(p map[string]any)
	}{
		{"ShareProcessNamespace", vap, func(p map[string]any) { spec(p)["shareProcessNamespace"] = true }},
		{"DefaultServiceAccount", vap, func(p map[string]any) { spec(p)["serviceAccountName"] = "default" }},
		{"TagImage", vap, func(p map[string]any) { ctrs(p)[0].(map[string]any)["image"] = "registry.invalid/ard:latest" }},
		{"FourthContainer", vap, func(p map[string]any) {
			spec(p)["containers"] = append(ctrs(p), map[string]any{"name": "extra", "image": e.ardRef})
		}},
		{"EnvFrom", vap, func(p map[string]any) {
			ctrs(p)[0].(map[string]any)["envFrom"] = []any{map[string]any{"secretRef": map[string]any{"name": "x"}}}
		}},
		{"SecretMountOnAgent", vap, func(p map[string]any) {
			spec(p)["volumes"] = []any{map[string]any{"name": "s", "secret": map[string]any{"secretName": "ard-job-probe"}}}
			ctrs(p)[0].(map[string]any)["volumeMounts"] = []any{map[string]any{"name": "s", "mountPath": "/s"}}
		}},
		{"ForeignSecret", vap, func(p map[string]any) {
			spec(p)["volumes"] = []any{map[string]any{"name": "s", "secret": map[string]any{"secretName": "ard-github-app"}}}
		}},
		{"HostPath", "", func(p map[string]any) {
			spec(p)["volumes"] = []any{map[string]any{"name": "h", "hostPath": map[string]any{"path": "/"}}}
		}},
		{"AgentUIDEqualsSidecar", vap, func(p map[string]any) {
			ctrs(p)[0].(map[string]any)["securityContext"].(map[string]any)["runAsUser"] = 65533
		}},
		{"MissingRunAsUser", vap, func(p map[string]any) {
			delete(ctrs(p)[0].(map[string]any)["securityContext"].(map[string]any), "runAsUser")
		}},
		{"Root", psa, func(p map[string]any) {
			c := ctrs(p)[1].(map[string]any)["securityContext"].(map[string]any)
			c["runAsUser"], c["runAsNonRoot"] = 0, false
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := base()
			c.mut(p)
			out, err := apply(p)
			if err == nil {
				t.Fatalf("pod was admitted:\n%s", out)
			}
			if c.want == "" && !strings.Contains(out, vap) && !strings.Contains(out, psa) {
				t.Fatalf("refused, but not by a policy:\n%s", out)
			}
			if c.want != "" && !strings.Contains(out, c.want) {
				t.Fatalf("refused, but not by %s:\n%s", c.want, out)
			}
		})
	}
}

// rwop starts a harness pod that holds the -home claim and checks that a second pod on
// the claim stays Pending. kind's default provisioner is not a CSI driver, so an
// accepted second pod is recorded, not failed.
func (e *env) rwop(t *testing.T) {
	e.startInner(t, "e2e-rwop-holder", "TestInnerHold", nil)
	if p := e.waitPhase(ctlNS, "e2e-rwop-holder", 5*time.Minute, "Running", "Succeeded", "Failed"); p != "Running" {
		e.dump(ctlNS)
		t.Fatalf("holder ended %q before the claim was in use", p)
	}
	second := fmt.Sprintf(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"e2e-rwop-second","namespace":%q},"spec":{"restartPolicy":"Never",
"securityContext":{"runAsNonRoot":true,"runAsUser":65532,"fsGroup":65532},
"containers":[{"name":"c","image":%q,"command":["sleep","60"]}],
"volumes":[{"name":"home","persistentVolumeClaim":{"claimName":%q}}]}}`, ctlNS, e.ardRef, instance+"-home")
	if _, err := e.kubectlIn(second, "apply", "-f", "-"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		e.kubectl("-n", ctlNS, "delete", "pod", "e2e-rwop-second", "--wait=false", "--grace-period=0", "--force")
	})
	time.Sleep(30 * time.Second)
	switch p := e.phase(ctlNS, "e2e-rwop-second"); p {
	case "Pending":
	case "Running", "Succeeded":
		t.Logf("INFO: ReadWriteOncePod is not enforced by this cluster's storage (second pod is %s); needs a CSI driver", p)
	default:
		t.Fatalf("second pod phase %q", p)
	}
	e.finishInner(t, "e2e-rwop-holder")
}

// ---------------------------------------------------------------- inner mode

// gitServer serves repoRoot (…/<owner>/<name>.git) over smart HTTP, read-only, to
// callers that present a token the fake minted and has not revoked.
func gitServer(t *testing.T, root string, valid func(token string) bool, seen *[]string) (addr string) {
	t.Helper()
	out, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Fatal(err)
	}
	backend := filepath.Join(strings.TrimSpace(string(out)), "git-http-backend")
	h := &cgi.Handler{Path: backend, Root: "/", InheritEnv: []string{"PATH", "HOME"},
		Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "GIT_CONFIG_NOSYSTEM=1"}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tok := ""
		if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Basic ") {
			if b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(a, "Basic ")); err == nil {
				_, tok, _ = strings.Cut(string(b), ":")
			}
		}
		if strings.Contains(r.URL.RawQuery, "git-receive-pack") || strings.HasSuffix(r.URL.Path, "git-receive-pack") || !valid(tok) {
			w.Header().Set("WWW-Authenticate", `Basic realm="e2e"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
	ln, err := net.Listen("tcp", ":"+envOr("ARD_E2E_GIT_PORT", fmt.Sprint(gitPort)))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return "http://" + net.JoinHostPort(os.Getenv("ARD_E2E_POD_IP"), fmt.Sprint(ln.Addr().(*net.TCPAddr).Port))
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

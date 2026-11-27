package kube

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/kube/api"
	"github.com/albertcbrown/ard/internal/redact"
)

// ErrRoleImageBuild wraps every failure to produce a role image.
var ErrRoleImageBuild = errors.New("role image build failed")

// DefaultAgentBase is the agent image a role Dockerfile builds on in the cluster.
const DefaultAgentBase = "ghcr.io/albertcbrown/ard/agent:latest"

// RoleImages turns on in-cluster role image builds (#245, #449). The builder
// pod shape is enforced by the chart's admission policy (#448).
type RoleImages struct {
	Namespace    string // build namespace
	BuilderImage string // pinned by digest; the admission policy admits exactly this
	BuilderUID   int64
	Repository   string // e.g. ghcr.io/owner/ard; images are <Repository>/<type>-<name>
	PushSecret   string // name of the dockerconfigjson Secret in Namespace
	AgentBase    string // default DefaultAgentBase
	Budget       time.Duration
	// Registry overrides the registry base URL (default https://<host of Repository>).
	Registry   string
	MaxContext int64 // default 512 KiB
}

// TreeEntry is one file of a role directory at a commit.
type TreeEntry struct{ Mode, Blob, Path string }

// RoleTree reads a role directory from the controller's clone at a commit. It
// never reads a worktree.
type RoleTree interface {
	LsTree(ctx context.Context, repo, sha, path string) ([]TreeEntry, error)
	// Archive returns the directory as a tar stream; more than max bytes is an error.
	Archive(ctx context.Context, repo, sha, path string, max int64) ([]byte, error)
}

// ExecTree is the RoleTree that runs git. bin "" means "git".
func ExecTree(bin string) RoleTree {
	if bin == "" {
		bin = "git"
	}
	return execTree{bin}
}

type execTree struct{ bin string }

func (g execTree) run(ctx context.Context, repo string, max int64, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, g.bin, append([]string{"-C", repo}, args...)...)
	c.Env = []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &limitWriter{w: &out, max: max}, &errb
	if err := c.Run(); err != nil {
		if errors.Is(err, errTooBig) {
			return nil, errTooBig
		}
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

var errTooBig = errors.New("role directory is larger than the build context limit")

type limitWriter struct {
	w   io.Writer
	max int64
	n   int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	l.n += int64(len(p))
	if l.max > 0 && l.n > l.max {
		return 0, errTooBig
	}
	return l.w.Write(p)
}

func (g execTree) LsTree(ctx context.Context, repo, sha, path string) ([]TreeEntry, error) {
	b, err := g.run(ctx, repo, 8<<20, "ls-tree", "-r", "-z", sha, "--", path)
	if err != nil {
		return nil, err
	}
	var out []TreeEntry
	for _, rec := range strings.Split(string(b), "\x00") {
		if rec == "" {
			continue
		}
		meta, p, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return nil, fmt.Errorf("git ls-tree: unexpected record %q", rec)
		}
		rel := strings.TrimPrefix(p, strings.TrimSuffix(path, "/")+"/")
		out = append(out, TreeEntry{Mode: f[0], Blob: f[2], Path: rel})
	}
	return out, nil
}

func (g execTree) Archive(ctx context.Context, repo, sha, path string, max int64) ([]byte, error) {
	return g.run(ctx, repo, max, "archive", "--format=tar", sha+":"+path)
}

var segRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

var roleTypes = map[string]bool{"scanners": true, "triage": true, "workers": true, "reviewers": true}

// roleName maps the role path .ard/<type>/<name> to "<type>-<name>".
func roleName(path string) (string, error) {
	p := strings.Split(path, "/")
	if len(p) != 3 || p[0] != ".ard" || !roleTypes[p[1]] || !segRE.MatchString(p[2]) {
		return "", fmt.Errorf("%w: role path %q is not .ard/<type>/<name>", executor.ErrInvalidJob, path)
	}
	return p[1] + "-" + p[2], nil
}

// roleTag is the content tag: a hash of the sorted tree entries and the base.
func roleTag(entries []TreeEntry, base string) string {
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, e.Mode+" "+e.Path+" "+e.Blob+"\n")
	}
	sort.Strings(lines)
	h := sha256.New()
	fmt.Fprintf(h, "base %s\n", base)
	for _, l := range lines {
		h.Write([]byte(l))
	}
	return "h-" + hex.EncodeToString(h.Sum(nil))[:32]
}

var digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type buildCall struct {
	done chan struct{}
	ref  string
	err  error
}

// roleImage returns the digest-pinned image for the job's role Dockerfile,
// building it in the cluster when the registry has no image for the tag.
func (e *Executor) roleImage(ctx context.Context, j executor.Job) (string, error) {
	ri := e.o.RoleImages
	name, err := roleName(j.RoleSource.Path)
	if err != nil {
		return "", err
	}
	entries, err := e.o.RoleTree.LsTree(ctx, j.RepoDir, j.RoleSource.SHA, j.RoleSource.Path)
	if err != nil {
		return "", fmt.Errorf("%w: list role directory: %w", ErrRoleImageBuild, err)
	}
	hasDockerfile := false
	for _, t := range entries {
		if t.Mode == "120000" || t.Mode == "160000" {
			return "", fmt.Errorf("%w: role directory has a symlink or submodule at %s", ErrRoleImageBuild, t.Path)
		}
		if t.Path == "Dockerfile" {
			hasDockerfile = true
		}
	}
	if !hasDockerfile {
		return "", fmt.Errorf("%w: role directory has no Dockerfile at %s", ErrRoleImageBuild, j.RoleSource.SHA)
	}
	tag := roleTag(entries, ri.AgentBase)
	repo := ri.Repository + "/" + name

	e.mu.Lock()
	if c, ok := e.builds[tag+repo]; ok {
		e.mu.Unlock()
		select {
		case <-c.done:
			return c.ref, c.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	c := &buildCall{done: make(chan struct{})}
	e.builds[tag+repo] = c
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.builds, tag+repo)
		e.mu.Unlock()
		close(c.done)
	}()
	// The build outlives the first caller's cancellation so that others waiting on it still get a result.
	c.ref, c.err = e.resolveImage(context.WithoutCancel(ctx), j, repo, tag)
	return c.ref, c.err
}

func (e *Executor) resolveImage(ctx context.Context, j executor.Job, repo, tag string) (string, error) {
	ri := e.o.RoleImages
	digest, err := e.registryDigest(ctx, repo, tag)
	if err != nil {
		return "", fmt.Errorf("%w: registry check: %w", ErrRoleImageBuild, err)
	}
	if digest != "" {
		e.o.Log.Info("kube.role_image", "job", j.ID, "image", repo+"@"+digest, "result", "cached")
		return repo + "@" + digest, nil
	}
	tarball, err := e.o.RoleTree.Archive(ctx, j.RepoDir, j.RoleSource.SHA, j.RoleSource.Path, ri.MaxContext)
	if err != nil {
		return "", fmt.Errorf("%w: export role directory: %w", ErrRoleImageBuild, err)
	}
	digest, err = e.runBuilder(ctx, repo, tag, tarball)
	if err != nil {
		return "", err
	}
	e.o.Log.Info("kube.role_image", "job", j.ID, "image", repo+"@"+digest, "result", "built")
	return repo + "@" + digest, nil
}

// registryDigest returns the digest of repo:tag, or "" when it is not there
// (a package that is still private looks the same as a missing one).
func (e *Executor) registryDigest(ctx context.Context, repo, tag string) (string, error) {
	ri := e.o.RoleImages
	host, path, _ := strings.Cut(repo, "/")
	base := ri.Registry
	if base == "" {
		base = "https://" + host
	}
	get := func(method, u string, hdr map[string]string) (*http.Response, error) {
		rq, err := http.NewRequestWithContext(ctx, method, u, nil)
		if err != nil {
			return nil, err
		}
		for k, v := range hdr {
			rq.Header.Set(k, v)
		}
		return e.o.HTTP.Do(rq)
	}
	hdr := map[string]string{}
	q := url.Values{"service": {host}, "scope": {"repository:" + path + ":pull"}}
	resp, err := get("GET", base+"/token?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	switch {
	case resp.StatusCode == 200:
		var tok struct{ Token string }
		err = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&tok)
		resp.Body.Close()
		if err != nil || tok.Token == "" {
			return "", fmt.Errorf("token response unusable")
		}
		hdr["Authorization"] = "Bearer " + tok.Token
	case resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404:
		resp.Body.Close()
		return "", nil
	default:
		resp.Body.Close()
		return "", fmt.Errorf("token request: status %d", resp.StatusCode)
	}
	hdr["Accept"] = "application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, " +
		"application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json"
	resp, err = get("HEAD", base+"/v2/"+path+"/manifests/"+tag, hdr)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case 200:
		d := resp.Header.Get("Docker-Content-Digest")
		if !digestRE.MatchString(d) {
			return "", fmt.Errorf("registry returned no valid digest")
		}
		return d, nil
	case 401, 403, 404:
		return "", nil
	}
	return "", fmt.Errorf("manifest request: status %d", resp.StatusCode)
}

// builderPod is the pod the chart's admission policy admits (#448).
func (e *Executor) builderPod(name, repo, tag string) *api.Pod {
	ri := e.o.RoleImages
	uid := ri.BuilderUID
	deadline := int64(ri.Budget / time.Second)
	script := `set -e
mkdir -p /ctx
tar -xf /in/context.tar -C /ctx
exec buildctl-daemonless.sh build --frontend dockerfile.v0 --local context=/ctx --local dockerfile=/ctx` +
		` --opt build-arg:AGENT_IMAGE=` + shellQuote(ri.AgentBase) +
		` --output type=image,name=` + shellQuote(repo+":"+tag) + `,push=true --metadata-file /dev/termination-log`
	unconfined := &api.SeccompProfile{Type: "Unconfined"}
	return &api.Pod{
		Meta: api.ObjectMeta{Name: name, Labels: map[string]string{
			"app.kubernetes.io/part-of": "ard", "app.kubernetes.io/component": "ard-build",
			"app.kubernetes.io/instance": e.o.Instance, "ard.dev/role-image": tag,
		}},
		Spec: api.PodSpec{
			Containers: []api.Container{{
				Name: "buildkit", Image: ri.BuilderImage, ImagePullPolicy: "IfNotPresent",
				TerminationMessagePolicy: "File",
				Command:                  []string{"sh", "-c", script},
				Env: []api.EnvVar{{Name: "DOCKER_CONFIG", Value: "/home/user/.docker"},
					{Name: "BUILDKITD_FLAGS", Value: "--oci-worker-no-process-sandbox"}},
				VolumeMounts: []api.VolumeMount{
					{Name: "in", MountPath: "/in", ReadOnly: true},
					{Name: "ctx", MountPath: "/ctx"},
					{Name: "push", MountPath: "/home/user/.docker", ReadOnly: true},
					{Name: "state", MountPath: "/home/user/.local/share/buildkit"},
				},
				SecurityContext: &api.SecurityContext{RunAsUser: ptr(uid), RunAsGroup: ptr(uid),
					AllowPrivilegeEscalation: ptr(true), SeccompProfile: unconfined,
					AppArmorProfile: &api.AppArmorProfile{Type: "Unconfined"}},
			}},
			Volumes: []api.Volume{
				{Name: "in", Secret: &api.SecretVolumeSource{SecretName: name}},
				{Name: "ctx", EmptyDir: &api.EmptyDirVolumeSource{SizeLimit: qty(ri.MaxContext * 8)}},
				{Name: "push", Secret: &api.SecretVolumeSource{SecretName: ri.PushSecret,
					Items: []api.KeyToPath{{Key: ".dockerconfigjson", Path: "config.json"}}}},
				{Name: "state", EmptyDir: &api.EmptyDirVolumeSource{}},
			},
			RestartPolicy: "Never", ActiveDeadlineSeconds: &deadline,
			AutomountServiceAccountToken: ptr(false), EnableServiceLinks: ptr(false),
			ShareProcessNamespace: ptr(false), ServiceAccountName: "ard-build",
			SecurityContext: &api.PodSecurityContext{RunAsUser: ptr(uid), RunAsGroup: ptr(uid), RunAsNonRoot: ptr(true),
				SeccompProfile: unconfined},
		},
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// runBuilder builds the context, waits for the pod within the budget and
// returns the pushed image digest. It always removes the pod and Secret.
func (e *Executor) runBuilder(ctx context.Context, repo, tag string, tarball []byte) (string, error) {
	ri := e.o.RoleImages
	name := "ard-build-" + tag[2:14]
	pod := e.builderPod(name, repo, tag)
	defer func() {
		cctx := context.WithoutCancel(ctx)
		e.o.Client.DeletePod(cctx, ri.Namespace, name, 0)
		e.o.Client.DeleteSecret(cctx, ri.Namespace, name)
	}()
	created, err := e.o.Client.CreatePod(ctx, ri.Namespace, pod)
	if err != nil {
		return "", fmt.Errorf("%w: builder pod refused: %w", ErrRoleImageBuild, err)
	}
	sec := &api.Secret{Meta: api.ObjectMeta{Name: name,
		Labels:          map[string]string{"app.kubernetes.io/part-of": "ard", "app.kubernetes.io/component": "ard-build"},
		OwnerReferences: []api.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: name, UID: created.Meta.UID}}},
		Type: "Opaque", Data: map[string][]byte{"context.tar": tarball}}
	if err := e.o.Client.CreateSecret(ctx, ri.Namespace, sec); err != nil {
		return "", fmt.Errorf("%w: create build context Secret: %w", ErrRoleImageBuild, err)
	}
	deadline := time.NewTimer(ri.Budget)
	defer deadline.Stop()
	tick := time.NewTicker(e.o.Poll)
	defer tick.Stop()
	for {
		p, err := e.o.Client.GetPod(ctx, ri.Namespace, name)
		switch {
		case errors.Is(err, api.ErrNotFound):
			return "", fmt.Errorf("%w: builder pod disappeared", ErrRoleImageBuild)
		case err != nil:
			return "", fmt.Errorf("%w: watch builder pod: %w", ErrRoleImageBuild, err)
		}
		switch p.Status.Phase {
		case "Succeeded":
			return builderDigest(p)
		case "Failed":
			return "", e.buildFailure(ctx, name, p)
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			return "", fmt.Errorf("%w: build timed out after %s", ErrRoleImageBuild, ri.Budget)
		case <-ctx.Done():
			return "", fmt.Errorf("%w: %w", ErrRoleImageBuild, ctx.Err())
		}
	}
}

func builderState(p *api.Pod) *api.StateTerminated {
	for _, s := range p.Status.ContainerStatuses {
		if s.Name == "buildkit" {
			return s.State.Terminated
		}
	}
	return nil
}

func builderDigest(p *api.Pod) (string, error) {
	t := builderState(p)
	if t != nil {
		var m map[string]any
		if json.Unmarshal([]byte(t.Message), &m) == nil {
			if d, _ := m["containerimage.digest"].(string); digestRE.MatchString(d) {
				return d, nil
			}
		}
	}
	return "", fmt.Errorf("%w: no digest reported by the builder", ErrRoleImageBuild)
}

func (e *Executor) buildFailure(ctx context.Context, name string, p *api.Pod) error {
	if p.Status.Reason == "DeadlineExceeded" {
		return fmt.Errorf("%w: build timed out after %s", ErrRoleImageBuild, e.o.RoleImages.Budget)
	}
	code := int32(-1)
	if t := builderState(p); t != nil {
		code = t.ExitCode
	}
	msg := fmt.Sprintf("build failed (exit %d)", code)
	if p.Status.Reason != "" {
		msg += ": " + p.Status.Reason
	}
	if tail := e.logTail(ctx, name); tail != "" {
		msg += ": " + tail
	}
	return fmt.Errorf("%w: %s", ErrRoleImageBuild, msg)
}

// logTail is the last lines of the builder log with secret-looking lines removed.
func (e *Executor) logTail(ctx context.Context, name string) string {
	rc, err := e.o.Client.PodLogs(ctx, e.o.RoleImages.Namespace, name, "buildkit", time.Time{})
	if err != nil {
		return ""
	}
	defer rc.Close()
	var lines []string
	sc := bufio.NewScanner(io.LimitReader(rc, 1<<20))
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() {
		l := sc.Text()
		if _, rest, ok := strings.Cut(l, " "); ok && len(l) > 20 && l[4] == '-' {
			l = rest // drop the timestamp the log API adds
		}
		lines = append(lines, l)
	}
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	text := strings.Join(lines, "\n")
	if fs, err := redact.Builtin().Scan(ctx, strings.NewReader(text)); err != nil {
		return "(log withheld: scan failed)"
	} else if len(fs) > 0 {
		bad := map[int]bool{}
		for _, f := range fs {
			bad[f.Line] = true
		}
		for i := range lines {
			if bad[i+1] {
				lines[i] = "[redacted]"
			}
		}
	}
	return strconv.Quote(strings.Join(lines, "\n"))
}

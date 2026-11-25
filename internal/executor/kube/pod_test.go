//go:build unix

package kube

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/kube/api"
	"github.com/albertcbrown/ard/internal/jobpod"
)

var labelRE = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9._-]{0,61}[A-Za-z0-9])?)?$`)

func podFor(h executor.Handle, c executor.Command) *api.Pod {
	o := Options{Namespace: ns, Repo: "o/r", Instance: "ard-test", AgentImage: "a@sha256:1", PodImage: "p@sha256:2",
		GitBase: "https://github.com", Resources: DefaultResources(), PreBudget: 5 * time.Minute, ResultGrace: 2 * time.Minute}
	return buildPod(h, c, o)
}

func TestBuildPod(t *testing.T) {
	start := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	base := executor.Handle{JobID: "01HZZZZZZZZZZZZZZZZZZZZZZZ", Issue: 7, Stage: "worker", Role: "dev", Kind: Kind,
		View: jobpod.AgentPaths(), StartedAt: start, Timeout: 20 * time.Minute, Deadline: start.Add(25 * time.Minute)}
	base.View.Sidecar, base.View.History = "", ""
	oauth := testCmd()
	oauth.Env = []string{"CLAUDE_CODE_OAUTH_TOKEN=oauth-secret"}
	for _, tc := range []struct {
		name string
		mut  func(*executor.Handle)
		cmd  executor.Command
		cred string
	}{
		{"plain", func(*executor.Handle) {}, testCmd(), envName},
		{"readonly", func(h *executor.Handle) { h.ReadOnly = true }, testCmd(), envName},
		{"sidecar", func(h *executor.Handle) { h.View.Sidecar = jobpod.JobSidecar }, testCmd(), envName},
		{"history", func(h *executor.Handle) { h.View.History = jobpod.JobHistory; h.Stage = "scanner" }, testCmd(), envName},
		{"oauth", func(*executor.Handle) {}, oauth, "CLAUDE_CODE_OAUTH_TOKEN"},
		{"no network", func(h *executor.Handle) { h.Network = executor.NetworkNone }, testCmd(), envName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := base
			tc.mut(&h)
			p := podFor(h, tc.cmd)
			if len(p.Meta.Name) > 63 || p.Meta.Name != jobpod.PodName(h.JobID) {
				t.Errorf("name %q", p.Meta.Name)
			}
			for k, v := range p.Meta.Labels {
				if !labelRE.MatchString(v) || len(v) > 63 {
					t.Errorf("label %s=%q invalid", k, v)
				}
			}
			wantNet := "true"
			if h.Network != executor.NetworkOn {
				wantNet = "false"
			}
			if p.Meta.Labels[jobpod.LabelNetwork] != wantNet || p.Meta.Labels[jobpod.LabelRepo] != jobpod.RepoLabel("o/r") {
				t.Errorf("labels %v", p.Meta.Labels)
			}
			s := p.Spec
			if s.RestartPolicy != "Never" || *s.ActiveDeadlineSeconds != int64((5+20+2)*60) || *s.TerminationGracePeriodSeconds != 30 ||
				*s.AutomountServiceAccountToken || *s.EnableServiceLinks || *s.ShareProcessNamespace || s.ServiceAccountName != "ard-job" {
				t.Errorf("spec %+v", s)
			}
			if sc := s.SecurityContext; !*sc.RunAsNonRoot || *sc.FSGroup != jobpod.GID || sc.SeccompProfile.Type != "RuntimeDefault" || sc.FSGroupChangePolicy != "OnRootMismatch" {
				t.Errorf("pod security context %+v", sc)
			}
			if len(s.InitContainers) != 1 || len(s.Containers) != 2 {
				t.Fatalf("containers: %d init, %d regular", len(s.InitContainers), len(s.Containers))
			}
			pre, agent, post := s.InitContainers[0], s.Containers[0], s.Containers[1]
			if pre.Name != "pre" || agent.Name != "agent" || post.Name != "post" {
				t.Fatalf("names %s %s %s", pre.Name, agent.Name, post.Name)
			}
			for _, c := range []api.Container{pre, agent, post} {
				sc := c.SecurityContext
				if *sc.AllowPrivilegeEscalation || !*sc.ReadOnlyRootFilesystem || sc.Capabilities.Drop[0] != "ALL" {
					t.Errorf("%s security context %+v", c.Name, sc)
				}
			}
			if *pre.SecurityContext.RunAsUser != jobpod.SidecarUID || *post.SecurityContext.RunAsUser != jobpod.SidecarUID ||
				*agent.SecurityContext.RunAsUser != jobpod.AgentUID {
				t.Error("run-as users wrong")
			}
			// Secrets: only pre gets the token, only post the key, agent none.
			vols := map[string]api.Volume{}
			for _, v := range s.Volumes {
				vols[v.Name] = v
			}
			secretVolsOf := func(c api.Container) (items []string) {
				for _, m := range c.VolumeMounts {
					if v := vols[m.Name]; v.Secret != nil {
						for _, it := range v.Secret.Items {
							items = append(items, it.Key)
						}
					}
				}
				return
			}
			if got := strings.Join(secretVolsOf(agent), ","); got != "" {
				t.Errorf("agent mounts secret items %q", got)
			}
			if got := strings.Join(secretVolsOf(pre), ","); got != jobpod.ItemManifest+","+jobpod.ItemGitAccess {
				t.Errorf("pre items %q", got)
			}
			if got := strings.Join(secretVolsOf(post), ","); got != jobpod.ItemResultKey {
				t.Errorf("post items %q", got)
			}
			workRO := false
			for _, m := range agent.VolumeMounts {
				if m.Name == "work" {
					workRO = m.ReadOnly
				}
			}
			if workRO != h.ReadOnly {
				t.Errorf("agent work read-only = %v", workRO)
			}
			if vols["ctl"].EmptyDir.SizeLimit != qty(DefaultResources().Ctl) && h.View.History == "" {
				t.Errorf("ctl size %s", vols["ctl"].EmptyDir.SizeLimit)
			}
			if h.View.History != "" && vols["ctl"].EmptyDir.SizeLimit != qty(8*gi) {
				t.Errorf("history ctl size %s", vols["ctl"].EmptyDir.SizeLimit)
			}
			// The credential only as a secretKeyRef; no literal secret anywhere.
			found := false
			for _, e := range agent.Env {
				if e.Name == tc.cred {
					r := e.ValueFrom.SecretKeyRef
					found = e.Value == "" && r.Key == tc.cred && r.Name == p.Meta.Name
				}
			}
			if !found {
				t.Errorf("credential %s not a secretKeyRef: %+v", tc.cred, agent.Env)
			}
			for _, c := range []api.Container{pre, agent, post} {
				for _, e := range c.Env {
					if strings.Contains(e.Value, "secret") || strings.Contains(e.Value, "tok-") {
						t.Errorf("%s env %s carries a secret", c.Name, e.Name)
					}
				}
				for _, a := range append(c.Command, c.Args...) {
					if strings.Contains(a, "secret") {
						t.Errorf("%s argv carries a secret: %s", c.Name, a)
					}
				}
			}
			// Deadlines: pre 5m, agent 25m minus 2m, post 25m plus 2m.
			if got := strings.Join(pre.Args, " "); !strings.Contains(got, "--deadline 2026-11-25T12:05:00Z") {
				t.Errorf("pre args %q", got)
			}
			if got := strings.Join(agent.Command, " "); !strings.Contains(got, "--deadline 2026-11-25T12:23:00Z -- claude -p") {
				t.Errorf("agent command %q", got)
			}
			if got := strings.Join(post.Args, " "); got != "--deadline 2026-11-25T12:27:00Z" || post.Ports[0].ContainerPort != jobpod.ResultPort {
				t.Errorf("post %v %v", post.Args, post.Ports)
			}
		})
	}
}

func TestBuildSecret(t *testing.T) {
	h := executor.Handle{JobID: "J1"}
	s, err := buildSecret(h, testCmd(), []byte("{}"), "gh-token", "rkey", "uid-9")
	if err != nil {
		t.Fatal(err)
	}
	if s.Meta.OwnerReferences[0].UID != "uid-9" || string(s.Data[envName]) != "sk-secret" ||
		string(s.Data[jobpod.ItemGitAccess]) != "gh-token" || string(s.Data[jobpod.ItemResultKey]) != "rkey" {
		t.Errorf("secret %+v", s)
	}
	bad := testCmd()
	bad.Env = []string{"PATH=/x"}
	if _, err := buildSecret(h, bad, nil, "", "", ""); err == nil {
		t.Error("want error for a non-model credential name")
	}
	none := testCmd()
	none.Env = nil
	if s, err := buildSecret(h, none, []byte("{}"), "t", "k", "u"); err != nil || len(s.Data) != 3 {
		t.Errorf("no-credential secret: %v %v", s, err)
	}
}

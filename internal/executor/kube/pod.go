package kube

import (
	"strconv"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/executor/kube/api"
	"github.com/albertcbrown/ard/internal/jobpod"
)

// Resources are the per-container requests and limits and the emptyDir size
// limits (bytes). The cluster's LimitRange mirrors the defaults.
type Resources struct {
	Agent, Pre, Post api.ResourceRequirements
	Work, Job, Out   int64
	Ctl, CtlHistory  int64
	Bin, Tmp         int64
}

const (
	mi = int64(1) << 20
	gi = int64(1) << 30
)

// DefaultResources returns the ops defaults.
func DefaultResources() Resources {
	return Resources{
		Agent: api.ResourceRequirements{
			Requests: map[string]string{"cpu": "1", "memory": "2Gi"},
			Limits:   map[string]string{"memory": "6Gi"},
		},
		Pre: api.ResourceRequirements{
			Requests: map[string]string{"cpu": "250m", "memory": "256Mi"},
			Limits:   map[string]string{"memory": "1Gi", "ephemeral-storage": "256Mi"},
		},
		Post: api.ResourceRequirements{
			Requests: map[string]string{"cpu": "100m", "memory": "64Mi"},
			Limits:   map[string]string{"memory": "256Mi", "ephemeral-storage": "64Mi"},
		},
		Work: 2 * gi, Job: 4 * gi, Out: 256 * mi, Ctl: 2 * gi, CtlHistory: 8 * gi, Bin: 128 * mi, Tmp: 2 * gi,
	}
}

func ptr[T any](v T) *T { return &v }

func qty(n int64) string { return strconv.FormatInt(n, 10) }

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func hardened(uid int64) *api.SecurityContext {
	return &api.SecurityContext{
		RunAsUser: ptr(uid), RunAsGroup: ptr(int64(jobpod.GID)),
		AllowPrivilegeEscalation: ptr(false), ReadOnlyRootFilesystem: ptr(true),
		Capabilities: &api.Capabilities{Drop: []string{"ALL"}},
	}
}

func mount(name, path string, ro bool) api.VolumeMount {
	return api.VolumeMount{Name: name, MountPath: path, ReadOnly: ro}
}

func clone(m map[string]string) map[string]string {
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

const (
	volSecretPre  = "secret-pre"
	volSecretPost = "secret-post"
)

// buildPod is pure: it maps the handle, command and options to the pod. The
// agent container never mounts a Secret volume; the model credential reaches
// it only as a secretKeyRef.
func buildPod(h executor.Handle, c executor.Command, o Options) *api.Pod {
	r := o.Resources
	name := jobpod.PodName(h.JobID)
	preDeadline := h.StartedAt.Add(o.PreBudget)
	postDeadline := h.Deadline.Add(o.ResultGrace)
	agentDeadline := h.Deadline.Add(-min(2*time.Minute, h.Timeout/10))
	active := int64((o.PreBudget + h.Timeout + o.ResultGrace) / time.Second)
	history := h.View.History != ""

	ctl := r.Ctl
	if history {
		ctl = r.CtlHistory
	}
	empty := func(n string, size int64) api.Volume {
		return api.Volume{Name: n, EmptyDir: &api.EmptyDirVolumeSource{SizeLimit: qty(size)}}
	}
	secretVol := func(n string, items ...string) api.Volume {
		v := &api.SecretVolumeSource{SecretName: name, DefaultMode: ptr(int32(jobpod.SecretMode))}
		for _, it := range items {
			v.Items = append(v.Items, api.KeyToPath{Key: it, Path: it})
		}
		return api.Volume{Name: n, Secret: v}
	}

	pre := api.Container{
		Name: jobpod.ContainerPre, Image: o.PodImage, ImagePullPolicy: "IfNotPresent",
		TerminationMessagePolicy: "File",
		Command:                  []string{"/usr/local/bin/ard", "job-pre"},
		Args:                     []string{"--deadline", stamp(preDeadline), "--git-base", o.GitBase},
		Env: []api.EnvVar{
			{Name: "HOME", Value: "/ctl/home"}, {Name: "TMPDIR", Value: "/ctl/tmp"},
			{Name: "GIT_CONFIG_NOSYSTEM", Value: "1"}, {Name: "GIT_CONFIG_GLOBAL", Value: "/dev/null"},
			{Name: "GIT_TERMINAL_PROMPT", Value: "0"},
		},
		VolumeMounts: []api.VolumeMount{
			mount(jobpod.VolWork, jobpod.MountWork, false), mount(jobpod.VolJob, jobpod.MountJob, false),
			mount(jobpod.VolCtl, jobpod.MountCtl, false), mount(jobpod.VolBin, jobpod.MountBin, false),
			mount(volSecretPre, jobpod.PreSecretDir, true),
		},
		Resources:       api.ResourceRequirements{Requests: clone(r.Pre.Requests), Limits: clone(r.Pre.Limits)},
		SecurityContext: hardened(jobpod.SidecarUID),
	}

	workRO := h.ReadOnly
	env := []api.EnvVar{
		{Name: "HOME", Value: jobpod.AgentHome}, {Name: "TMPDIR", Value: jobpod.AgentTmpdir},
		{Name: "LANG", Value: "C.UTF-8"}, {Name: "GOFLAGS", Value: "-buildvcs=false -modcacherw"},
		{Name: "DISABLE_AUTOUPDATER", Value: "1"},
		{Name: "GIT_TERMINAL_PROMPT", Value: "0"}, {Name: "GIT_CONFIG_NOSYSTEM", Value: "1"},
		{Name: "GIT_CONFIG_GLOBAL", Value: "/dev/null"},
	}
	for _, kv := range executor.ArdEnv(h) {
		n, v, _ := strings.Cut(kv, "=")
		env = append(env, api.EnvVar{Name: n, Value: v})
	}
	for _, kv := range c.Fixed {
		n, v, _ := strings.Cut(kv, "=")
		env = append(env, api.EnvVar{Name: n, Value: v})
	}
	for _, kv := range c.Env {
		n, _, _ := strings.Cut(kv, "=")
		env = append(env, api.EnvVar{Name: n, ValueFrom: &api.EnvVarSource{
			SecretKeyRef: &api.SecretKeySelector{Name: name, Key: n}}})
	}
	agentStore := r.Work + r.Job + r.Out + r.Bin + r.Tmp + gi
	agentRes := api.ResourceRequirements{Requests: clone(r.Agent.Requests), Limits: clone(r.Agent.Limits)}
	agentRes.Requests["ephemeral-storage"] = qty(agentStore)
	agentRes.Limits["ephemeral-storage"] = qty(agentStore)
	cmd := append([]string{jobpod.AgentBin, "job-exec", "--deadline", stamp(agentDeadline), "--", c.Path}, c.Args...)
	sc := hardened(jobpod.AgentUID)
	agentImage := o.AgentImage
	if h.Image != "" { // a role image, pinned by digest
		agentImage = h.Image
	}
	agent := api.Container{
		Name: jobpod.ContainerAgent, Image: agentImage, ImagePullPolicy: "IfNotPresent",
		Command: cmd, Env: env, WorkingDir: jobpod.MountWork,
		VolumeMounts: []api.VolumeMount{
			mount(jobpod.VolWork, jobpod.MountWork, workRO), mount(jobpod.VolJob, jobpod.MountJob, false),
			mount(jobpod.VolOut, jobpod.MountOut, false), mount(jobpod.VolBin, jobpod.MountBin, true),
			mount(jobpod.VolTmp, jobpod.MountTmp, false),
		},
		Resources: agentRes, SecurityContext: sc,
	}

	post := api.Container{
		Name: jobpod.ContainerPost, Image: o.PodImage, ImagePullPolicy: "IfNotPresent",
		Command: []string{"/usr/local/bin/ard", "job-post"},
		Args:    []string{"--deadline", stamp(postDeadline)},
		Ports:   []api.ContainerPort{{Name: jobpod.ResultPortName, ContainerPort: jobpod.ResultPort, Protocol: "TCP"}},
		VolumeMounts: []api.VolumeMount{
			mount(jobpod.VolWork, jobpod.MountWork, true), mount(jobpod.VolOut, jobpod.MountOut, true),
			mount(volSecretPost, jobpod.PostSecretDir, true),
		},
		Resources:       api.ResourceRequirements{Requests: clone(r.Post.Requests), Limits: clone(r.Post.Limits)},
		SecurityContext: hardened(jobpod.SidecarUID),
	}

	labels := map[string]string{
		jobpod.LabelPartOf: "ard", jobpod.LabelComponent: jobpod.ComponentJob,
		jobpod.LabelInstance: jobpod.LabelValue(o.Instance), jobpod.LabelRepo: jobpod.RepoLabel(o.Repo),
		jobpod.LabelJob: jobpod.LabelValue(h.JobID), jobpod.LabelIssue: strconv.Itoa(h.Issue),
		jobpod.LabelStage: jobpod.LabelValue(h.Stage), jobpod.LabelRole: jobpod.LabelValue(h.Role),
		jobpod.LabelNetwork: "true",
	}
	if h.Network != executor.NetworkOn {
		labels[jobpod.LabelNetwork] = "false"
	}
	return &api.Pod{
		Meta: api.ObjectMeta{Name: name, Labels: labels},
		Spec: api.PodSpec{
			InitContainers: []api.Container{pre}, Containers: []api.Container{agent, post},
			Volumes: []api.Volume{
				empty(jobpod.VolWork, r.Work), empty(jobpod.VolJob, r.Job), empty(jobpod.VolOut, r.Out),
				empty(jobpod.VolCtl, ctl), empty(jobpod.VolBin, r.Bin), empty(jobpod.VolTmp, r.Tmp),
				secretVol(volSecretPre, jobpod.ItemManifest, jobpod.ItemGitAccess),
				secretVol(volSecretPost, jobpod.ItemResultKey),
			},
			RestartPolicy:                 "Never",
			ActiveDeadlineSeconds:         &active,
			TerminationGracePeriodSeconds: ptr(int64(30)),
			AutomountServiceAccountToken:  ptr(false),
			EnableServiceLinks:            ptr(false),
			ShareProcessNamespace:         ptr(false),
			ServiceAccountName:            "ard-job",
			SecurityContext: &api.PodSecurityContext{
				RunAsNonRoot: ptr(true), RunAsUser: ptr(int64(jobpod.AgentUID)), RunAsGroup: ptr(int64(jobpod.GID)),
				FSGroup: ptr(int64(jobpod.GID)), FSGroupChangePolicy: "OnRootMismatch",
				SeccompProfile: &api.SeccompProfile{Type: "RuntimeDefault"},
			},
		},
	}
}

// buildSecret builds the per-job Secret, owned by the pod so deleting the pod
// deletes it. The model credential's value comes from c.Env.
func buildSecret(h executor.Handle, c executor.Command, manifest []byte, gitToken, resultKey, podUID string) (*api.Secret, error) {
	data := map[string][]byte{
		jobpod.ItemManifest: manifest, jobpod.ItemGitAccess: []byte(gitToken), jobpod.ItemResultKey: []byte(resultKey),
	}
	if len(c.Env) == 1 {
		n, v, _ := strings.Cut(c.Env[0], "=")
		d, err := jobpod.SecretData(manifest, gitToken, resultKey, n, v)
		if err != nil {
			return nil, err
		}
		data = d
	}
	name := jobpod.PodName(h.JobID)
	return &api.Secret{
		Meta: api.ObjectMeta{Name: name, Labels: map[string]string{
			jobpod.LabelPartOf: "ard", jobpod.LabelComponent: jobpod.ComponentJob, jobpod.LabelJob: jobpod.LabelValue(h.JobID)},
			OwnerReferences: []api.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: name, UID: podUID}}},
		Type: "Opaque", Data: data,
	}, nil
}

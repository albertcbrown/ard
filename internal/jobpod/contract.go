// Package jobpod is the single contract between the controller and a job pod
// of the Kubernetes executor: paths, Secret items, the input manifest, the
// result route and key helpers. The executor (#317) and the pod side (#316)
// both import it and never each other. It imports only executor and gitref.
package jobpod

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/albertcbrown/ard/internal/executor"
)

// Containers.
const (
	ContainerPre   = "pre"
	ContainerAgent = "agent"
	ContainerPost  = "post"
)

// Users and groups.
const (
	AgentUID   = 65532
	SidecarUID = 65533 // pre and post
	GID        = 65532 // fsGroup
)

// Volume mount paths.
const (
	MountWork   = "/work"    // tree without .git: pre rw, agent rw or ro, post ro
	MountJob    = "/job"     // pre and agent
	MountOut    = "/out"     // agent rw, post ro
	MountCtl    = "/ctl"     // pre only: git dirs and pre's HOME
	MountBin    = "/ard-bin" // pre rw, agent ro
	MountTmp    = "/tmp"     // agent only
	AgentBin    = "/ard-bin/ard"
	JobHome     = "/job/home"
	JobRole     = "/job/role"
	JobSidecar  = "/job/sidecar"
	JobHistory  = "/job/history"
	AgentConfig = "/job/home/.claude"
)

// Volume names.
const (
	VolWork = "work"
	VolJob  = "job"
	VolOut  = "out"
	VolCtl  = "ctl"
	VolBin  = "ard-bin"
	VolTmp  = "tmp"
)

// AgentPaths is the agent's view of the job, as executor.Paths.
func AgentPaths() executor.Paths {
	return executor.Paths{
		Work:      MountWork,
		Role:      JobRole,
		Sidecar:   JobSidecar,
		ConfigDir: AgentConfig,
		History:   JobHistory,
	}
}

// Agent environment.
const (
	AgentHome   = JobHome
	AgentTmpdir = "/tmp"
)

// Per-job Secret items. The model credential is stored under its env name.
const (
	ItemManifest  = "manifest.json"
	ItemGitAccess = "token"      // pre only
	ItemResultKey = "result-key" // post only
	PreSecretDir  = "/var/run/ard/pre"
	PostSecretDir = "/var/run/ard/post"
	SecretMode    = 0o440
)

// ModelEnv lists the env names a model credential may have.
var ModelEnv = []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}

// Result route served by the post container.
const (
	ResultPort     = 8080
	ResultPortName = "ard-result"
	ResultPath     = "/v1/result"
	RootWork       = "work"
	RootOut        = "out"
	StdoutName     = "stdout"
	DoneName       = "done" // marker in /out, never packed
)

// Pre exit codes: 3 means a retry cannot help, 1 is transient. A short reason
// goes to /dev/termination-log.
const (
	ExitPermanent = 3
	ExitTransient = 1
)

// Labels.
const (
	labelPrefix    = "ard.albertcbrown/"
	LabelPartOf    = "app.kubernetes.io/part-of"
	LabelComponent = "app.kubernetes.io/component"
	LabelInstance  = "app.kubernetes.io/instance"
	LabelRepo      = labelPrefix + "repo"
	LabelNetwork   = labelPrefix + "network"
	LabelJob       = labelPrefix + "job"
	LabelIssue     = labelPrefix + "issue"
	LabelStage     = labelPrefix + "stage"
	LabelRole      = labelPrefix + "role"

	ComponentJob = "ard-job"
)

// Pod naming.
const (
	podPrefix = "ard-job-"
	// MaxJobID is the longest job id whose pod name fits 63 characters.
	MaxJobID = 63 - len(podPrefix)
)

// PodName is the pod and Secret name of a job. ULIDs are case-insensitive;
// names are lowercase.
func PodName(id string) string { return podPrefix + strings.ToLower(id) }

// RepoLabel is a label value for owner/name: 16 hex of the sha256 of the
// lowercased repo.
func RepoLabel(repo string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(repo)))
	return hex.EncodeToString(sum[:8])
}

// LabelValue returns s as a valid label value of at most 63 characters. A
// value that does not fit is cut and given a short hash of the whole.
func LabelValue(s string) string {
	clean := func(in string) string {
		b := []byte(in)
		for i, c := range b {
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			default:
				b[i] = '_'
			}
		}
		return strings.Trim(string(b), "-_.")
	}
	v := clean(s)
	if v == s && len(v) <= 63 {
		return v
	}
	sum := sha256.Sum256([]byte(s))
	h := hex.EncodeToString(sum[:4])
	if len(v) > 63-9 {
		v = v[:63-9]
	}
	v = strings.Trim(v, "-_.")
	if v == "" {
		return h
	}
	return v + "-" + h
}

// Package api is a minimal Kubernetes REST layer: only the JSON fields ard
// sets or reads, a Client interface, and an in-cluster implementation.
// It uses the standard library only (no client-go).
package api

import "time"

// ObjectMeta is the metadata common to every object.
type ObjectMeta struct {
	Name              string            `json:"name,omitempty"`
	Namespace         string            `json:"namespace,omitempty"`
	UID               string            `json:"uid,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	OwnerReferences   []OwnerReference  `json:"ownerReferences,omitempty"`
	CreationTimestamp time.Time         `json:"creationTimestamp,omitzero"`
}

// OwnerReference links an object to its owner.
type OwnerReference struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Name       string `json:"name,omitempty"`
	UID        string `json:"uid,omitempty"`
}

// Pod is a v1 Pod.
type Pod struct {
	Meta   ObjectMeta `json:"metadata"`
	Spec   PodSpec    `json:"spec"`
	Status PodStatus  `json:"status,omitzero"`
}

// PodSpec is the part of the pod spec ard sets.
type PodSpec struct {
	InitContainers                []Container         `json:"initContainers,omitempty"`
	Containers                    []Container         `json:"containers,omitempty"`
	Volumes                       []Volume            `json:"volumes,omitempty"`
	RestartPolicy                 string              `json:"restartPolicy,omitempty"`
	ActiveDeadlineSeconds         *int64              `json:"activeDeadlineSeconds,omitempty"`
	TerminationGracePeriodSeconds *int64              `json:"terminationGracePeriodSeconds,omitempty"`
	AutomountServiceAccountToken  *bool               `json:"automountServiceAccountToken,omitempty"`
	EnableServiceLinks            *bool               `json:"enableServiceLinks,omitempty"`
	ServiceAccountName            string              `json:"serviceAccountName,omitempty"`
	SecurityContext               *PodSecurityContext `json:"securityContext,omitempty"`
	ShareProcessNamespace         *bool               `json:"shareProcessNamespace,omitempty"`
}

// Container is a v1 Container.
type Container struct {
	Name                     string               `json:"name"`
	Image                    string               `json:"image,omitempty"`
	ImagePullPolicy          string               `json:"imagePullPolicy,omitempty"`
	TerminationMessagePolicy string               `json:"terminationMessagePolicy,omitempty"`
	Command                  []string             `json:"command,omitempty"`
	Args                     []string             `json:"args,omitempty"`
	Env                      []EnvVar             `json:"env,omitempty"`
	Ports                    []ContainerPort      `json:"ports,omitempty"`
	VolumeMounts             []VolumeMount        `json:"volumeMounts,omitempty"`
	Resources                ResourceRequirements `json:"resources,omitzero"`
	SecurityContext          *SecurityContext     `json:"securityContext,omitempty"`
	WorkingDir               string               `json:"workingDir,omitempty"`
	// RestartPolicy "Always" on an init container makes it a native sidecar.
	RestartPolicy *string `json:"restartPolicy,omitempty"`
}

// ContainerPort declares a container port.
type ContainerPort struct {
	Name          string `json:"name,omitempty"`
	ContainerPort int32  `json:"containerPort"`
	Protocol      string `json:"protocol,omitempty"`
}

// EnvVar is an environment variable, literal or from a Secret.
type EnvVar struct {
	Name      string        `json:"name"`
	Value     string        `json:"value,omitempty"`
	ValueFrom *EnvVarSource `json:"valueFrom,omitempty"`
}

// EnvVarSource is where an EnvVar value comes from.
type EnvVarSource struct {
	SecretKeyRef *SecretKeySelector `json:"secretKeyRef,omitempty"`
}

// SecretKeySelector names one key of a Secret.
type SecretKeySelector struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// VolumeMount mounts a volume into a container.
type VolumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	SubPath   string `json:"subPath,omitempty"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// Volume is a pod volume: an emptyDir or a Secret.
type Volume struct {
	Name     string                `json:"name"`
	EmptyDir *EmptyDirVolumeSource `json:"emptyDir,omitempty"`
	Secret   *SecretVolumeSource   `json:"secret,omitempty"`
}

// EmptyDirVolumeSource is an emptyDir volume.
type EmptyDirVolumeSource struct {
	SizeLimit string `json:"sizeLimit,omitempty"`
}

// SecretVolumeSource projects a Secret as files.
type SecretVolumeSource struct {
	SecretName  string      `json:"secretName"`
	Items       []KeyToPath `json:"items,omitempty"`
	DefaultMode *int32      `json:"defaultMode,omitempty"`
}

// KeyToPath maps a Secret key to a file path.
type KeyToPath struct {
	Key  string `json:"key"`
	Path string `json:"path"`
	Mode *int32 `json:"mode,omitempty"`
}

// SecurityContext is the container security context.
type SecurityContext struct {
	RunAsUser                *int64          `json:"runAsUser,omitempty"`
	RunAsGroup               *int64          `json:"runAsGroup,omitempty"`
	RunAsNonRoot             *bool           `json:"runAsNonRoot,omitempty"`
	AllowPrivilegeEscalation *bool           `json:"allowPrivilegeEscalation,omitempty"`
	ReadOnlyRootFilesystem   *bool           `json:"readOnlyRootFilesystem,omitempty"`
	Capabilities             *Capabilities   `json:"capabilities,omitempty"`
	SeccompProfile           *SeccompProfile `json:"seccompProfile,omitempty"`
}

// PodSecurityContext is the pod-level security context.
type PodSecurityContext struct {
	RunAsUser      *int64          `json:"runAsUser,omitempty"`
	RunAsGroup     *int64          `json:"runAsGroup,omitempty"`
	RunAsNonRoot   *bool           `json:"runAsNonRoot,omitempty"`
	FSGroup        *int64          `json:"fsGroup,omitempty"`
	SeccompProfile *SeccompProfile `json:"seccompProfile,omitempty"`
}

// Capabilities lists Linux capabilities to drop.
type Capabilities struct {
	Drop []string `json:"drop,omitempty"`
}

// SeccompProfile selects a seccomp profile type.
type SeccompProfile struct {
	Type string `json:"type"`
}

// ResourceRequirements are requests and limits by resource name.
type ResourceRequirements struct {
	Requests map[string]string `json:"requests,omitempty"`
	Limits   map[string]string `json:"limits,omitempty"`
}

// PodStatus is the observed pod state.
type PodStatus struct {
	Phase                 string            `json:"phase,omitempty"`
	Reason                string            `json:"reason,omitempty"`
	Message               string            `json:"message,omitempty"`
	PodIP                 string            `json:"podIP,omitempty"`
	InitContainerStatuses []ContainerStatus `json:"initContainerStatuses,omitempty"`
	ContainerStatuses     []ContainerStatus `json:"containerStatuses,omitempty"`
}

// ContainerStatus is one container's observed state.
type ContainerStatus struct {
	Name         string         `json:"name"`
	State        ContainerState `json:"state,omitzero"`
	RestartCount int32          `json:"restartCount,omitempty"`
}

// ContainerState is exactly one of Waiting, Running or Terminated.
type ContainerState struct {
	Waiting    *StateWaiting    `json:"waiting,omitempty"`
	Running    *StateRunning    `json:"running,omitempty"`
	Terminated *StateTerminated `json:"terminated,omitempty"`
}

// StateWaiting is a waiting container.
type StateWaiting struct {
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

// StateRunning is a running container.
type StateRunning struct {
	StartedAt time.Time `json:"startedAt,omitzero"`
}

// StateTerminated is a terminated container.
type StateTerminated struct {
	ExitCode   int32     `json:"exitCode"`
	Signal     int32     `json:"signal,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Message    string    `json:"message,omitempty"`
	StartedAt  time.Time `json:"startedAt,omitzero"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`
}

// Secret is a v1 Secret; Data is base64 on the wire, which encoding/json
// does for []byte.
type Secret struct {
	Meta ObjectMeta        `json:"metadata"`
	Type string            `json:"type,omitempty"`
	Data map[string][]byte `json:"data,omitempty"`
}

// ObjectReference identifies the object an Event is about.
type ObjectReference struct {
	Name string `json:"name,omitempty"`
	UID  string `json:"uid,omitempty"`
}

// Event is a v1 Event.
type Event struct {
	Meta           ObjectMeta      `json:"metadata"`
	InvolvedObject ObjectReference `json:"involvedObject,omitzero"`
	Reason         string          `json:"reason,omitempty"`
	Message        string          `json:"message,omitempty"`
	Type           string          `json:"type,omitempty"`
	LastTimestamp  time.Time       `json:"lastTimestamp,omitzero"`
}

// Status is the body of an API error response.
type Status struct {
	Code    int    `json:"code,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

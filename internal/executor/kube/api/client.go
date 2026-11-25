package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrNotFound is returned for a 404.
var ErrNotFound = errors.New("kube: not found")

// ErrConflict is returned for a 409.
var ErrConflict = errors.New("kube: conflict")

// StatusError is any other non-2xx API answer.
type StatusError struct {
	Code    int
	Reason  string
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("kube: status %d %s: %s", e.Code, e.Reason, e.Message)
}

// Client is the few Kubernetes endpoints ard uses. There is deliberately no
// pods/exec, no Secret get or list and no patch.
type Client interface {
	CreatePod(ctx context.Context, ns string, p *Pod) (*Pod, error)
	// GetPod returns ErrNotFound when the pod does not exist.
	GetPod(ctx context.Context, ns, name string) (*Pod, error)
	ListPods(ctx context.Context, ns, labelSelector string) ([]Pod, error)
	// DeletePod uses background propagation; a missing pod is not an error.
	DeletePod(ctx context.Context, ns, name string, graceSec int64) error
	// PodLogs follows the container log with timestamps. A zero sinceTime
	// starts at the beginning.
	PodLogs(ctx context.Context, ns, name, container string, sinceTime time.Time) (io.ReadCloser, error)
	// CreateSecret returns ErrConflict when the Secret exists.
	CreateSecret(ctx context.Context, ns string, s *Secret) error
	// DeleteSecret treats a missing Secret as success.
	DeleteSecret(ctx context.Context, ns, name string) error
	ListEvents(ctx context.Context, ns, fieldSelector string) ([]Event, error)
}

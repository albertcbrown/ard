// Package fake is an in-memory api.Client for tests.
package fake

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/executor/kube/api"
)

var _ api.Client = (*Fake)(nil)

// LogBase is the timestamp of the first scripted log line; line i is at
// LogBase + i seconds.
var LogBase = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Fake is a mutex-guarded in-memory Client.
type Fake struct {
	// Err maps a method name (e.g. "GetPod") to an error that method returns.
	// Set it before use; it is read under the lock.
	Err map[string]error
	// LogLimit, when > 0, ends each PodLogs stream after that many lines, so
	// a caller must reconnect with sinceTime.
	LogLimit int

	mu      sync.Mutex
	pods    map[string]*api.Pod // key ns/name
	secrets map[string]*api.Secret
	events  map[string][]api.Event // by ns
	logs    map[string][]string    // key ns/pod/container
	calls   []string
	nextUID int
}

// New returns an empty Fake.
func New() *Fake {
	return &Fake{
		Err:     map[string]error{},
		pods:    map[string]*api.Pod{},
		secrets: map[string]*api.Secret{},
		events:  map[string][]api.Event{},
		logs:    map[string][]string{},
	}
}

func key(ns, name string) string { return ns + "/" + name }

// enter records the call and returns any injected error. Caller holds mu.
func (f *Fake) enter(method string) error {
	f.calls = append(f.calls, method)
	return f.Err[method]
}

// Calls returns the method names called so far, in order.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *Fake) uid() string {
	f.nextUID++
	return fmt.Sprintf("uid-%d", f.nextUID)
}

func copyPod(p *api.Pod) *api.Pod {
	c := *p
	c.Status.ContainerStatuses = append([]api.ContainerStatus(nil), p.Status.ContainerStatuses...)
	c.Status.InitContainerStatuses = append([]api.ContainerStatus(nil), p.Status.InitContainerStatuses...)
	return &c
}

func (f *Fake) CreatePod(_ context.Context, ns string, p *api.Pod) (*api.Pod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("CreatePod"); err != nil {
		return nil, err
	}
	k := key(ns, p.Meta.Name)
	if _, ok := f.pods[k]; ok {
		return nil, api.ErrConflict
	}
	c := copyPod(p)
	c.Meta.Namespace = ns
	c.Meta.UID = f.uid()
	c.Status.Phase = "Pending"
	f.pods[k] = c
	return copyPod(c), nil
}

func (f *Fake) GetPod(_ context.Context, ns, name string) (*api.Pod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("GetPod"); err != nil {
		return nil, err
	}
	p, ok := f.pods[key(ns, name)]
	if !ok {
		return nil, api.ErrNotFound
	}
	return copyPod(p), nil
}

// parseSelector parses "k=v,k2=v2" (equality only; "==" accepted).
func parseSelector(sel string) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(sel) == "" {
		return out, nil
	}
	for _, part := range strings.Split(sel, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("fake: unsupported selector %q", part)
		}
		out[k] = strings.TrimPrefix(v, "=")
	}
	return out, nil
}

func (f *Fake) ListPods(_ context.Context, ns, labelSelector string) ([]api.Pod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("ListPods"); err != nil {
		return nil, err
	}
	want, err := parseSelector(labelSelector)
	if err != nil {
		return nil, err
	}
	var out []api.Pod
	for _, p := range f.pods {
		if p.Meta.Namespace != ns {
			continue
		}
		match := true
		for k, v := range want {
			if got, ok := p.Meta.Labels[k]; !ok || got != v {
				match = false
				break
			}
		}
		if match {
			out = append(out, *copyPod(p))
		}
	}
	return out, nil
}

func (f *Fake) DeletePod(_ context.Context, ns, name string, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("DeletePod"); err != nil {
		return err
	}
	delete(f.pods, key(ns, name))
	return nil
}

func (f *Fake) CreateSecret(_ context.Context, ns string, s *api.Secret) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("CreateSecret"); err != nil {
		return err
	}
	k := key(ns, s.Meta.Name)
	if _, ok := f.secrets[k]; ok {
		return api.ErrConflict
	}
	c := *s
	c.Data = cloneData(s.Data)
	c.Meta.Namespace = ns
	c.Meta.UID = f.uid()
	f.secrets[k] = &c
	return nil
}

func (f *Fake) DeleteSecret(_ context.Context, ns, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("DeleteSecret"); err != nil {
		return err
	}
	delete(f.secrets, key(ns, name))
	return nil
}

// Secret returns a stored Secret for assertions (the Client has no Get).
func (f *Fake) Secret(ns, name string) (*api.Secret, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.secrets[key(ns, name)]
	if !ok {
		return nil, false
	}
	c := *s
	c.Data = cloneData(s.Data)
	return &c, true
}

func (f *Fake) ListEvents(_ context.Context, ns, fieldSelector string) ([]api.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("ListEvents"); err != nil {
		return nil, err
	}
	want, err := parseSelector(fieldSelector)
	if err != nil {
		return nil, err
	}
	var out []api.Event
	for _, e := range f.events[ns] {
		if v, ok := want["involvedObject.name"]; ok && e.InvolvedObject.Name != v {
			continue
		}
		if v, ok := want["involvedObject.uid"]; ok && e.InvolvedObject.UID != v {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// AddEvent stores an event in ns.
func (f *Fake) AddEvent(ns string, e api.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e.Meta.Namespace = ns
	f.events[ns] = append(f.events[ns], e)
}

func (f *Fake) mutate(ns, name string, fn func(*api.Pod)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.pods[key(ns, name)]
	if !ok {
		return api.ErrNotFound
	}
	fn(p)
	return nil
}

// SetPhase sets the pod phase.
func (f *Fake) SetPhase(ns, name, phase string) error {
	return f.mutate(ns, name, func(p *api.Pod) { p.Status.Phase = phase })
}

// SetPodIP sets the pod IP.
func (f *Fake) SetPodIP(ns, name, ip string) error {
	return f.mutate(ns, name, func(p *api.Pod) { p.Status.PodIP = ip })
}

// SetContainer sets the state of the named container status, replacing an
// existing entry or appending one.
func (f *Fake) SetContainer(ns, pod, name string, st api.ContainerState) error {
	return f.mutate(ns, pod, func(p *api.Pod) {
		for i := range p.Status.ContainerStatuses {
			if p.Status.ContainerStatuses[i].Name == name {
				p.Status.ContainerStatuses[i].State = st
				return
			}
		}
		p.Status.ContainerStatuses = append(p.Status.ContainerStatuses, api.ContainerStatus{Name: name, State: st})
	})
}

// Evict marks the pod Failed with reason Evicted.
func (f *Fake) Evict(ns, name string) error {
	return f.mutate(ns, name, func(p *api.Pod) {
		p.Status.Phase = "Failed"
		p.Status.Reason = "Evicted"
	})
}

// SetLogs scripts a container's log. Line i is served as
// "<LogBase+i seconds, RFC3339Nano> <line>".
func (f *Fake) SetLogs(ns, pod, container string, lines []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs[ns+"/"+pod+"/"+container] = append([]string(nil), lines...)
}

// PodLogs serves the scripted lines strictly after sinceTime, at most
// LogLimit of them when set.
func (f *Fake) PodLogs(_ context.Context, ns, name, container string, sinceTime time.Time) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("PodLogs"); err != nil {
		return nil, err
	}
	if _, ok := f.pods[key(ns, name)]; !ok {
		return nil, api.ErrNotFound
	}
	var b strings.Builder
	n := 0
	for i, l := range f.logs[ns+"/"+name+"/"+container] {
		ts := LogBase.Add(time.Duration(i) * time.Second)
		if !sinceTime.IsZero() && !ts.After(sinceTime) {
			continue
		}
		if f.LogLimit > 0 && n >= f.LogLimit {
			break
		}
		fmt.Fprintf(&b, "%s %s\n", ts.Format(time.RFC3339Nano), l)
		n++
	}
	return io.NopCloser(strings.NewReader(b.String())), nil
}

func cloneData(m map[string][]byte) map[string][]byte {
	if m == nil {
		return nil
	}
	c := make(map[string][]byte, len(m))
	for k, v := range m {
		c[k] = append([]byte(nil), v...)
	}
	return c
}

package fake

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor/kube/api"
)

var ctx = context.Background()

func pod(name string, labels map[string]string) *api.Pod {
	return &api.Pod{Meta: api.ObjectMeta{Name: name, Labels: labels}}
}

func TestRoundTrip(t *testing.T) {
	f := New()
	p, err := f.CreatePod(ctx, "ns", pod("a", nil))
	if err != nil || p.Meta.UID == "" || p.Status.Phase != "Pending" {
		t.Fatalf("create: %v %+v", err, p)
	}
	if _, err := f.CreatePod(ctx, "ns", pod("a", nil)); !errors.Is(err, api.ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := f.GetPod(ctx, "ns", "a"); err != nil {
		t.Fatal(err)
	}
	if err := f.DeletePod(ctx, "ns", "a", 0); err != nil {
		t.Fatal(err)
	}
	if err := f.DeletePod(ctx, "ns", "a", 0); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := f.GetPod(ctx, "ns", "a"); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
}

func TestSecrets(t *testing.T) {
	f := New()
	s := &api.Secret{Meta: api.ObjectMeta{Name: "s"}, Data: map[string][]byte{"k": []byte("v")}}
	if err := f.CreateSecret(ctx, "ns", s); err != nil {
		t.Fatal(err)
	}
	if err := f.CreateSecret(ctx, "ns", s); !errors.Is(err, api.ErrConflict) {
		t.Fatalf("dup: %v", err)
	}
	if _, ok := f.Secret("ns", "s"); !ok {
		t.Fatal("missing secret")
	}
	if err := f.DeleteSecret(ctx, "ns", "s"); err != nil {
		t.Fatal(err)
	}
	if err := f.DeleteSecret(ctx, "ns", "s"); err != nil {
		t.Fatal(err)
	}
}

func TestEvict(t *testing.T) {
	f := New()
	f.CreatePod(ctx, "ns", pod("a", nil))
	if err := f.Evict("ns", "a"); err != nil {
		t.Fatal(err)
	}
	p, _ := f.GetPod(ctx, "ns", "a")
	if p.Status.Phase != "Failed" || p.Status.Reason != "Evicted" {
		t.Fatalf("status %+v", p.Status)
	}
}

func TestSetContainer(t *testing.T) {
	f := New()
	f.CreatePod(ctx, "ns", pod("a", nil))
	f.SetContainer("ns", "a", "agent", api.ContainerState{Running: &api.StateRunning{}})
	f.SetContainer("ns", "a", "agent", api.ContainerState{Terminated: &api.StateTerminated{ExitCode: 3}})
	p, _ := f.GetPod(ctx, "ns", "a")
	if len(p.Status.ContainerStatuses) != 1 || p.Status.ContainerStatuses[0].State.Terminated.ExitCode != 3 {
		t.Fatalf("status %+v", p.Status)
	}
}

func readAll(t *testing.T, rc io.ReadCloser) string {
	t.Helper()
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return string(b)
}

func TestLogsResume(t *testing.T) {
	f := New()
	f.LogLimit = 2
	f.CreatePod(ctx, "ns", pod("a", nil))
	f.SetLogs("ns", "a", "agent", []string{"one", "two", "three"})
	rc, err := f.PodLogs(ctx, "ns", "a", "agent", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	first := strings.Split(strings.TrimSpace(readAll(t, rc)), "\n")
	if len(first) != 2 || !strings.HasSuffix(first[1], " two") {
		t.Fatalf("first %q", first)
	}
	ts, _, _ := strings.Cut(first[1], " ")
	since, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t.Fatal(err)
	}
	rc, _ = f.PodLogs(ctx, "ns", "a", "agent", since)
	if got := strings.TrimSpace(readAll(t, rc)); !strings.HasSuffix(got, " three") || strings.Contains(got, "two") {
		t.Fatalf("resume %q", got)
	}
}

func TestInjectedErrAndCalls(t *testing.T) {
	f := New()
	boom := errors.New("boom")
	f.Err["GetPod"] = boom
	f.CreatePod(ctx, "ns", pod("a", nil))
	if _, err := f.GetPod(ctx, "ns", "a"); err != boom {
		t.Fatalf("err %v", err)
	}
	f.ListPods(ctx, "ns", "")
	got := strings.Join(f.Calls(), ",")
	if got != "CreatePod,GetPod,ListPods" {
		t.Fatalf("calls %s", got)
	}
}

func TestListSelector(t *testing.T) {
	f := New()
	f.CreatePod(ctx, "ns", pod("a", map[string]string{"app": "ard", "job": "1"}))
	f.CreatePod(ctx, "ns", pod("b", map[string]string{"app": "ard", "job": "2"}))
	f.CreatePod(ctx, "other", pod("c", map[string]string{"app": "ard"}))
	for sel, want := range map[string]int{"": 2, "app=ard": 2, "app=ard,job=2": 1, "job=3": 0} {
		got, err := f.ListPods(ctx, "ns", sel)
		if err != nil || len(got) != want {
			t.Errorf("%q: %d %v, want %d", sel, len(got), err, want)
		}
	}
	if _, err := f.ListPods(ctx, "ns", "app in (x)"); err == nil {
		t.Error("set selector accepted")
	}
}

func TestEvents(t *testing.T) {
	f := New()
	f.AddEvent("ns", api.Event{Reason: "x", InvolvedObject: api.ObjectReference{Name: "a"}})
	f.AddEvent("ns", api.Event{Reason: "y", InvolvedObject: api.ObjectReference{Name: "b"}})
	got, _ := f.ListEvents(ctx, "ns", "involvedObject.name=a")
	if len(got) != 1 || got[0].Reason != "x" {
		t.Fatalf("%+v", got)
	}
}

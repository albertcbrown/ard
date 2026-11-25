package api

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var ctx = context.Background()

type env struct {
	srv   *httptest.Server
	token string
	c     Client
	hits  atomic.Int32
}

func setup(t *testing.T, h http.HandlerFunc) *env {
	t.Helper()
	e := &env{}
	e.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.hits.Add(1)
		h(w, r)
	}))
	t.Cleanup(e.srv.Close)
	dir := t.TempDir()
	e.token = filepath.Join(dir, "token")
	if err := os.WriteFile(e.token, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(dir, "ca.crt")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: e.srv.Certificate().Raw})
	if err := os.WriteFile(ca, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := InCluster(RESTOptions{Host: strings.TrimPrefix(e.srv.URL, "https://"), TokenFile: e.token, CAFile: ca})
	if err != nil {
		t.Fatal(err)
	}
	e.c = c
	return e
}

func TestTokenReread(t *testing.T) {
	var seen []string
	e := setup(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.Write([]byte(`{}`))
	})
	e.c.GetPod(ctx, "ns", "p")
	os.WriteFile(e.token, []byte("b"), 0o600)
	e.c.GetPod(ctx, "ns", "p")
	if strings.Join(seen, "|") != "Bearer a|Bearer b" {
		t.Fatalf("seen %v", seen)
	}
}

func TestNoRedirect(t *testing.T) {
	var target atomic.Int32
	e := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			target.Add(1)
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	})
	if _, err := e.c.GetPod(ctx, "ns", "p"); err == nil {
		t.Fatal("expected error")
	}
	if target.Load() != 0 {
		t.Fatal("redirect followed")
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name   string
		code   int
		body   string
		check  func(error) bool
		maxMsg int
	}{
		{"404", 404, `{}`, func(e error) bool { return errors.Is(e, ErrNotFound) }, 0},
		{"409", 409, `{}`, func(e error) bool { return errors.Is(e, ErrConflict) }, 0},
		{"403", 403, `{"code":403,"reason":"Forbidden","message":"no"}`, func(e error) bool {
			var se *StatusError
			return errors.As(e, &se) && se.Code == 403 && se.Reason == "Forbidden"
		}, 0},
		{"big", 500, `{"message":"` + strings.Repeat("x", 10240) + `"}`, func(e error) bool {
			var se *StatusError
			return errors.As(e, &se) && len(se.Message) <= 512
		}, 0},
		{"nonjson", 502, "bad gateway", func(e error) bool {
			var se *StatusError
			return errors.As(e, &se) && se.Code == 502 && se.Message == "bad gateway"
		}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				io.WriteString(w, tc.body)
			})
			err := e.c.CreateSecret(ctx, "ns", &Secret{Meta: ObjectMeta{Name: "s"}})
			if err == nil || !tc.check(err) {
				t.Fatalf("err %v", err)
			}
		})
	}
}

func TestCreatePodBody(t *testing.T) {
	var got map[string]any
	e := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/namespaces/ns/pods" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		var p Pod
		json.Unmarshal(b, &p)
		p.Status = PodStatus{Phase: "Pending", ContainerStatuses: []ContainerStatus{{Name: "agent",
			State: ContainerState{Terminated: &StateTerminated{ExitCode: 7}}}}}
		json.NewEncoder(w).Encode(p)
	})
	dl := int64(60)
	mode := int32(0o400)
	in := &Pod{
		Meta: ObjectMeta{Name: "p"},
		Spec: PodSpec{
			ActiveDeadlineSeconds: &dl,
			InitContainers:        []Container{{Name: "pre", Image: "i"}},
			Containers: []Container{{Name: "agent", Env: []EnvVar{{Name: "K",
				ValueFrom: &EnvVarSource{SecretKeyRef: &SecretKeySelector{Name: "s", Key: "k"}}}}}},
			Volumes: []Volume{{Name: "v", Secret: &SecretVolumeSource{SecretName: "s",
				Items: []KeyToPath{{Key: "k", Path: "k", Mode: &mode}}}}},
		},
	}
	out, err := e.c.CreatePod(ctx, "ns", in)
	if err != nil {
		t.Fatal(err)
	}
	spec := got["spec"].(map[string]any)
	for _, k := range []string{"initContainers", "activeDeadlineSeconds", "containers", "volumes"} {
		if _, ok := spec[k]; !ok {
			t.Errorf("missing spec.%s", k)
		}
	}
	if _, ok := got["metadata"]; !ok {
		t.Error("missing metadata")
	}
	raw, _ := json.Marshal(got)
	for _, k := range []string{"secretKeyRef", `"items"`} {
		if !strings.Contains(string(raw), k) {
			t.Errorf("missing %s", k)
		}
	}
	if out.Status.ContainerStatuses[0].State.Terminated.ExitCode != 7 || *out.Spec.ActiveDeadlineSeconds != 60 {
		t.Fatalf("round trip %+v", out)
	}
}

func TestSecretDataBase64(t *testing.T) {
	var body map[string]any
	e := setup(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
	})
	e.c.CreateSecret(ctx, "ns", &Secret{Meta: ObjectMeta{Name: "s"}, Data: map[string][]byte{"k": []byte("hi")}})
	if body["data"].(map[string]any)["k"] != "aGk=" {
		t.Fatalf("%v", body)
	}
}

func TestPodLogsQuery(t *testing.T) {
	var q url.Values
	e := setup(t, func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		if !strings.HasSuffix(r.URL.Path, "/pods/p/log") {
			t.Errorf("path %s", r.URL.Path)
		}
		io.WriteString(w, "line\n")
	})
	since := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	rc, err := e.c.PodLogs(ctx, "ns", "p", "agent", since)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "line\n" || q.Get("follow") != "true" || q.Get("timestamps") != "true" ||
		q.Get("container") != "agent" || q.Get("sinceTime") != "2026-01-02T03:04:05.000000006Z" {
		t.Fatalf("body %q query %v", b, q)
	}
	rc, _ = e.c.PodLogs(ctx, "ns", "p", "agent", time.Time{})
	rc.Close()
	if q.Has("sinceTime") {
		t.Fatal("sinceTime sent for zero time")
	}
}

func TestDeletePropagation(t *testing.T) {
	var body map[string]any
	var code = 200
	e := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method %s", r.Method)
		}
		body = nil
		json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(code)
		io.WriteString(w, `{}`)
	})
	if err := e.c.DeletePod(ctx, "ns", "p", 5); err != nil {
		t.Fatal(err)
	}
	if body["propagationPolicy"] != "Background" || body["gracePeriodSeconds"] != float64(5) {
		t.Fatalf("body %v", body)
	}
	code = 404
	if err := e.c.DeletePod(ctx, "ns", "p", 5); err != nil {
		t.Fatalf("pod 404: %v", err)
	}
	if err := e.c.DeleteSecret(ctx, "ns", "s"); err != nil {
		t.Fatalf("secret 404: %v", err)
	}
}

func TestBadName(t *testing.T) {
	e := setup(t, func(w http.ResponseWriter, r *http.Request) {})
	bad := []string{"../x", "", "Upper", strings.Repeat("a", 64), "a/b", "-a"}
	for _, n := range bad {
		if _, err := e.c.GetPod(ctx, "ns", n); err == nil {
			t.Errorf("name %q accepted", n)
		}
		if _, err := e.c.GetPod(ctx, n, "p"); err == nil {
			t.Errorf("namespace %q accepted", n)
		}
	}
	if e.hits.Load() != 0 {
		t.Fatalf("%d requests made", e.hits.Load())
	}
}

func TestListSelectors(t *testing.T) {
	var raw string
	e := setup(t, func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		io.WriteString(w, `{"items":[{"metadata":{"name":"x"}}]}`)
	})
	pods, err := e.c.ListPods(ctx, "ns", "a=b,c=d")
	if err != nil || len(pods) != 1 || raw != "labelSelector=a%3Db%2Cc%3Dd" {
		t.Fatalf("%v %v %q", pods, err, raw)
	}
	if _, err := e.c.ListEvents(ctx, "ns", "involvedObject.name=p"); err != nil || raw != "fieldSelector=involvedObject.name%3Dp" {
		t.Fatalf("%v %q", err, raw)
	}
}

func TestInClusterErrors(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	if _, err := InCluster(RESTOptions{}); err == nil {
		t.Error("no host accepted")
	}
	if _, err := InCluster(RESTOptions{Host: "h:1", CAFile: filepath.Join(t.TempDir(), "none")}); err == nil {
		t.Error("missing CA accepted")
	}
	bad := filepath.Join(t.TempDir(), "ca")
	os.WriteFile(bad, []byte("junk"), 0o600)
	if _, err := InCluster(RESTOptions{Host: "h:1", CAFile: bad}); err == nil {
		t.Error("junk CA accepted")
	}
}

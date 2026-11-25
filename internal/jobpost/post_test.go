package jobpost

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/jobpod"
)

const testKey = "k3y-value-for-tests"

type rig struct {
	url       string
	work, out string
	cancel    context.CancelFunc
	errc      chan error
}

func start(t *testing.T) *rig {
	t.Helper()
	root := t.TempDir()
	r := &rig{work: filepath.Join(root, "work"), out: filepath.Join(root, "out"), errc: make(chan error, 1)}
	os.MkdirAll(r.work, 0o755)
	os.MkdirAll(r.out, 0o755)
	workDir, outDir = r.work, r.out
	t.Cleanup(func() { workDir, outDir = jobpod.MountWork, jobpod.MountOut })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r.url = "http://" + ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	t.Cleanup(cancel)
	go func() { r.errc <- serve(ctx, ln, testKey) }()
	return r
}

func (r *rig) get(t *testing.T, path, auth string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", r.url+path, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func (r *rig) finish(t *testing.T) {
	t.Helper()
	os.WriteFile(filepath.Join(r.work, "a.txt"), []byte("A"), 0o644)
	os.WriteFile(filepath.Join(r.out, jobpod.StdoutName), []byte("log"), 0o644)
	os.WriteFile(filepath.Join(r.out, "other"), []byte("no"), 0o644)
	os.WriteFile(filepath.Join(r.out, jobpod.DoneName), []byte("0\n"), 0o644)
}

func names(t *testing.T, b []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var out []string
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		if h.Typeflag == tar.TypeReg {
			out = append(out, h.Name)
		}
	}
	sort.Strings(out)
	return out
}

func TestPost(t *testing.T) {
	auth := "Bearer " + testKey
	t.Run("auth", func(t *testing.T) {
		r := start(t)
		r.finish(t)
		os.Chmod(r.work, 0o000) // a read before auth would fail loudly
		defer os.Chmod(r.work, 0o755)
		for _, a := range []string{"", "Bearer wrong", testKey, "Bearer "} {
			if resp, body := r.get(t, jobpod.ResultPath, a); resp.StatusCode != 401 || strings.Contains(string(body), testKey) {
				t.Errorf("auth %q: %d %q", a, resp.StatusCode, body)
			}
		}
	})
	t.Run("not ready then ready", func(t *testing.T) {
		r := start(t)
		os.Chmod(r.work, 0o000)
		if resp, _ := r.get(t, jobpod.ResultPath, auth); resp.StatusCode != 409 {
			t.Errorf("before marker: %d", resp.StatusCode)
		}
		os.Chmod(r.work, 0o755)
		r.finish(t)
		resp, b1 := r.get(t, jobpod.ResultPath, auth)
		if resp.StatusCode != 200 {
			t.Fatalf("after marker: %d", resp.StatusCode)
		}
		got := names(t, b1)
		if strings.Join(got, ",") != "out/stdout,work/a.txt" {
			t.Errorf("entries = %v", got)
		}
		if _, b2 := r.get(t, jobpod.ResultPath, auth); !bytes.Equal(b1, b2) {
			t.Error("repeat GET differs")
		}
	})
	t.Run("routes", func(t *testing.T) {
		r := start(t)
		for _, p := range []string{"/debug/pprof/", "/", "/v1/other"} {
			if resp, _ := r.get(t, p, auth); resp.StatusCode != 404 {
				t.Errorf("%s: %d", p, resp.StatusCode)
			}
		}
		req, _ := http.NewRequest("POST", r.url+jobpod.ResultPath, nil)
		req.Header.Set("Authorization", auth)
		if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 405 {
			t.Errorf("POST: %v %v", resp, err)
		}
	})
	t.Run("concurrent request gets 429", func(t *testing.T) {
		r := start(t)
		r.finish(t)
		// A big file and a client that does not read keep the first request busy.
		os.WriteFile(filepath.Join(r.work, "big"), bytes.Repeat([]byte{1}, 64<<20), 0o644)
		req, _ := http.NewRequest("GET", r.url+jobpod.ResultPath, nil)
		req.Header.Set("Authorization", auth)
		first, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer first.Body.Close()
		resp, _ := r.get(t, jobpod.ResultPath, auth)
		if resp.StatusCode != 429 {
			t.Errorf("second request: %d", resp.StatusCode)
		}
	})
	t.Run("shutdown on cancel", func(t *testing.T) {
		r := start(t)
		r.cancel()
		select {
		case err := <-r.errc:
			if err != nil {
				t.Errorf("serve = %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("no shutdown")
		}
	})
}

func TestRunKeyAndPort(t *testing.T) {
	dir := t.TempDir()
	keyDir = dir
	t.Cleanup(func() { keyDir = jobpod.PostSecretDir })
	ctx := context.Background()
	// Missing and empty key.
	if err := Run(ctx, &cli.Env{}, cli.JobPostOpts{}); err == nil {
		t.Error("missing key accepted")
	}
	os.WriteFile(filepath.Join(dir, jobpod.ItemResultKey), []byte("\n"), 0o444)
	if err := Run(ctx, &cli.Env{}, cli.JobPostOpts{}); err == nil {
		t.Error("empty key accepted")
	}
	os.Remove(filepath.Join(dir, jobpod.ItemResultKey))
	os.WriteFile(filepath.Join(dir, jobpod.ItemResultKey), []byte(testKey), 0o444)
	// Port already bound.
	busy, _ := net.Listen("tcp", "127.0.0.1:0")
	defer busy.Close()
	listen = func() (net.Listener, error) { return net.Listen("tcp", busy.Addr().String()) }
	defer func() {
		listen = func() (net.Listener, error) { return net.Listen("tcp", ":8080") }
	}()
	if err := Run(ctx, &cli.Env{}, cli.JobPostOpts{}); err == nil {
		t.Error("bound port accepted")
	}
	// Deadline ends the server with exit 0.
	listen = func() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
	if err := Run(ctx, &cli.Env{}, cli.JobPostOpts{Deadline: time.Now().Add(200 * time.Millisecond)}); err != nil {
		t.Errorf("deadline: %v", err)
	}
}

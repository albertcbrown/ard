// Package jobpost is `ard job-post`, the result server of a Kubernetes job
// pod. It serves one route, GET /v1/result, guarded by the per-job key, and
// streams the packed work tree and agent stdout once the agent step has
// written its done marker. It serves plain HTTP; the controller reaches it
// through the pod network.
package jobpost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/jobpod"
	"github.com/albertcbrown/ard/internal/tarball"
)

// Redirectable for tests.
var (
	keyDir  = jobpod.PostSecretDir
	workDir = jobpod.MountWork
	outDir  = jobpod.MountOut
	listen  = func() (net.Listener, error) { return net.Listen("tcp", fmt.Sprintf(":%d", jobpod.ResultPort)) }
)

const maxKeyBytes = 1024

// Run serves until SIGTERM, ctx cancellation or the deadline, then exits 0.
func Run(ctx context.Context, e *cli.Env, o cli.JobPostOpts) error {
	key, err := readKey()
	if err != nil {
		return err
	}
	ln, err := listen()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, os.Interrupt)
	defer stop()
	if !o.Deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, o.Deadline)
		defer cancel()
	}
	return serve(ctx, ln, key)
}

func readKey() (string, error) {
	f, err := os.Open(filepath.Join(keyDir, jobpod.ItemResultKey))
	if err != nil {
		return "", fmt.Errorf("result key: %w", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxKeyBytes+1))
	if err != nil {
		return "", fmt.Errorf("result key: %w", err)
	}
	k := strings.TrimSpace(string(b))
	if k == "" || len(b) > maxKeyBytes {
		return "", errors.New("result key: empty or oversize")
	}
	return k, nil
}

func serve(ctx context.Context, ln net.Listener, key string) error {
	h := &handler{key: key, slot: make(chan struct{}, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+jobpod.ResultPath, h.result)
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
		ErrorLog:          slog.NewLogLogger(slog.DiscardHandler, slog.LevelError),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		srv.Close()
	}
	return nil
}

type handler struct {
	key  string
	slot chan struct{}
}

func (h *handler) result(w http.ResponseWriter, r *http.Request) {
	const prefix = "Bearer "
	got := r.Header.Get("Authorization")
	if !strings.HasPrefix(got, prefix) || !jobpod.KeyMatches(got[len(prefix):], h.key) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	select {
	case h.slot <- struct{}{}:
		defer func() { <-h.slot }()
	default:
		http.Error(w, "busy", http.StatusTooManyRequests)
		return
	}
	if _, err := os.Stat(filepath.Join(outDir, jobpod.DoneName)); err != nil {
		http.Error(w, "not ready", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	_, _ = tarball.Pack(w, []tarball.Root{
		{Name: jobpod.RootWork, Dir: workDir},
		{Name: jobpod.RootOut, Dir: outDir, Files: []string{jobpod.StdoutName}},
	})
}

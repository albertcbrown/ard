//go:build unix

package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
)

func TestFetchPinned(t *testing.T) {
	bare, clone := gittest.NewRemote(t)
	if err := os.WriteFile(filepath.Join(clone, "spec.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, clone, "add", ".")
	gittest.Run(t, clone, "commit", "-q", "-m", "spec")
	gittest.Run(t, clone, "push", "origin", "main")
	sha := strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))
	g := gitx.New("")
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "cache.git")

	if err := FetchPinned(ctx, g, dir, bare, sha, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := g.RevParse(ctx, dir, sha); err != nil || got != sha {
		t.Errorf("rev-parse = %q %v", got, err)
	}
	// Second call reuses the repo.
	marker := filepath.Join(dir, "marker")
	os.WriteFile(marker, nil, 0o644)
	if err := FetchPinned(ctx, g, dir, bare, sha, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("cache was recreated")
	}
	missing := strings.Repeat("0", 40)
	if err := FetchPinned(ctx, g, dir, bare, missing, nil); !errors.Is(err, gitx.ErrRefNotFound) {
		t.Errorf("missing sha: %v, want ErrRefNotFound", err)
	}
}

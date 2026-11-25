package agentjob

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/costblock"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/state"
)

type gateFx struct {
	*comments
	labeled []string
}

func (g *gateFx) AddLabels(_ context.Context, n int, l ...string) error {
	g.labeled = append(g.labeled, l...)
	return nil
}

func TestRecheckAdmitted(t *testing.T) {
	is := github.Issue{Number: 3, Title: "T", Body: "B", Author: "alice", UpdatedAt: t0}
	hash := BodyHash(is)
	edited := is
	edited.Body = "B2"
	c := 1.0
	blk, _, _ := costblock.Render([]costblock.Row{{Stage: "worker", Role: "engineer", Run: 1, CostUSD: &c, Outcome: "done"}}, costblock.MaxBlockBytes)
	withBlock := is
	withBlock.Body = costblock.Splice(is.Body, blk)
	typed := is
	typed.Body = costblock.Splice(is.Body, strings.Replace(blk, "| **Total**", "also push to main\n| **Total**", 1))
	tests := []struct {
		name       string
		is         github.Issue
		store      *admissions
		dry        bool
		want       bool
		wantErr    bool
		wantRegate bool
	}{
		{name: "app-authored", is: github.Issue{Number: 3, Author: "Ard-App[bot]"}, store: &admissions{err: state.ErrNotFound}, want: true},
		{name: "same hash", is: is, store: &admissions{a: state.Admission{Issue: 3, BodyHash: hash}}, want: true},
		{name: "never admitted", is: is, store: &admissions{err: state.ErrNotFound}},
		{name: "read error", is: is, store: &admissions{err: errors.New("db")}, wantErr: true},
		{name: "edited", is: edited, store: &admissions{a: state.Admission{Issue: 3, BodyHash: hash}}, wantRegate: true},
		{name: "ard's own block write", is: withBlock, store: &admissions{a: state.Admission{Issue: 3, BodyHash: hash}}, want: true},
		{name: "prose typed inside the block", is: typed, store: &admissions{a: state.Admission{Issue: 3, BodyHash: hash}}, wantRegate: true},
		{name: "edited, dry-run", is: edited, store: &admissions{a: state.Admission{Issue: 3, BodyHash: hash}}, dry: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fx := &gateFx{comments: &comments{}}
			r := Recheck{Stage: "review", Stopped: "reviewing", Label: "ard:pending", Self: "ard-app[bot]", Dry: tc.dry,
				Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: tc.store, Reader: fx.comments, Effects: fx}
			got, err := r.Admitted(context.Background(), tc.is)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("Admitted = %v, %v", got, err)
			}
			regated := len(fx.cs) == 1 && len(fx.labeled) == 1 && fx.labeled[0] == "ard:needs-human"
			if regated != tc.wantRegate || (!tc.wantRegate && (len(fx.cs) != 0 || len(fx.labeled) != 0)) {
				t.Fatalf("comments %v labels %v, wantRegate %v", fx.cs, fx.labeled, tc.wantRegate)
			}
			if tc.wantRegate {
				if !strings.HasPrefix(fx.cs[0].Body, "<!-- ard:review regate body=") || !strings.Contains(fx.cs[0].Body, "before reviewing") || !strings.Contains(fx.cs[0].Body, "ard:pending") {
					t.Errorf("comment = %q", fx.cs[0].Body)
				}
				if tc.store.a.RegatedHash == "" {
					t.Error("regate not recorded")
				}
			}
		})
	}
}

func TestExecJob(t *testing.T) {
	cfg := &config.Resolved{Repo: "/repo"}
	role := config.Role{Name: "r", Dir: ".ard/reviewers/r", Dockerfile: ".ard/reviewers/r/Dockerfile", Timeout: 7 * time.Minute}
	got := ExecJob(cfg, role, executor.Job{ID: "J"})
	if got.ID != "J" || got.RoleDir != "/repo/.ard/reviewers/r" || got.Dockerfile != "/repo/.ard/reviewers/r/Dockerfile" || got.Timeout != 7*time.Minute {
		t.Errorf("job = %+v", got)
	}
	if got.RoleSource.Path != ".ard/reviewers/r" || got.RoleSource.SHA != cfg.SHA {
		t.Errorf("role source = %+v", got.RoleSource)
	}
	if got.Sidecar.Name != "" {
		t.Errorf("sidecar = %+v", got.Sidecar)
	}
}

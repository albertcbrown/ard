package setupcmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/labels/labelsync"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

var want = []labels.Label{
	{Name: "ard:hold", Color: "f9a03f", Description: "d"},
	{Name: "ard:keep", Color: "cfd3d7", Description: "k"},
}

func okPerm(context.Context) Row { return Row{Status: StatusOK, Detail: "repository reachable"} }

func deps(f *fake.Fake) Deps {
	return Deps{Reader: f, Writer: effects.NewLive(f, nil, quiet), Desired: want, Permissions: okPerm, Log: quiet}
}

func labelRow(t *testing.T, r Report) Row {
	t.Helper()
	for _, x := range r.Rows {
		if x.Name == labelName {
			return x
		}
	}
	t.Fatalf("no labels row in %+v", r.Rows)
	return Row{}
}

func TestRunAppliesWhenGreen(t *testing.T) {
	f := fake.New()
	rep, err := Run(context.Background(), deps(f), Opts{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if r := labelRow(t, rep); r.Status != StatusChanged || r.Detail != "created 2, updated 0" {
		t.Errorf("row = %+v", r)
	}
	if got, _ := f.ListLabels(context.Background()); len(got) != 2 {
		t.Errorf("labels = %v", got)
	}
}

func TestRunIdempotent(t *testing.T) {
	f := fake.New()
	d := deps(f)
	if _, err := Run(context.Background(), d, Opts{}, io.Discard); err != nil {
		t.Fatal(err)
	}
	n := len(f.Writes())
	rep, err := Run(context.Background(), d, Opts{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if r := labelRow(t, rep); r.Status != StatusOK || len(f.Writes()) != n {
		t.Errorf("row = %+v, writes %d -> %d", r, n, len(f.Writes()))
	}
}

func TestRunCheckReportsDriftNoWrites(t *testing.T) {
	f := fake.New()
	rep, err := Run(context.Background(), deps(f), Opts{Check: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.HasDrift() || rep.Green() || len(f.Writes()) != 0 {
		t.Errorf("rep = %+v writes = %d", rep, len(f.Writes()))
	}
	if r := labelRow(t, rep); r.Detail != "2 to create, 0 to update" {
		t.Errorf("detail = %q", r.Detail)
	}
}

func TestRunPermissionFailureSkipsApply(t *testing.T) {
	f := fake.New()
	d := deps(f)
	d.Permissions = func(context.Context) Row { return Row{Status: StatusManual, Detail: "install the App"} }
	var out bytes.Buffer
	rep, err := Run(context.Background(), d, Opts{}, &out)
	if err != nil {
		t.Fatal(err)
	}
	Print(&out, rep, false)
	if len(f.Writes()) != 0 || !strings.Contains(out.String(), "manual: permissions: install the App") {
		t.Errorf("writes=%d out=%q", len(f.Writes()), out.String())
	}
}

func TestRunDryRun(t *testing.T) {
	f := fake.New()
	d := deps(f)
	dr, err := effects.NewDryRun(effects.DryRunOptions{Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	d.Writer, d.DryRun = dr, true
	rep, err := Run(context.Background(), d, Opts{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if r := labelRow(t, rep); r.Status != StatusChanged || !strings.Contains(r.Detail, "would create 2") || len(f.Writes()) != 0 {
		t.Errorf("row = %+v writes %d", r, len(f.Writes()))
	}
}

func TestRunNeverDeletes(t *testing.T) {
	f := fake.New()
	if err := f.CreateLabel(context.Background(), github.Label{Name: "ard:old", Color: "000000"}); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), deps(f), Opts{Check: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if r := labelRow(t, rep); !strings.Contains(r.Detail, "ard:old") {
		t.Errorf("detail = %q", r.Detail)
	}
	if _, err := Run(context.Background(), deps(f), Opts{}, io.Discard); err != nil {
		t.Fatal(err)
	}
	got, _ := f.ListLabels(context.Background())
	found := false
	for _, l := range got {
		found = found || l.Name == "ard:old"
	}
	if !found {
		t.Error("ard:old was deleted")
	}
}

type failWriter struct{ labelsync.Writer }

func (failWriter) CreateLabel(_ context.Context, l github.Label) error {
	if l.Name == "ard:hold" {
		return errors.New("boom")
	}
	return nil
}

func TestRunSyncFailureManualRow(t *testing.T) {
	f := fake.New()
	d := deps(f)
	d.Writer = failWriter{}
	rep, err := Run(context.Background(), d, Opts{}, io.Discard)
	if err == nil {
		t.Fatal("want an error")
	}
	if r := labelRow(t, rep); r.Status != StatusManual || !strings.Contains(r.Detail, "ard:hold") {
		t.Errorf("row = %+v", r)
	}
}

func TestRunOriginMainWarning(t *testing.T) {
	f := fake.New()
	d := deps(f)
	d.LocalVsMain = func(context.Context) (string, error) { return "differs", nil }
	var out bytes.Buffer
	rep, err := Run(context.Background(), d, Opts{}, &out)
	if err != nil || len(rep.Rows) != 2 || out.String() != "warning: differs\n" {
		t.Errorf("err=%v rows=%d out=%q", err, len(rep.Rows), out.String())
	}
}

func TestPrintRows(t *testing.T) {
	var b bytes.Buffer
	Print(&b, Report{Rows: []Row{
		{"a", StatusOK, ""}, {"b", StatusChanged, "created 1"}, {"c", StatusDrift, "1 to create"}, {"d", StatusManual, "do x"},
	}}, false)
	const w = "ok      a\nchanged b: created 1\ndrift   c: 1 to create\nmanual: d: do x\n"
	if b.String() != w {
		t.Errorf("got %q want %q", b.String(), w)
	}
}

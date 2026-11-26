package setupcmd

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/labels"
)

type varRec struct {
	calls [][3]string
	err   error
}

func (v *varRec) SetRepoVariable(_ context.Context, repo, name, value string) error {
	v.calls = append(v.calls, [3]string{repo, name, value})
	return v.err
}

func botDeps(f *fake.Fake, v *varRec, confirm func(string) (bool, error)) Deps {
	d := policyDeps(f)
	d.Variables, d.Confirm = v, confirm
	return d
}

func runBot(t *testing.T, d Deps, o Opts) Row {
	t.Helper()
	rep, err := Run(context.Background(), d, o, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return row(t, rep, botLoginVar)
}

func TestBotLoginAbsentIsSet(t *testing.T) {
	v := &varRec{}
	r := runBot(t, botDeps(policyFake(), v, nil), Opts{})
	if r.Status != StatusChanged || len(v.calls) != 1 || v.calls[0] != [3]string{"o/r", "ARD_BOT_LOGIN", "ard-test[bot]"} {
		t.Errorf("row %+v calls %v", r, v.calls)
	}
}

func TestBotLoginEqualNoWrite(t *testing.T) {
	f := policyFake()
	f.SetVariable(botLoginVar, " ard-test[bot]\n")
	v := &varRec{}
	if r := runBot(t, botDeps(f, v, nil), Opts{}); r.Status != StatusOK || len(v.calls) != 0 {
		t.Errorf("row %+v calls %v", r, v.calls)
	}
}

func TestBotLoginDifferentAsksFirst(t *testing.T) {
	cases := []struct {
		name    string
		confirm func(string) (bool, error)
		writes  int
	}{
		{"yes", func(string) (bool, error) { return true, nil }, 1},
		{"no", func(string) (bool, error) { return false, nil }, 0},
		{"nil", nil, 0},
		{"error", func(string) (bool, error) { return true, errors.New("x") }, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := policyFake()
			f.SetVariable(botLoginVar, "other[bot]")
			v := &varRec{}
			r := runBot(t, botDeps(f, v, c.confirm), Opts{})
			if len(v.calls) != c.writes {
				t.Fatalf("writes = %d", len(v.calls))
			}
			if c.writes == 0 && (r.Status != StatusManual || !contains(r.Detail, "gh variable set ARD_BOT_LOGIN -R o/r --body 'ard-test[bot]'")) {
				t.Errorf("row %+v", r)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestBotLoginCheckOnly(t *testing.T) {
	v := &varRec{}
	rep, err := Run(context.Background(), botDeps(policyFake(), v, nil), Opts{Check: true}, io.Discard)
	if err != nil || len(v.calls) != 0 || !rep.HasDrift() {
		t.Errorf("err %v calls %v drift %v", err, v.calls, rep.HasDrift())
	}
}

func TestBotLoginDryRun(t *testing.T) {
	f := policyFake()
	d := botDeps(f, &varRec{}, nil)
	dr, err := effects.NewDryRun(effects.DryRunOptions{Log: quiet})
	if err != nil {
		t.Fatal(err)
	}
	d.Variables, d.DryRun = dr, true
	if r := runBot(t, d, Opts{}); r.Status != StatusChanged || r.Detail != "would set to ard-test[bot]" {
		t.Errorf("row %+v", r)
	}
}

func TestBotLoginNotABot(t *testing.T) {
	f := fake.New(fake.WithIdentity(github.Identity{Login: "alice", Kind: github.AuthToken}))
	v := &varRec{}
	d := botDeps(f, v, nil)
	d.Policy = f
	rep, err := Run(context.Background(), d, Opts{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if r := row(t, rep, botLoginVar); r.Status != StatusManual || len(v.calls) != 0 {
		t.Errorf("row %+v calls %v", r, v.calls)
	}
}

func TestBotLoginNoTokenDoesNotAbort(t *testing.T) {
	f := policyFake()
	v := &varRec{err: effects.ErrNoOperatorToken}
	d := botDeps(f, v, nil)
	rep, err := Run(context.Background(), d, Opts{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if r := row(t, rep, botLoginVar); r.Status != StatusManual {
		t.Errorf("row %+v", r)
	}
	if r := labelRow(t, rep); r.Status != StatusChanged {
		t.Errorf("labels not synced: %+v", r)
	}
}

func TestBotLoginForbiddenRead(t *testing.T) {
	f := policyFake()
	f.FailNext("GetVariable", github.ErrForbidden) // consumed by the ARD_VERSIONING read
	f.FailNext("GetVariable", github.ErrForbidden)
	v := &varRec{}
	rep, err := Run(context.Background(), botDeps(f, v, nil), Opts{}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if r := row(t, rep, botLoginVar); r.Status != StatusManual || len(v.calls) != 0 {
		t.Errorf("row %+v calls %v", r, v.calls)
	}
}

func TestBotLoginNeverFromConfig(t *testing.T) {
	// A different login already set (as config or a stale value might say)
	// and a decoy login in Desired must not influence the value written.
	f := policyFake()
	f.SetVariable(botLoginVar, "config-says[bot]")
	v := &varRec{}
	d := botDeps(f, v, func(string) (bool, error) { return true, nil })
	d.Desired = append(d.Desired, labels.Label{Name: "config-says[bot]"})
	runBot(t, d, Opts{})
	if len(v.calls) != 1 || v.calls[0][2] != "ard-test[bot]" {
		t.Errorf("calls %v", v.calls)
	}
}

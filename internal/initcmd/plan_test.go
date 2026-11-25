package initcmd

import (
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/albertcbrown/ard/internal/committer"
)

func mf(data string, exec bool) *fstest.MapFile {
	mode := fs.FileMode(0o644)
	if exec {
		mode = 0o755
	}
	return &fstest.MapFile{Data: []byte(data), Mode: mode}
}

func lockOf(kv ...string) Lock {
	l := Lock{Files: map[string]string{}}
	for i := 0; i < len(kv); i += 2 {
		l.Files[kv[i]] = hashOf([]byte(kv[i+1]))
	}
	return l
}

func TestPlanUpgrade(t *testing.T) {
	const p = ".ard/triage/x/prompt.txt"
	files := []committer.InitFile{{Path: p, Data: []byte("new\n")}}
	execFiles := []committer.InitFile{{Path: p, Data: []byte("new\n"), Exec: true}}
	tests := []struct {
		name     string
		existing fstest.MapFS
		files    []committer.InitFile
		lock     Lock
		want     action
		diff     bool
	}{
		{"absent", fstest.MapFS{}, files, Lock{}, actCreate, false},
		{"absent with stale lock entry", fstest.MapFS{}, files, lockOf(p, "old\n"), actCreate, false},
		{"identical no lock", fstest.MapFS{p: mf("new\n", false)}, files, Lock{}, actUnchanged, false},
		{"identical with lock", fstest.MapFS{p: mf("new\n", false)}, files, lockOf(p, "old\n"), actUnchanged, false},
		{"hash matches lock", fstest.MapFS{p: mf("old\n", false)}, files, lockOf(p, "old\n"), actUpdate, false},
		{"hash differs from lock", fstest.MapFS{p: mf("mine\n", false)}, files, lockOf(p, "old\n"), actEdited, true},
		{"no lock", fstest.MapFS{p: mf("mine\n", false)}, files, Lock{}, actEdited, true},
		{"lock lacks entry", fstest.MapFS{p: mf("mine\n", false)}, files, lockOf(".ard/other", "x"), actEdited, true},
		{"exec bit only, lock matches", fstest.MapFS{p: mf("new\n", false)}, execFiles, lockOf(p, "new\n"), actUpdate, false},
		{"exec bit only, no lock", fstest.MapFS{p: mf("new\n", false)}, execFiles, Lock{}, actEdited, true},
		{"directory-free hand-edited lock entry", fstest.MapFS{p: mf("old\n", false)}, files, Lock{Files: map[string]string{p: "ZZ"}}, actEdited, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := plan(tc.existing, tc.files, tc.lock)
			if err != nil {
				t.Fatal(err)
			}
			o := res.Ops[0]
			if o.Act != tc.want || (o.Diff != "") != tc.diff {
				t.Fatalf("act = %s diff = %q, want %s (diff %v)", o.Act, o.Diff, tc.want, tc.diff)
			}
			wrote := len(changed(res.Ops)) == 1
			if wrote != (tc.want == actCreate || tc.want == actUpdate) {
				t.Errorf("changed = %v", changed(res.Ops))
			}
		})
	}
	t.Run("directory on main", func(t *testing.T) {
		_, err := plan(fstest.MapFS{p + "/x": mf("", false)}, files, Lock{})
		if err == nil || !strings.Contains(err.Error(), "directory") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestPlanEditedDiff(t *testing.T) {
	files := []committer.InitFile{{Path: ".ard/a", Data: []byte("one\nthree\nfour\n")}}
	res, err := plan(fstest.MapFS{".ard/a": mf("one\ntwo\nthree\n", false)}, files, Lock{})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"--- .ard/a (main)", " one\n", "-two\n", " three\n", "+four\n"} {
		if !strings.Contains(res.Ops[0].Diff, w) {
			t.Errorf("diff lacks %q:\n%s", w, res.Ops[0].Diff)
		}
	}
}

func TestPlanContextDocs(t *testing.T) {
	files := []committer.InitFile{{Path: ".ard/PRODUCT.md", Data: []byte("starter\n")}}
	lock := lockOf(".ard/PRODUCT.md", "old\n")
	res, _ := plan(fstest.MapFS{}, files, lock)
	if res.Ops[0].Act != actCreate {
		t.Errorf("absent: %s", res.Ops[0].Act)
	}
	// edited, or matching its lock entry: never updated
	for _, c := range []string{"mine\n", "old\n"} {
		res, err := plan(fstest.MapFS{".ard/PRODUCT.md": mf(c, true)}, files, lock)
		if err != nil || res.Ops[0].Act != actUnchanged || len(res.Dropped) != 0 {
			t.Errorf("present %q: %v %v", c, res, err)
		}
	}
}

func TestPlanDropped(t *testing.T) {
	files := []committer.InitFile{{Path: ".ard/keep", Data: []byte("k")}}
	lock := lockOf(".ard/keep", "k", ".ard/old2", "x", ".ard/old1", "x", ".ard/PRODUCT.md", "x")
	for _, existing := range []fstest.MapFS{{}, {".ard/old1": mf("x", false)}} {
		res, err := plan(existing, files, lock)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(res.Dropped, []string{".ard/old1", ".ard/old2"}) {
			t.Errorf("Dropped = %v", res.Dropped)
		}
		for _, o := range res.Ops {
			if o.File.Path != ".ard/keep" {
				t.Errorf("unexpected op %+v", o)
			}
		}
	}
}

func TestParseLockHandEdited(t *testing.T) {
	good := hashOf([]byte("x"))
	tests := []struct {
		name, in string
		files    []string
		warn     bool
	}{
		{"empty", "", nil, false},
		{"garbage", "{{{ not yaml: [", nil, true},
		{"files is a list", "version: v1\nfiles:\n  - a\n", nil, true},
		{"scalar document", "hello\n", nil, true},
		{"short hash", "files:\n  .ard/triage/x/p: abc\n", nil, true},
		{"uppercase hash", "files:\n  .ard/triage/x/p: " + strings.ToUpper(good) + "\n", nil, true},
		{"non-string hash", "files:\n  .ard/triage/x/p: 12\n", nil, true},
		{"path outside init paths", "files:\n  ../../etc/passwd: " + good + "\n", nil, true},
		{"extra keys", "extra: 1\nfiles:\n  .ard/triage/x/p: " + good + "\n", []string{".ard/triage/x/p"}, false},
		{"one good one bad", "files:\n  .ard/triage/x/p: " + good + "\n  .ard/triage/x/q: nope\n", []string{".ard/triage/x/p"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, warns := ParseLock([]byte(tc.in))
			var got []string
			for k := range l.Files {
				got = append(got, k)
			}
			if !slices.Equal(got, tc.files) || (len(warns) > 0) != tc.warn {
				t.Errorf("files = %v warns = %v", got, warns)
			}
		})
	}
	// An ignored entry plans as edited.
	l, _ := ParseLock([]byte("files:\n  .ard/triage/x/p: abc\n"))
	files := []committer.InitFile{{Path: ".ard/triage/x/p", Data: []byte("new")}}
	res, _ := plan(fstest.MapFS{".ard/triage/x/p": mf("old", false)}, files, l)
	if res.Ops[0].Act != actEdited {
		t.Errorf("act = %s", res.Ops[0].Act)
	}
}

func TestLockMarshalRoundTrip(t *testing.T) {
	l := lockOf(".ard/triage/b/p", "b", ".ard/triage/a/p", "a")
	l.Version = "v1.2.3"
	out, err := l.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	out2, _ := l.Marshal()
	if string(out) != string(out2) || strings.Index(string(out), "triage/a") > strings.Index(string(out), "triage/b") {
		t.Errorf("not deterministic or sorted:\n%s", out)
	}
	back, warns := ParseLock(out)
	if len(warns) != 0 || back.Version != "v1.2.3" || len(back.Files) != 2 || back.Files[".ard/triage/a/p"] != l.Files[".ard/triage/a/p"] {
		t.Errorf("round trip: %+v %v", back, warns)
	}
}

func TestWithLock(t *testing.T) {
	old := lockOf(".ard/e", "old-e", ".ard/u", "old-u")
	ops := []op{
		{File: committer.InitFile{Path: ".ard/c", Data: []byte("c")}, Act: actCreate},
		{File: committer.InitFile{Path: ".ard/u", Data: []byte("new-u")}, Act: actUpdate},
		{File: committer.InitFile{Path: ".ard/e", Data: []byte("new-e")}, Act: actEdited},
		{File: committer.InitFile{Path: ".ard/n", Data: []byte("n")}, Act: actUnchanged},
	}
	n := withLock(old, ops, "v9")
	want := map[string]string{".ard/c": hashOf([]byte("c")), ".ard/u": hashOf([]byte("new-u")), ".ard/e": hashOf([]byte("old-e"))}
	if n.Version != "v9" || len(n.Files) != 3 {
		t.Fatalf("lock = %+v", n)
	}
	for k, v := range want {
		if n.Files[k] != v {
			t.Errorf("%s = %s, want %s", k, n.Files[k], v)
		}
	}
	if old.Files[".ard/u"] != hashOf([]byte("old-u")) {
		t.Error("old lock mutated")
	}
}

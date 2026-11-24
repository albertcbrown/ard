package roles

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// jqRun runs one of the lts jq programs with the given args and returns the
// output lines and any error.
func ltsJQ(t *testing.T, prog string, args ...string) ([]string, error) {
	t.Helper()
	full := append([]string{"-c", "-n"}, args...)
	full = append(full, "-f", filepath.Join("scanners", "lts", prog))
	var so, se bytes.Buffer
	cmd := exec.Command("jq", full...)
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	if err != nil {
		return nil, err
	}
	s := strings.TrimSpace(so.String())
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLtsRefsGolden(t *testing.T) {
	needTools(t, "jq")
	products := filepath.Join("scanners", "lts", "products.json")
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"go directive is a floor", `{"kind":"gomod","path":"go.mod","text":"module x\n\ngo 1.21\n"}`,
			[]string{`{"product":"go","version":"1.21","path":"go.mod","line":3,"ref":"go 1.21","floor":true}`}},
		{"toolchain wins", `{"kind":"gomod","path":"go.mod","text":"go 1.21\ntoolchain go1.21.5\n"}`,
			[]string{`{"product":"go","version":"1.21.5","path":"go.mod","line":2,"ref":"toolchain go1.21.5","floor":false}`}},
		{"engines lowest bound", `{"kind":"packagejson","path":"package.json","text":"{\"engines\":{\"node\":\">=16.0.0 <21\"}}"}`,
			[]string{`{"product":"nodejs","version":"16.0.0","path":"package.json","line":1,"ref":"engines.node >=16.0.0 <21","floor":true}`}},
		{"engines or-range", `{"kind":"packagejson","path":"package.json","text":"{\"engines\":{\"node\":\"^20 || ^18\"}}"}`,
			[]string{`{"product":"nodejs","version":"18","path":"package.json","line":1,"ref":"engines.node ^20 || ^18","floor":true}`}},
		{"engines star skipped", `{"kind":"packagejson","path":"package.json","text":"{\"engines\":{\"node\":\"*\"}}"}`,
			[]string{`{"skip":"engines.node range not understood in package.json"}`}},
		{"npm table only", `{"kind":"packagejson","path":"package.json","text":"{\"dependencies\":{\"vue\":\"^2.6\",\"left-pad\":\"1.0.0\"}}"}`,
			[]string{`{"product":"vue","version":"2.6","path":"package.json","line":1,"ref":"vue ^2.6","floor":true}`}},
		{"dockerfile matrix", `{"kind":"dockerfile","path":"Dockerfile","text":"ARG GO_IMAGE=golang:1.21\nFROM ${GO_IMAGE} AS build\nFROM --platform=$BUILDPLATFORM node:18-alpine\nFROM build\nFROM scratch\nFROM docker.io/library/python:3.8@sha256:abc\nfrom ubuntu:jammy\nFROM node:latest\nFROM ${UNSET}\nFROM ghcr.io/x/y:1\n"}`,
			[]string{
				`{"product":"go","version":"1.21","path":"Dockerfile","line":2,"ref":"FROM ${GO_IMAGE} AS build","floor":false}`,
				`{"product":"nodejs","version":"18","path":"Dockerfile","line":3,"ref":"FROM --platform=$BUILDPLATFORM node:18-alpine","floor":false}`,
				`{"product":"python","version":"3.8","path":"Dockerfile","line":6,"ref":"FROM docker.io/library/python:3.8@sha256:abc","floor":false}`,
				`{"product":"ubuntu","version":"jammy","path":"Dockerfile","line":7,"ref":"from ubuntu:jammy","floor":false}`,
				`{"skip":"unmapped image ghcr.io/x/y (Dockerfile)"}`,
				`{"skip":"unpinned tag for node (Dockerfile:8)"}`,
				`{"skip":"unresolved image \"${UNSET}\" (Dockerfile:9)"}`,
			}},
		{"helm", `{"kind":"helmvalues","path":"c/values.yaml","json":{"image":{"repository":"redis","tag":"7.0.5"},"sub":{"image":"nginx:1.25","db":{"image":{"repository":"postgres","tag":12}},"x":{"repository":"quay.io/a/b","tag":"1"}}}}`,
			[]string{
				`{"product":"nginx","version":"1.25","path":"c/values.yaml","line":0,"ref":"image nginx:1.25","floor":false}`,
				`{"product":"postgresql","version":"12","path":"c/values.yaml","line":0,"ref":"repository postgres tag 12","floor":false}`,
				`{"product":"redis","version":"7.0.5","path":"c/values.yaml","line":0,"ref":"repository redis tag 7.0.5","floor":false}`,
				`{"skip":"unmapped image quay.io/a/b (c/values.yaml)"}`,
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := writeTemp(t, "in.jsonl", c.in+"\n")
			got, err := ltsJQ(t, "refs.jq", "--slurpfile", "in", in, "--slurpfile", "map", products)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
		})
	}
	t.Run("unparseable package.json fails", func(t *testing.T) {
		in := writeTemp(t, "in.jsonl", `{"kind":"packagejson","path":"package.json","text":"{nope"}`+"\n")
		if _, err := ltsJQ(t, "refs.jq", "--slurpfile", "in", in, "--slurpfile", "map", products); err == nil {
			t.Fatal("want error")
		}
	})
}

// apiFile builds the one-array API input from the captured fixtures.
func ltsAPI(t *testing.T, products ...string) string {
	t.Helper()
	var parts []json.RawMessage
	for _, p := range products {
		b, err := os.ReadFile(filepath.Join("testdata", "lts", "api", p+".json"))
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, b)
	}
	b, _ := json.Marshal(parts)
	return writeTemp(t, "api.json", string(b))
}

const ltsSynthetic = `{"result":{"name":"syn","label":"Syn","releases":[
{"name":"1","codename":null,"isLts":false,"isEol":true,"eolFrom":null,"isMaintained":false,"latest":{"name":"1.9"}},
{"name":"2","codename":null,"isLts":false,"isEol":false,"eolFrom":"2026-02-23","isMaintained":true,"latest":{"name":"2.9"}},
{"name":"3","codename":null,"isLts":false,"isEol":false,"eolFrom":"2026-05-24","isMaintained":true,"latest":{"name":"3.9"}},
{"name":"4","codename":null,"isLts":false,"isEol":false,"eolFrom":"2026-05-25","isMaintained":true,"latest":{"name":"4.9"}},
{"name":"5","codename":null,"isLts":false,"isEol":false,"eolFrom":null,"isMaintained":true,"latest":{"name":"5.9"}}]}}`

func TestLtsEolGolden(t *testing.T) {
	needTools(t, "jq")
	ref := func(product, version, path string, line int, text string, floor bool) string {
		b, _ := json.Marshal(map[string]any{"product": product, "version": version, "path": path, "line": line, "ref": text, "floor": floor})
		return string(b)
	}
	// today is 2026-02-23: cycle 2 is due today (high), 3 in exactly 90 days
	// (medium), 4 in 91 days (low), 1 has isEol with no date (high).
	syn := writeTemp(t, "syn.json", "["+ltsSynthetic+"]")
	cases := []struct {
		name   string
		api    string
		today  string
		refs   []string
		want   map[string]string // fingerprint -> severity
		skips  int
		errors bool
	}{
		{"past eol is high", ltsAPI(t, "go"), "2026-11-24", []string{ref("go", "1.20.3", "Dockerfile", 1, "FROM golang:1.20.3", false)}, map[string]string{"eol:go:1.20": "high"}, 0, false},
		{"supported is nothing", ltsAPI(t, "go"), "2026-11-24", []string{ref("go", "1.27.4", "Dockerfile", 1, "FROM golang:1.27", false)}, map[string]string{}, 0, false},
		{"floor only is capped", ltsAPI(t, "go"), "2026-11-24", []string{ref("go", "1.20", "go.mod", 3, "go 1.20", true)}, map[string]string{"eol:go:1.20": "medium"}, 0, false},
		{"floor plus runtime is not capped", ltsAPI(t, "go"), "2026-11-24", []string{ref("go", "1.20", "go.mod", 3, "go 1.20", true), ref("go", "1.20", "Dockerfile", 1, "FROM golang:1.20", false)}, map[string]string{"eol:go:1.20": "high"}, 0, false},
		{"two cycles two findings", ltsAPI(t, "go"), "2026-11-24", []string{ref("go", "1.20", "a/Dockerfile", 1, "FROM golang:1.20", false), ref("go", "1.19", "b/Dockerfile", 1, "FROM golang:1.19", false)}, map[string]string{"eol:go:1.19": "high", "eol:go:1.20": "high"}, 0, false},
		{"codename", ltsAPI(t, "ubuntu"), "2030-01-01", []string{ref("ubuntu", "jammy", "Dockerfile", 1, "FROM ubuntu:jammy", false)}, map[string]string{"eol:ubuntu:22.04": "high"}, 0, false},
		{"no cycle skips", ltsAPI(t, "go"), "2026-11-24", []string{ref("go", "9.9", "Dockerfile", 1, "FROM golang:9.9", false)}, map[string]string{}, 1, false},
		{"missing api product", ltsAPI(t, "go"), "2026-11-24", []string{ref("nodejs", "18", "Dockerfile", 1, "FROM node:18", false)}, nil, 0, true},
		{"isEol without date", syn, "2026-02-23", []string{ref("syn", "1", "Dockerfile", 1, "x", false)}, map[string]string{"eol:syn:1": "high"}, 0, false},
		{"due today", syn, "2026-02-23", []string{ref("syn", "2", "Dockerfile", 1, "x", false)}, map[string]string{"eol:syn:2": "high"}, 0, false},
		{"90 day boundary", syn, "2026-02-23", []string{ref("syn", "3", "Dockerfile", 1, "x", false)}, map[string]string{"eol:syn:3": "medium"}, 0, false},
		{"91 days is low", syn, "2026-02-23", []string{ref("syn", "4", "Dockerfile", 1, "x", false)}, map[string]string{"eol:syn:4": "low"}, 0, false},
		{"180 day boundary", syn, "2025-11-25", []string{ref("syn", "3", "Dockerfile", 1, "x", false)}, map[string]string{"eol:syn:3": "low"}, 0, false},
		{"beyond 180 days", syn, "2025-11-24", []string{ref("syn", "3", "Dockerfile", 1, "x", false)}, map[string]string{}, 0, false},
		{"no date not eol", syn, "2026-02-23", []string{ref("syn", "5", "Dockerfile", 1, "x", false)}, map[string]string{}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			refs := writeTemp(t, "refs.jsonl", strings.Join(c.refs, "\n")+"\n")
			got, err := ltsJQ(t, "eol.jq", "--arg", "today", c.today, "--slurpfile", "refs", refs, "--slurpfile", "api", c.api)
			if c.errors {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			found, skips := map[string]string{}, 0
			for _, l := range got {
				var f struct{ Fingerprint, Severity, Group, Skip string }
				if err := json.Unmarshal([]byte(l), &f); err != nil {
					t.Fatal(err)
				}
				if f.Skip != "" {
					skips++
				} else {
					found[f.Fingerprint] = f.Severity
				}
			}
			if len(found) != len(c.want) || skips != c.skips {
				t.Fatalf("got %v (%d skips), want %v (%d skips)", found, skips, c.want, c.skips)
			}
			for k, v := range c.want {
				if found[k] != v {
					t.Errorf("%s: severity %q, want %q", k, found[k], v)
				}
			}
		})
	}

	t.Run("one finding lists all files, title and shape", func(t *testing.T) {
		refs := writeTemp(t, "refs.jsonl", ref("go", "1.20", "b/go.mod", 3, "go 1.20", true)+"\n"+ref("go", "1.20.1", "a/Dockerfile", 1, "FROM golang:1.20.1", false)+"\n")
		got, err := ltsJQ(t, "eol.jq", "--arg", "today", "2026-11-24", "--slurpfile", "refs", refs, "--slurpfile", "api", ltsAPI(t, "go"))
		if err != nil || len(got) != 1 {
			t.Fatalf("%v %v", got, err)
		}
		if !strings.HasPrefix(got[0], `{"fingerprint":"eol:go:1.20","title":"Go 1.20: end of life 2024-02-06","severity":"high","group":"go","files":["a/Dockerfile","b/go.mod"],"body":"`) {
			t.Errorf("unexpected: %s", got[0])
		}
		f := decode(t, []byte(got[0]))
		if !strings.Contains(f[0].Body, "- `a/Dockerfile:1`: `FROM golang:1.20.1`\n- `b/go.mod:3`: `go 1.20`") {
			t.Errorf("body: %s", f[0].Body)
		}
	})
	t.Run("hostile text is inert and refs are truncated", func(t *testing.T) {
		var refs []string
		refs = append(refs, ref("go", "1.20", "Dockerfile", 1, "FROM `x`\n@evil <!-- "+strings.Repeat("a", 300), false))
		for i := 2; i <= 52; i++ {
			refs = append(refs, ref("go", "1.20", "f"+string(rune('a'+i%26))+strings.Repeat("z", i)+"/Dockerfile", 1, "FROM golang:1.20", false))
		}
		in := writeTemp(t, "refs.jsonl", strings.Join(refs, "\n")+"\n")
		got, err := ltsJQ(t, "eol.jq", "--arg", "today", "2026-11-24", "--slurpfile", "refs", in, "--slurpfile", "api", ltsAPI(t, "go"))
		if err != nil || len(got) != 1 {
			t.Fatalf("%v %v", got, err)
		}
		body := decode(t, []byte(got[0]))[0].Body
		if !strings.Contains(body, "…and 2 more") || strings.Count(body, "\n- `") != 50 {
			t.Errorf("not truncated: %d refs", strings.Count(body, "\n- `"))
		}
		if !strings.Contains(body, "- `Dockerfile:1`: `FROM x @evil <!-- aaa") || strings.Contains(body, strings.Repeat("a", 201)) {
			t.Errorf("hostile ref not neutralised: %.300s", body)
		}
	})
}

// ltsEnv is a hermetic scan environment: a fake curl serving the captured API
// fixtures, plus jq (and optionally yq) and the system's basic tools.
type ltsEnv struct {
	bin, repo string
}

func newLtsEnv(t *testing.T, withYq bool) *ltsEnv {
	t.Helper()
	e := &ltsEnv{bin: t.TempDir(), repo: t.TempDir()}
	tools := []string{"jq"}
	if withYq {
		tools = append(tools, "yq")
	}
	for _, n := range tools {
		p, err := exec.LookPath(n)
		if err != nil {
			t.Skipf("%s not available", n)
		}
		if err := os.Symlink(p, filepath.Join(e.bin, n)); err != nil {
			t.Fatal(err)
		}
	}
	api, _ := filepath.Abs(filepath.Join("testdata", "lts", "api"))
	curl := `#!/bin/sh
prev= out= url=
for a; do
  [ "$prev" = -o ] && out=$a
  prev=$a url=$a
done
[ -z "${CURL_FAIL:-}" ] || exit 7
p=${url##*/}
if [ "$p" = "${CURL_404:-}" ] || [ ! -f "` + api + `/$p.json" ]; then printf 404; exit 0; fi
cp "` + api + `/$p.json" "$out"
printf 200
`
	if err := os.WriteFile(filepath.Join(e.bin, "curl"), []byte(curl), 0o755); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *ltsEnv) run(t *testing.T, files map[string]string, extra ...string) (string, string, int) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(e.repo, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scan, _ := filepath.Abs(filepath.Join("scanners", "lts", "scan"))
	cmd := exec.Command(scan)
	cmd.Dir = e.repo
	cmd.Env = append([]string{"PATH=" + e.bin + ":/usr/bin:/bin", "HOME=/nonexistent", "TMPDIR=" + t.TempDir(), "LTS_TODAY=2026-11-24"}, extra...)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return so.String(), se.String(), code
}

func TestLtsScanHermetic(t *testing.T) {
	needTools(t, "jq")
	t.Run("empty repo", func(t *testing.T) {
		out, _, code := newLtsEnv(t, false).run(t, nil)
		if code != 0 || out != "" {
			t.Errorf("code %d out %q", code, out)
		}
	})
	t.Run("mixed repo", func(t *testing.T) {
		out, _, code := newLtsEnv(t, false).run(t, map[string]string{
			"go.mod":                        "module x\n\ngo 1.20\n",
			"svc/Dockerfile":                "FROM node:16-alpine\nFROM debian:bookworm\n",
			"testdata/old/Dockerfile":       "FROM python:2.7\n",
			"web/node_modules/x/Dockerfile": "FROM python:2.7\n",
		})
		if code != 0 {
			t.Fatalf("code %d", code)
		}
		got := map[string]string{}
		for _, f := range decode(t, []byte(out)) {
			got[f.Fingerprint] = f.Severity
		}
		want := map[string]string{"eol:go:1.20": "medium", "eol:nodejs:16": "high"}
		if len(got) != 2 || got["eol:go:1.20"] != want["eol:go:1.20"] || got["eol:nodejs:16"] != want["eol:nodejs:16"] {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("helm values", func(t *testing.T) {
		out, _, code := newLtsEnv(t, true).run(t, map[string]string{
			"chart/Chart.yaml":  "name: c\nversion: 1.0.0\n",
			"chart/values.yaml": "image:\n  repository: redis\n  tag: \"5.0.14\"\n",
		})
		fs := decode(t, []byte(out))
		if code != 0 || len(fs) != 1 || fs[0].Fingerprint != "eol:redis:5.0" {
			t.Errorf("code %d out %q", code, out)
		}
	})
	failures := []struct {
		name  string
		files map[string]string
		extra []string
		yq    bool
		code  int
	}{
		{"mapped product 404", map[string]string{"Dockerfile": "FROM node:16\n"}, []string{"CURL_404=nodejs"}, false, 1},
		{"curl error", map[string]string{"Dockerfile": "FROM node:16\n"}, []string{"CURL_FAIL=1"}, false, 1},
		{"bad package.json", map[string]string{"package.json": "{nope", "Dockerfile": "FROM node:16\n"}, nil, false, 1},
		{"bad LTS_TODAY", nil, []string{"LTS_TODAY=tomorrow"}, false, 1},
		{"Chart.yaml without yq", map[string]string{"c/Chart.yaml": "name: c\n"}, nil, false, 2},
	}
	for _, c := range failures {
		t.Run(c.name, func(t *testing.T) {
			out, _, code := newLtsEnv(t, c.yq).run(t, c.files, c.extra...)
			if code != c.code || out != "" {
				t.Errorf("code %d (want %d), stdout %q", code, c.code, out)
			}
		})
	}
}

package docscheck

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/config"
)

func one(body string) fstest.MapFS {
	return fstest.MapFS{"a.md": {Data: []byte(body)}}
}

func TestCheckCommandsFixtures(t *testing.T) {
	paths := cli.CommandPaths()
	cases := []struct {
		name, body, want string
	}{
		{"unknown command", "Run `ard bogus`.", `unknown command "ard bogus"`},
		{"unknown flag", "Run `ard serve --nope`.", "no flag --nope"},
		{"known flag", "Run `ard serve --dry-run`.", ""},
		{"global flag on subcommand", "`ard config check --repo a/b --ref main`", ""},
		{"fenced prompt", "```sh\n$ ard labels sync --dry-run\n```", ""},
		{"fenced bad", "```sh\nard labels nope\n```", "unknown command"},
		{"ignored", "<!-- docscheck:ignore -->\nRun `ard bogus`.", ""},
		{"prose only", "ard is a tool. `ard:pending` and `ard-bot`.", ""},
		{"placeholder arg", "`ard rebuild-state <scanner>`", ""},
	}
	for _, c := range cases {
		got, err := CheckCommands(one(c.body), paths)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(got, "\n")
		if c.want == "" && joined != "" || !strings.Contains(joined, c.want) {
			t.Errorf("%s: problems = %q, want %q", c.name, joined, c.want)
		}
	}
}

func TestCheckKeysFixtures(t *testing.T) {
	keys := []string{"max_pods", "paused"}
	got, _ := CheckKeys(one("| `max_pods` | 3 |\n| `old_key` | x |\n"), "a.md", keys)
	sort.Strings(got)
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, `"paused" is not documented`) || !strings.Contains(joined, `"old_key" does not exist`) {
		t.Errorf("problems = %q", joined)
	}
	if got, _ := CheckKeys(one("| `max_pods` |\n| `paused` |\n"), "a.md", keys); len(got) != 0 {
		t.Errorf("unexpected problems %q", got)
	}
}

func siteDir(t *testing.T) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "docs", "site")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func TestRealDocsCommandsExist(t *testing.T) {
	problems, err := CheckCommands(os.DirFS(siteDir(t)), cli.CommandPaths())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

func TestRealDocsConfigKeysMatch(t *testing.T) {
	problems, err := CheckKeys(os.DirFS(siteDir(t)), "configuration.md", config.Keys())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

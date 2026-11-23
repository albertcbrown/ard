//go:build integration

package roles

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const mitText = "MIT License\n\nCopyright (c) 2024 Example\n\nPermission is hereby granted, free of charge, to any person obtaining a copy\nof this software and associated documentation files (the \"Software\"), to deal\nin the Software without restriction, including without limitation the rights\nto use, copy, modify, merge, publish, distribute, sublicense, and/or sell\ncopies of the Software, and to permit persons to whom the Software is\nfurnished to do so, subject to the following conditions:\n\nThe above copyright notice and this permission notice shall be included in all\ncopies or substantial portions of the Software.\n\nTHE SOFTWARE IS PROVIDED \"AS IS\", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR\nIMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,\nFITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE\nAUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER\nLIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,\nOUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE\nSOFTWARE.\n"

// Needs go and go-licenses on PATH; the module graph is local, so no network.
func TestIntegrationGoLicensesBlocks(t *testing.T) {
	for _, n := range []string{"go-licenses", "go"} {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("%s not on PATH", n)
		}
	}
	dir := t.TempDir()
	write := func(name, s string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/fixture\n\ngo 1.21\n\nrequire example.com/dep v0.0.0\n\nreplace example.com/dep => ./dep\n")
	write("main.go", "package main\n\nimport _ \"example.com/dep\"\n\nfunc main() {}\n")
	write("dep/go.mod", "module example.com/dep\n\ngo 1.21\n")
	write("dep/dep.go", "package dep\n")
	write("dep/LICENSE", mitText)
	run := func(allowed string) (string, error) {
		args := strings.Fields(goLicensesCheck + allowed + " --ignore example.com/fixture")
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run("Apache-2.0")
	if err == nil || !strings.Contains(out, "example.com/dep") || !strings.Contains(out, "MIT") {
		t.Errorf("Apache-only allowlist: err=%v\n%s", err, out)
	}
	if out, err := run("MIT"); err != nil {
		t.Errorf("MIT allowlist: %v\n%s", err, out)
	}
}

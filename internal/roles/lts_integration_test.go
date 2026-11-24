//go:build integration

package roles

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestIntegrationLtsProductsExist fetches every slug in products.json from
// the live API: a stale table makes the scan fail closed.
func TestIntegrationLtsProductsExist(t *testing.T) {
	b, err := FS.ReadFile("scanners/lts/products.json")
	if err != nil {
		t.Fatal(err)
	}
	var tables map[string]map[string]string
	if err := json.Unmarshal(b, &tables); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, m := range tables {
		for _, slug := range m {
			resp, err := client.Get("https://endoflife.date/api/v1/products/" + slug)
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Result struct{ Releases []json.RawMessage }
			}
			derr := json.NewDecoder(resp.Body).Decode(&body)
			resp.Body.Close()
			if resp.StatusCode != 200 || derr != nil || len(body.Result.Releases) == 0 {
				t.Errorf("%s: status %d, %d releases, decode %v", slug, resp.StatusCode, len(body.Result.Releases), derr)
			}
		}
	}
}

// TestIntegrationLtsRealAPI runs the real scan against the live API. Go 1.20
// and Node.js 16 are end of life for good.
func TestIntegrationLtsRealAPI(t *testing.T) {
	needTools(t, "jq", "curl")
	repo := t.TempDir()
	for p, c := range map[string]string{"Dockerfile": "FROM node:16\n", "go.mod": "module x\n\ngo 1.20\ntoolchain go1.20.1\n"} {
		if err := os.WriteFile(filepath.Join(repo, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scan, _ := filepath.Abs(filepath.Join("scanners", "lts", "scan"))
	cmd := exec.Command(scan)
	cmd.Dir = repo
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v\n%s", err, se.String())
	}
	got := map[string]string{}
	for _, f := range decode(t, so.Bytes()) {
		got[f.Fingerprint] = f.Severity
	}
	if got["eol:go:1.20"] != "high" || got["eol:nodejs:16"] != "high" {
		t.Errorf("got %v", got)
	}
}

package setupcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/albertcbrown/ard/internal/cli"
	"github.com/albertcbrown/ard/internal/github"
)

// CreateAppOpts are the user's choices for --create-gh-app.
type CreateAppOpts struct {
	Name    string // App name; default ard-<owner>-<repo>
	KeyPath string // private key file; default ~/.ard/keys/<slug>.pem
	Force   bool   // overwrite an existing key file
}

// createDeps are the seams tests replace. Zero values use the real thing.
type createDeps struct {
	apiBase      string
	hc           *http.Client
	openBrowser  func(string) error
	discover     func(ctx context.Context, appID int64, pem []byte, repo github.Repo) (int64, error)
	ownerIsOrg   func(ctx context.Context, owner string) (bool, error)
	callbackWait time.Duration
	pollEvery    time.Duration
	pollFor      time.Duration
}

func (d *createDeps) defaults() {
	if d.apiBase == "" {
		d.apiBase = github.DefaultAPIBaseURL()
	}
	if d.hc == nil {
		d.hc = &http.Client{Timeout: 30 * time.Second}
	}
	if d.openBrowser == nil {
		d.openBrowser = openBrowser
	}
	if d.discover == nil {
		d.discover = func(ctx context.Context, appID int64, pem []byte, repo github.Repo) (int64, error) {
			a := &github.AppAuth{AppID: appID, PrivateKey: pem}
			return a.DiscoverInstallation(ctx, repo, d.apiBase)
		}
	}
	if d.ownerIsOrg == nil {
		d.ownerIsOrg = func(ctx context.Context, owner string) (bool, error) {
			return lookupOwnerIsOrg(ctx, d.hc, d.apiBase, owner)
		}
	}
	if d.callbackWait == 0 {
		d.callbackWait = DefaultCallbackTimeout
	}
	if d.pollEvery == 0 {
		d.pollEvery = 3 * time.Second
	}
	if d.pollFor == 0 {
		d.pollFor = 5 * time.Minute
	}
}

// CreateApp creates ard's GitHub App through the manifest flow, stores the
// key and prints the environment lines. On success it also sets them in this
// process so the normal setup checks use the new App.
func CreateApp(ctx context.Context, e *cli.Env, repo github.Repo, o CreateAppOpts) error {
	var d createDeps
	return createApp(ctx, e, repo, o, d)
}

func createApp(ctx context.Context, e *cli.Env, repo github.Repo, o CreateAppOpts, d createDeps) error {
	d.defaults()
	name := o.Name
	if name == "" {
		name = "ard-" + repo.Owner + "-" + repo.Name
	}
	isOrg, err := d.ownerIsOrg(ctx, repo.Owner)
	if err != nil {
		return err
	}
	state, err := newState()
	if err != nil {
		return err
	}
	render := func(base string) ([]byte, string) {
		m, err := BuildManifest(name, base+"/callback")
		if err != nil {
			return []byte("{}"), ""
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		return b, ManifestURL(repo.Owner, isOrg, state)
	}
	if e.DryRun {
		b, _ := render("http://127.0.0.1:<port>")
		fmt.Fprintf(e.Stdout, "dry-run: would create a GitHub App; nothing was created.\nmanifest URL: %s\nmanifest:\n%s\n", ManifestURL(repo.Owner, isOrg, "<state>"), b)
		return nil
	}
	if _, err := BuildManifest(name, "http://127.0.0.1/callback"); err != nil {
		return err
	}
	// A fixed key path is checked now so the user does not create an App
	// that cannot be stored. The default path depends on the slug.
	if o.KeyPath != "" {
		if err := CheckKeyPath(o.KeyPath, o.Force); err != nil {
			return err
		}
	} else if _, err := DefaultKeyPath("x"); err != nil {
		return err
	}
	ready := func(local string) {
		fmt.Fprintf(e.Stdout, "Open this URL to create the App on GitHub (listening on loopback only):\n  %s\n", local)
		if err := d.openBrowser(local); err != nil {
			fmt.Fprintln(e.Stdout, "could not open a browser; open the URL above yourself")
		}
	}
	code, err := serveCallback(ctx, render, state, d.callbackWait, ready)
	if err != nil {
		return err
	}
	conv, err := Convert(ctx, d.apiBase, d.hc, code)
	if err != nil {
		return err
	}
	path := o.KeyPath
	if path == "" {
		if path, err = DefaultKeyPath(conv.Slug); err != nil {
			return err
		}
	}
	used, err := WriteKeyWithFallback(path, []byte(conv.PEM), o.Force)
	if err != nil {
		return fmt.Errorf("%w (the App %q was already created; its key could not be saved anywhere)", err, conv.Slug)
	}
	if used != path {
		fmt.Fprintf(e.Stdout, "could not write %s; the key was saved to %s instead.\n", path, used)
		path = used
	}
	fmt.Fprintf(e.Stdout, "App %q created; private key written to %s (mode 0600).\n", conv.Slug, path)
	installURL := "https://github.com/apps/" + conv.Slug + "/installations/new"
	fmt.Fprintf(e.Stdout, "Install it on %s: %s\n", repo, installURL)

	instID, err := waitInstallation(ctx, d, conv, repo)
	if err != nil {
		fmt.Fprintf(e.Stdout, "Set ARD_GITHUB_APP_ID=%d and ARD_GITHUB_PRIVATE_KEY_FILE=%s, install the App, then rerun `ard setup`.\n", conv.ID, path)
		return err
	}
	lines := [][2]string{
		{"ARD_GITHUB_APP_ID", strconv.FormatInt(conv.ID, 10)},
		{"ARD_GITHUB_INSTALLATION_ID", strconv.FormatInt(instID, 10)},
		{"ARD_GITHUB_PRIVATE_KEY_FILE", path},
	}
	fmt.Fprintln(e.Stdout, "Add these to ard's environment:")
	for _, l := range lines {
		fmt.Fprintf(e.Stdout, "  %s=%s\n", l[0], l[1])
		if err := os.Setenv(l[0], l[1]); err != nil {
			return fmt.Errorf("setup: %w", err)
		}
	}
	return nil
}

// waitInstallation polls until the App is installed on repo.
func waitInstallation(ctx context.Context, d createDeps, conv Conversion, repo github.Repo) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, d.pollFor)
	defer cancel()
	t := time.NewTicker(d.pollEvery)
	defer t.Stop()
	for {
		if id, err := d.discover(ctx, conv.ID, []byte(conv.PEM), repo); err == nil && id != 0 {
			return id, nil
		}
		select {
		case <-ctx.Done():
			return 0, errors.New("setup: timed out waiting for the App to be installed")
		case <-t.C:
		}
	}
}

func lookupOwnerIsOrg(ctx context.Context, hc *http.Client, apiBase, owner string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/users/"+url.PathEscape(owner), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := hc.Do(req)
	if err != nil {
		return false, fmt.Errorf("setup: look up owner %s: %w", owner, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("setup: look up owner %s: HTTP %d", owner, resp.StatusCode)
	}
	var out struct {
		Type string `json:"type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, fmt.Errorf("setup: look up owner %s: unexpected response", owner)
	}
	return out.Type == "Organization", nil
}

func openBrowser(u string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", u)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		c = exec.Command("xdg-open", u)
	}
	return c.Start()
}

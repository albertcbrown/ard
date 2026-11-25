package setupcmd

import (
	"fmt"
	"net/url"
	"sort"
)

// AppPermissions is the one built-in definition of the repository
// permissions ard's GitHub App needs. The manifest and the setup permission
// check both use it; it is never read from repository content.
var AppPermissions = map[string]string{
	"contents":      "write",
	"issues":        "write",
	"pull_requests": "write",
	"checks":        "write",
	"statuses":      "read",
	"metadata":      "read",
}

// ForbiddenPermissions must never be requested or held by the App.
var ForbiddenPermissions = []string{"workflows", "administration"}

// Manifest is the GitHub App manifest, see
// https://docs.github.com/apps/sharing-github-apps/registering-a-github-app-from-a-manifest.
type Manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	RedirectURL        string            `json:"redirect_url"`
	Public             bool              `json:"public"`
	DefaultEvents      []string          `json:"default_events"`
	DefaultPermissions map[string]string `json:"default_permissions"`
}

// manifestHomepage is the App's homepage URL; GitHub requires one.
const manifestHomepage = "https://github.com/albertcbrown/ard"

// BuildManifest returns the manifest for the built-in permission set. It has
// no webhook and is private. It fails if a forbidden permission is present.
func BuildManifest(name, redirectURL string) (Manifest, error) {
	return buildManifest(AppPermissions, name, redirectURL)
}

func buildManifest(perms map[string]string, name, redirectURL string) (Manifest, error) {
	for _, f := range ForbiddenPermissions {
		if _, ok := perms[f]; ok {
			return Manifest{}, fmt.Errorf("setup: permission %q must never be requested", f)
		}
	}
	cp := make(map[string]string, len(perms))
	for k, v := range perms {
		cp[k] = v
	}
	return Manifest{
		Name:               name,
		URL:                manifestHomepage,
		RedirectURL:        redirectURL,
		Public:             false,
		DefaultEvents:      []string{},
		DefaultPermissions: cp,
	}, nil
}

// ManifestURL is where the manifest form is POSTed: the user's settings, or
// the organization's for an org-owned repository.
func ManifestURL(owner string, ownerIsOrg bool, state string) string {
	base := "https://github.com/settings/apps/new"
	if ownerIsOrg {
		base = "https://github.com/organizations/" + url.PathEscape(owner) + "/settings/apps/new"
	}
	return base + "?state=" + url.QueryEscape(state)
}

// CheckPermissions compares granted repository permissions with the built-in
// definition. missing lists permissions that are absent or too weak; extra
// lists forbidden ones that are granted. Both are sorted.
func CheckPermissions(granted map[string]string) (missing, extra []string) {
	for k, want := range AppPermissions {
		got := granted[k]
		if got == want || (want == "read" && got == "write") {
			continue
		}
		missing = append(missing, k)
	}
	for _, f := range ForbiddenPermissions {
		if _, ok := granted[f]; ok {
			extra = append(extra, f)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

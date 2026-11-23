package gitx

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
)

// Version is a git release number.
type Version struct{ Major, Minor, Patch int }

// MinVersion is the oldest git the committer's fetches work on. git 2.39.5
// fails with "did not send all necessary objects" (#163); 2.47.3 passes. The
// exact boundary is unverified, so this is a conservative choice.
var MinVersion = Version{2, 47, 0}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Less reports whether v is older than o.
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

var fullVersionRe = regexp.MustCompile(`^git version (\d+)\.(\d+)(?:\.(\d+))?`)

// ParseVersion parses the output of `git --version`, tolerating a missing
// patch number and vendor suffixes.
func ParseVersion(s string) (Version, error) {
	m := fullVersionRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("gitx: cannot parse git version from %q", s)
	}
	var n [3]int
	for i := range n {
		if m[i+1] == "" {
			continue
		}
		v, err := strconv.Atoi(m[i+1])
		if err != nil {
			return Version{}, fmt.Errorf("gitx: cannot parse git version from %q", s)
		}
		n[i] = v
	}
	return Version{n[0], n[1], n[2]}, nil
}

// FullVersion runs `git --version` and returns the full number.
func (g *Git) FullVersion(ctx context.Context) (Version, error) {
	out, _, err := g.run(ctx, "", nil, "", nil, "--version")
	if err != nil {
		return Version{}, err
	}
	return ParseVersion(out)
}

// CheckVersion returns an error when git is missing, unparseable or older
// than MinVersion.
func (g *Git) CheckVersion(ctx context.Context) error {
	v, err := g.FullVersion(ctx)
	if err != nil {
		return fmt.Errorf("check git version: %w", err)
	}
	if v.Less(MinVersion) {
		return fmt.Errorf("git %s is too old: ard needs git %s or newer (the committer's fetches fail on older versions)", v, MinVersion)
	}
	return nil
}

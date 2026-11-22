package closeout

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/config"
)

// Writable is what one job may change under .ard/<n>/. Close-out never
// collects logs/, so it is not listed.
type Writable struct {
	RoleFile string   // .ard/<n>/<stage>-<role>.md
	Prefixes []string // directory prefixes ending in "/", e.g. ".ard/7/design/"
	Section  string   // the plan.md heading this role owns, without "## "; "" = plan.md not writable
}

// WritableFor returns the write allowlist of a job. n must already be
// positive (Prepare passes the dry-run-adjusted number). section is the
// plan.md heading the role owns (config.Role.PlanSection); it is granted only
// to triage roles.
func WritableFor(n int, stage, role, section string) Writable {
	w := Writable{RoleFile: agent.RoleFilePath(n, stage, role)}
	if stage != StageTriage {
		return w
	}
	w.Section = section
	if role == "designer" {
		w.Prefixes = []string{fmt.Sprintf(".ard/%d/design/", n)}
	}
	return w
}

// sectionOf finds the plan.md section of a triage role in the loaded config.
func sectionOf(rs RoleSet, stage, role string) string {
	if stage != StageTriage || rs == nil {
		return ""
	}
	r, ok := rs.Role(config.KindTriage, role)
	if !ok {
		return ""
	}
	return r.PlanSection
}

// checkWrites returns "" when every candidate is allowed, or a reason that
// names the first denied paths. Only a context error is returned as an error.
func checkWrites(ctx context.Context, j Job, d Deps, n int, cands []candidate) (string, error) {
	w := WritableFor(n, j.Stage, j.Role, sectionOf(d.Roles, j.Stage, j.Role))
	planPath := fmt.Sprintf(".ard/%d/plan.md", n)
	sorted := append([]candidate(nil), cands...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].path < sorted[b].path })

	type denial struct{ path, why string }
	var denied []denial
	for _, c := range sorted {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		switch {
		case c.path == w.RoleFile:
		case underPrefix(c.path, w.Prefixes):
		case c.path == planPath && w.Section != "":
			if c.delete {
				denied = append(denied, denial{c.path, "deleted"})
				continue
			}
			base, ok, err := d.BaseFile(ctx, j.Worktree, j.BaseSHA, c.path, maxFileBytes)
			if err != nil {
				denied = append(denied, denial{c.path, "could not read it at base: " + err.Error()})
				continue
			}
			if why := checkPlan(base, ok, c.data, w.Section); why != "" {
				denied = append(denied, denial{c.path, why})
			}
		default:
			denied = append(denied, denial{c.path, fmt.Sprintf("not writable by %s:%s", j.Stage, j.Role)})
		}
	}
	if len(denied) == 0 {
		return "", nil
	}
	all := make([]string, len(denied))
	for i, x := range denied {
		all[i] = clip(x.path, 80)
	}
	d.log().Warn("closeout.write_denied", "job", j.ID, "issue", j.Issue, "stage", j.Stage, "role", j.Role, "paths", all)
	const show = 3
	var parts []string
	for i, x := range denied {
		if i == show {
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", clip(x.path, 80), x.why))
	}
	reason := "write not allowed: " + strings.Join(parts, "; ")
	if len(denied) > show {
		reason += fmt.Sprintf("; and %d more", len(denied)-show)
	}
	return reason, nil
}

func underPrefix(p string, prefixes []string) bool {
	for _, x := range prefixes {
		if strings.HasPrefix(p, x) {
			return true
		}
	}
	return false
}

// checkPlan compares plan.md at base (baseOK false: absent at base) with the
// worktree copy. It returns "" or why the change is not allowed for a role
// that owns section own.
func checkPlan(base []byte, baseOK bool, cur []byte, own string) string {
	c := parsePlan(cur)
	n := 0
	for _, s := range c.sections {
		if s.heading == own {
			n++
		}
	}
	if n > 1 {
		return fmt.Sprintf("section ## %s appears twice", clip(own, 80))
	}
	var b planDoc
	if baseOK {
		b = parsePlan(base)
		if trimEnd(b.pre) != trimEnd(c.pre) {
			return "changed text before the first section"
		}
	}
	bo, co := others(b, own), others(c, own)
	for i := 0; i < len(bo) || i < len(co); i++ {
		if i < len(bo) && i < len(co) && bo[i].heading == co[i].heading && trimEnd(bo[i].text) == trimEnd(co[i].text) {
			continue
		}
		// First difference: say what it is.
		var h string
		switch {
		case i < len(co):
			h = co[i].heading
		default:
			h = bo[i].heading
		}
		switch {
		case hasHeading(co, h) && !hasHeading(bo, h):
			return fmt.Sprintf("adds section ## %s, which this role does not own", clip(h, 80))
		case hasHeading(bo, h) && !hasHeading(co, h):
			return fmt.Sprintf("removes section ## %s", clip(h, 80))
		}
		// The first difference may be a removal that shifted a later entry.
		if i < len(bo) && !hasHeading(co, bo[i].heading) {
			return fmt.Sprintf("removes section ## %s", clip(bo[i].heading, 80))
		}
		return fmt.Sprintf("changes section ## %s", clip(h, 80))
	}
	return ""
}

func hasHeading(ss []planSection, h string) bool {
	for _, s := range ss {
		if s.heading == h {
			return true
		}
	}
	return false
}

func others(d planDoc, own string) []planSection {
	var out []planSection
	for _, s := range d.sections {
		if s.heading != own {
			out = append(out, s)
		}
	}
	return out
}

func trimEnd(s string) string { return strings.TrimRight(s, " \t\r\n") }

type planDoc struct {
	pre      string        // text before the first section heading
	sections []planSection // in file order
}

type planSection struct{ heading, text string } // text includes the heading line

// parsePlan splits plan.md at "## " headings outside fenced code blocks. An
// unclosed fence runs to the end of the file, so the sections it swallows
// count as removed (fails closed).
func parsePlan(b []byte) planDoc {
	var doc planDoc
	var cur *strings.Builder
	var heading string
	flush := func() {
		if cur != nil {
			doc.sections = append(doc.sections, planSection{heading, cur.String()})
		}
	}
	var pre strings.Builder
	fenceCh, fenceN := byte(0), 0
	for _, line := range strings.SplitAfter(string(b), "\n") {
		if line == "" {
			continue
		}
		t := strings.TrimRight(line, " \t\r\n")
		if fenceCh != 0 {
			if isFenceClose(t, fenceCh, fenceN) {
				fenceCh, fenceN = 0, 0
			}
		} else if ch, n := fenceOpen(t); n > 0 {
			fenceCh, fenceN = ch, n
		} else if strings.HasPrefix(t, "## ") {
			flush()
			heading = headingText(t[3:])
			cur = &strings.Builder{}
		}
		if cur == nil {
			pre.WriteString(line)
		} else {
			cur.WriteString(line)
		}
	}
	flush()
	doc.pre = pre.String()
	return doc
}

// headingText drops an optional closing run of "#" and the spaces around it.
func headingText(s string) string {
	s = strings.TrimSpace(s)
	if t := strings.TrimRight(s, "#"); t != s && (t == "" || t[len(t)-1] == ' ' || t[len(t)-1] == '\t') {
		s = strings.TrimSpace(t)
	}
	return s
}

// fenceOpen reports a code fence opening: after at most 3 spaces, three or
// more backticks or tildes.
func fenceOpen(t string) (byte, int) {
	i := 0
	for i < len(t) && i < 4 && t[i] == ' ' {
		i++
	}
	if i > 3 || i >= len(t) || (t[i] != '`' && t[i] != '~') {
		return 0, 0
	}
	ch := t[i]
	n := 0
	for i+n < len(t) && t[i+n] == ch {
		n++
	}
	if n < 3 {
		return 0, 0
	}
	return ch, n
}

// isFenceClose reports a closing fence: the same character at least n times
// and nothing else on the line.
func isFenceClose(t string, ch byte, n int) bool {
	t = strings.TrimLeft(t, " ")
	if len(t) < n {
		return false
	}
	return strings.Trim(t, string(ch)) == ""
}

package config

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Load reads <repoRoot>/.ard/config.yaml from the filesystem and discovers
// the role directories under <repoRoot>/.ard, merging built-in defaults,
// the global config and per-role overrides into one Resolved. A missing
// config file is not an error; every problem found is reported together,
// each naming the file and key.
func Load(repoRoot string) (*Resolved, error) {
	return load(os.DirFS(repoRoot), repoRoot, ".ard/config.yaml")
}

// LoadFS is Load over an arbitrary filesystem, with paths relative to its
// root (so ".ard/config.yaml" must exist at the FS root).
func LoadFS(fsys fs.FS, repoRoot string) (*Resolved, error) {
	return load(fsys, repoRoot, ".ard/config.yaml")
}

func load(fsys fs.FS, repoRoot, cfgPath string) (*Resolved, error) {
	var errs Errors

	raw, parseErrs := parseConfigFile(fsys, cfgPath)
	errs = append(errs, parseErrs...)

	discovered, discoverErrs := discover(fsys)
	errs = append(errs, discoverErrs...)

	res := newResolved(repoRoot)
	errs = append(errs, applyGlobals(res, raw, cfgPath)...)
	errs = append(errs, applyOverrides(res, raw, discovered, cfgPath)...)

	sortRoles(res.Roles)
	if len(errs) > 0 {
		return res, errs
	}
	return res, nil
}

// rawOverride is one overrides entry, every field optional.
type rawOverride struct {
	agent, model, sidecar, tests *string
	planSection                  *string
	timeout, every               *Duration
	maxOpen, groupSize           *int
	maxTestFixes, maxRounds      *int
	autoclose                    *bool
	after, reviewers             *[]string
	when                         *rawWhen
	maxBudgetUSD, budgetPerDay   *float64
}

type rawWhen struct {
	paths  []string
	triage []string
}

// overrideKeys lists the fields valid on every role plus those valid only
// on one kind.
var commonOverrideKeys = map[string]bool{
	"agent": true, "model": true, "timeout": true, "sidecar": true,
	"max_budget_usd": true, "budget_usd_per_day": true,
}

var kindOverrideKeys = map[Kind]map[string]bool{
	KindScanner:  {"every": true, "max_open": true, "group_size": true, "autoclose": true},
	KindTriage:   {"after": true, "plan_section": true},
	KindWorker:   {"tests": true, "max_test_fixes": true, "max_review_rounds": true, "reviewers": true},
	KindReviewer: {"when": true},
}

// applyOverrides merges the global role defaults and each overrides entry
// into the discovered roles, and cross-references role names and sidecar
// keys against what actually exists.
func applyOverrides(res *Resolved, raw rawConfig, discovered []Role, cfgPath string) Errors {
	var errs Errors

	roles := make([]Role, len(discovered))
	copy(roles, discovered)
	byKey := make(map[string]int, len(roles))
	triageNames := map[string]bool{}
	reviewerNames := map[string]bool{}
	for i := range roles {
		applyRoleDefaults(res, &roles[i])
		byKey[kindDir[roles[i].Kind]+"/"+roles[i].Name] = i
		switch roles[i].Kind {
		case KindTriage:
			triageNames[roles[i].Name] = true
		case KindReviewer:
			reviewerNames[roles[i].Name] = true
		}
	}

	for _, ref := range raw.overrides {
		keyRef := overrideKeyRef(ref.key)
		dir, name, ok := splitOverrideKey(ref.key)
		if !ok {
			errs = append(errs, &Error{File: cfgPath, Key: keyRef,
				Msg: fmt.Sprintf("invalid key: must be <scanners|triage|workers|reviewers>/%s and the name must not be all digits", roleNamePattern)})
			continue
		}
		idx, found := byKey[dir+"/"+name]
		if !found {
			errs = append(errs, &Error{File: cfgPath, Key: keyRef, Msg: "no such role directory"})
			continue
		}
		kind := roles[idx].Kind
		ov, parseErrs := parseOverride(ref, kind, cfgPath)
		errs = append(errs, parseErrs...)
		if parseErrs != nil {
			continue
		}
		applyOverride(res, &roles[idx], ov, keyRef, cfgPath, triageNames, reviewerNames, &errs)
	}

	// Each plan.md section has one owner.
	owner := map[string]string{}
	for _, r := range roles {
		if r.Kind != KindTriage || r.PlanSection == "" {
			continue
		}
		if prev, dup := owner[r.PlanSection]; dup {
			errs = append(errs, &Error{File: cfgPath, Key: overrideKeyRef("triage/"+r.Name) + ".plan_section",
				Msg: fmt.Sprintf("section %q is already owned by triage role %q", r.PlanSection, prev)})
			continue
		}
		owner[r.PlanSection] = r.Name
	}

	errs = append(errs, checkAfter(roles, cfgPath)...)

	res.Roles = roles
	return errs
}

// BuiltinPlanSections is the plan.md section (the "## " heading, without the
// marks) each starter triage role owns. A plan_section override replaces it.
var BuiltinPlanSections = map[string]string{
	"product-manager":       "Requirements",
	"architect":             "Design",
	"designer":              "UX",
	"documentation-planner": "Docs",
	"engineer":              "Implementation",
}

// checkPlanSection reports why s cannot be a plan.md section heading, or "".
func checkPlanSection(s string) string {
	switch {
	case strings.TrimSpace(s) == "":
		return "must not be empty"
	case strings.ContainsAny(s, "\r\n"):
		return "must be a single line"
	case s != strings.TrimSpace(s):
		return "must not start or end with spaces"
	case strings.HasPrefix(s, "#"):
		return "must not start with # (ard writes the \"## \" itself)"
	case strings.HasSuffix(s, "#"):
		return "must not end with #"
	}
	return ""
}

// applyRoleDefaults sets the per-role values that come from convention and
// the global config before any override.
func applyRoleDefaults(res *Resolved, role *Role) {
	role.Agent = res.Agent
	role.Timeout = defaultRoleTimeout
	switch role.Kind {
	case KindScanner:
		role.Every = time.Duration(res.Intervals.Scanners)
		role.MaxOpen = defaultScannerMaxOpen
		role.GroupSize = defaultScannerGroupSize
		role.Autoclose = role.ScanPath != ""
	case KindTriage:
		role.PlanSection = BuiltinPlanSections[role.Name]
	case KindWorker:
		role.Tests = defaultWorkerTests
		role.MaxTestFixes = defaultWorkerMaxTestFixes
		role.MaxReviewRounds = defaultWorkerMaxReviewRounds
	}
}

// parseOverride validates and decodes one overrides entry against the keys
// valid for its role kind.
func parseOverride(ref rawOverrideRef, kind Kind, cfgPath string) (rawOverride, Errors) {
	var ov rawOverride
	var errs Errors
	keyRef := overrideKeyRef(ref.key)
	n := ref.value
	if n.Tag == "!!null" || n.Kind != yaml.MappingNode {
		return ov, Errors{{File: cfgPath, Key: keyRef, Msg: "must be a mapping of override keys"}}
	}
	for i := 0; i < len(n.Content); i += 2 {
		field, value := n.Content[i].Value, n.Content[i+1]
		fkey := keyRef + "." + field
		allowed := commonOverrideKeys[field] || kindOverrideKeys[kind][field]
		if !allowed {
			if msg := wrongKindMsg(field, kind); msg != "" {
				errs = append(errs, &Error{File: cfgPath, Key: fkey, Msg: msg})
			} else {
				errs = append(errs, &Error{File: cfgPath, Key: fkey, Msg: "unknown override key"})
			}
			continue
		}
		switch field {
		case "agent":
			errs = append(errs, decodeValue(value, cfgPath, fkey, &ov.agent)...)
		case "model":
			errs = append(errs, decodeValue(value, cfgPath, fkey, &ov.model)...)
		case "sidecar":
			errs = append(errs, decodeValue(value, cfgPath, fkey, &ov.sidecar)...)
		case "tests":
			errs = append(errs, decodeValue(value, cfgPath, fkey, &ov.tests)...)
		case "plan_section":
			before := len(errs)
			errs = append(errs, decodeValue(value, cfgPath, fkey, &ov.planSection)...)
			if len(errs) == before && ov.planSection != nil {
				if msg := checkPlanSection(*ov.planSection); msg != "" {
					errs = append(errs, &Error{File: cfgPath, Key: fkey, Msg: msg})
				}
			}
		case "timeout", "every":
			var d Duration
			before := len(errs)
			errs = append(errs, decodeValue(value, cfgPath, fkey, &d)...)
			if value.Tag != "!!null" && len(errs) == before {
				if field == "timeout" {
					ov.timeout = &d
				} else {
					ov.every = &d
				}
			}
		case "max_open", "group_size", "max_test_fixes", "max_review_rounds":
			var v int
			errs = append(errs, decodeValue(value, cfgPath, fkey, &v)...)
			if value.Tag == "!!null" {
				continue
			}
			if v < 1 {
				errs = append(errs, &Error{File: cfgPath, Key: fkey, Msg: fmt.Sprintf("must be at least 1 (use paused to stop a role), got %d", v)})
				continue
			}
			switch field {
			case "max_open":
				ov.maxOpen = &v
			case "group_size":
				ov.groupSize = &v
			case "max_test_fixes":
				ov.maxTestFixes = &v
			case "max_review_rounds":
				ov.maxRounds = &v
			}
		case "autoclose":
			errs = append(errs, decodeValue(value, cfgPath, fkey, &ov.autoclose)...)
		case "after", "reviewers":
			list, listErrs := decodeStringList(value, cfgPath, fkey)
			errs = append(errs, listErrs...)
			if value.Tag == "!!null" {
				continue
			}
			if field == "after" {
				ov.after = &list
			} else {
				ov.reviewers = &list
			}
		case "when":
			w, whenErrs := parseWhen(value, cfgPath, fkey)
			errs = append(errs, whenErrs...)
			if value.Tag != "!!null" {
				ov.when = &w
			}
		case "max_budget_usd", "budget_usd_per_day":
			var f float64
			errs = append(errs, decodeValue(value, cfgPath, fkey, &f)...)
			if value.Tag == "!!null" {
				continue
			}
			if f <= 0 {
				errs = append(errs, &Error{File: cfgPath, Key: fkey, Msg: fmt.Sprintf("must be greater than 0, got %g", f)})
				continue
			}
			if field == "max_budget_usd" {
				ov.maxBudgetUSD = &f
			} else {
				ov.budgetPerDay = &f
			}
		}
	}
	return ov, errs
}

// wrongKindMsg explains that a key belongs to a different role kind.
func wrongKindMsg(field string, kind Kind) string {
	for k, keys := range kindOverrideKeys {
		if keys[field] && k != kind {
			return fmt.Sprintf("key %q is only valid for %s roles", field, kindDir[k])
		}
	}
	return ""
}

func parseWhen(n *yaml.Node, cfgPath, keyRef string) (rawWhen, Errors) {
	var w rawWhen
	var errs Errors
	if n.Tag == "!!null" {
		return w, nil
	}
	if n.Kind != yaml.MappingNode {
		return w, Errors{{File: cfgPath, Key: keyRef, Msg: "must be a mapping with paths or triage"}}
	}
	for i := 0; i < len(n.Content); i += 2 {
		field, value := n.Content[i].Value, n.Content[i+1]
		switch field {
		case "paths":
			paths, pathErrs := decodeStringList(value, cfgPath, keyRef+".paths")
			errs = append(errs, pathErrs...)
			w.paths = paths
		case "triage":
			triage, triageErrs := decodeStringList(value, cfgPath, keyRef+".triage")
			errs = append(errs, triageErrs...)
			w.triage = triage
		default:
			errs = append(errs, &Error{File: cfgPath, Key: keyRef + "." + field, Msg: "unknown key (want paths or triage)"})
		}
	}
	return w, errs
}

// applyOverride merges one decoded override into a role, field by field
// (override wins; slices replace), and cross-references names against the
// discovered roles and the sidecars map.
func applyOverride(res *Resolved, role *Role, ov rawOverride, keyRef, cfgPath string, triageNames, reviewerNames map[string]bool, errs *Errors) {
	if ov.agent != nil {
		role.Agent = *ov.agent
	}
	if ov.model != nil {
		role.Model = *ov.model
	}
	if ov.sidecar != nil {
		if _, ok := res.Sidecars[*ov.sidecar]; !ok {
			*errs = append(*errs, &Error{File: cfgPath, Key: keyRef + ".sidecar", Msg: fmt.Sprintf("no such sidecar (define it under the top-level sidecars key), got %q", *ov.sidecar)})
		}
		role.Sidecar = *ov.sidecar
	}
	if ov.timeout != nil {
		role.Timeout = time.Duration(*ov.timeout)
	}
	if ov.maxBudgetUSD != nil {
		role.MaxBudgetUSD = *ov.maxBudgetUSD
	}
	if ov.budgetPerDay != nil {
		role.BudgetUSDPerDay = *ov.budgetPerDay
	}
	if ov.every != nil {
		if *ov.every < minLoopInterval {
			*errs = append(*errs, &Error{File: cfgPath, Key: keyRef + ".every", Msg: fmt.Sprintf("must be at least 1m (the Reaper alone runs every minute), got %s", ov.every)})
		}
		role.Every = time.Duration(*ov.every)
	}
	if ov.maxOpen != nil {
		role.MaxOpen = *ov.maxOpen
	}
	if ov.groupSize != nil {
		role.GroupSize = *ov.groupSize
	}
	if ov.autoclose != nil {
		role.Autoclose = *ov.autoclose
	}
	if ov.planSection != nil {
		role.PlanSection = *ov.planSection
	}
	if ov.after != nil {
		for _, dep := range *ov.after {
			if !triageNames[dep] {
				*errs = append(*errs, &Error{File: cfgPath, Key: keyRef + ".after", Msg: fmt.Sprintf("no such triage role %q", dep)})
			}
		}
		role.After = *ov.after
	}
	if ov.tests != nil {
		role.Tests = *ov.tests
	}
	if ov.maxTestFixes != nil {
		role.MaxTestFixes = *ov.maxTestFixes
	}
	if ov.maxRounds != nil {
		role.MaxReviewRounds = *ov.maxRounds
	}
	if ov.reviewers != nil {
		for _, rev := range *ov.reviewers {
			if !reviewerNames[rev] {
				*errs = append(*errs, &Error{File: cfgPath, Key: keyRef + ".reviewers", Msg: fmt.Sprintf("no such reviewer role %q", rev)})
			}
		}
		role.Reviewers = *ov.reviewers
	}
	if ov.when != nil {
		when := &When{Triage: ov.when.triage}
		for i, p := range ov.when.paths {
			norm, err := normaliseRelPath(p)
			if err != nil {
				*errs = append(*errs, &Error{File: cfgPath, Key: fmt.Sprintf("%s.when.paths[%d]", keyRef, i), Msg: err.Error()})
				continue
			}
			when.Paths = append(when.Paths, norm)
		}
		for _, dep := range ov.when.triage {
			if !triageNames[dep] {
				*errs = append(*errs, &Error{File: cfgPath, Key: keyRef + ".when.triage", Msg: fmt.Sprintf("no such triage role %q", dep)})
			}
		}
		role.When = when
	}
}

package config

import (
	"fmt"
	"io/fs"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file holds the YAML-facing structs. Every mapping is walked key by
// key (instead of a plain struct decode) so that an error can name the
// exact file and key that produced it; decoding individual values still
// goes through yaml.Node.Decode, so anchors and scalars behave normally.

// rawConfig is .ard/config.yaml before defaults and merging.
type rawConfig struct {
	maxPods                *int
	executor               *string
	agent                  *string
	requireApproval        *bool
	budgetUSDPerDay        *float64
	maxBudgetUSD           *float64
	globalMaxOpen          *int
	paused                 *bool
	intervals              rawIntervals
	intake                 rawIntake
	protectedPaths         []string // merge.protected_paths
	haveMerge              bool
	stale                  rawStale
	haveStale              bool
	gitleaksEnabled        *bool // redact.gitleaks.enabled
	gitleaksPath           *string
	gitleaksBlockAutoclose *bool // redact.gitleaks.block_autoclose_when_missing
	sidecars               map[string]rawSidecar
	rolesRepo              *rawSidecar
	overrides              []rawOverrideRef
}

// rawIntervals keeps every interval optional so defaults can tell "unset"
// apart from an explicit value.
type rawIntervals struct {
	scanners, triage, workers, reviewers, merger *Duration
}

// rawIntake keeps both keys optional so defaults can tell "unset" apart
// from an explicit value (an explicit empty label is an error).
type rawIntake struct {
	mode, label *string
}

type rawStale struct {
	inactiveFor, suppressFor *Duration
}

type rawSidecar struct {
	repo *string
	ref  *string
}

// rawOverrideRef is one overrides entry kept as its YAML node; entries are
// validated and merged once the role directories are known.
type rawOverrideRef struct {
	key   string // "scanners/cve"
	value *yaml.Node
}

// parseConfigFile reads cfgPath from fsys and parses it. A missing file is
// not an error: it yields a zero rawConfig and every default applies.
func parseConfigFile(fsys fs.FS, cfgPath string) (rawConfig, Errors) {
	var raw rawConfig
	data, err := fs.ReadFile(fsys, cfgPath)
	if err != nil {
		return raw, nil // no config file: defaults only
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return raw, Errors{{File: cfgPath, Msg: fmt.Sprintf("invalid YAML: %v", err)}}
	}
	return parseConfigDoc(&doc, cfgPath)
}

// parseConfigDoc walks the top-level mapping of the config document.
func parseConfigDoc(doc *yaml.Node, cfgPath string) (rawConfig, Errors) {
	var raw rawConfig
	var errs Errors
	if doc.Kind == yaml.DocumentNode {
		doc = doc.Content[0]
	}
	if doc.Tag == "!!null" {
		return raw, nil // empty file
	}
	if doc.Kind != yaml.MappingNode {
		return raw, Errors{{File: cfgPath, Msg: "top level must be a mapping of config keys"}}
	}
	seen := map[string]bool{}
	for i := 0; i < len(doc.Content); i += 2 {
		key, value := doc.Content[i], doc.Content[i+1]
		name := key.Value
		if seen[name] {
			errs = append(errs, &Error{File: cfgPath, Key: name, Msg: "duplicate key"})
			continue
		}
		seen[name] = true
		switch name {
		case "max_pods":
			errs = append(errs, decodeValue(value, cfgPath, name, &raw.maxPods)...)
		case "executor":
			errs = append(errs, decodeValue(value, cfgPath, name, &raw.executor)...)
		case "agent":
			errs = append(errs, decodeValue(value, cfgPath, name, &raw.agent)...)
		case "require_approval":
			errs = append(errs, decodeValue(value, cfgPath, name, &raw.requireApproval)...)
		case "budget_usd_per_day":
			errs = append(errs, decodeValue(value, cfgPath, name, &raw.budgetUSDPerDay)...)
		case "max_budget_usd":
			errs = append(errs, decodeValue(value, cfgPath, name, &raw.maxBudgetUSD)...)
		case "global_max_open":
			errs = append(errs, decodeValue(value, cfgPath, name, &raw.globalMaxOpen)...)
		case "paused":
			errs = append(errs, decodeValue(value, cfgPath, name, &raw.paused)...)
		case "intervals":
			errs = append(errs, raw.intervals.parse(value, cfgPath, "intervals")...)
		case "intake":
			errs = append(errs, raw.parseIntake(value, cfgPath)...)
		case "merge":
			errs = append(errs, raw.parseMerge(value, cfgPath)...)
		case "stale":
			errs = append(errs, raw.parseStale(value, cfgPath)...)
		case "redact":
			errs = append(errs, raw.parseRedact(value, cfgPath)...)
		case "sidecars":
			errs = append(errs, raw.parseSidecars(value, cfgPath)...)
		case "roles_repo":
			errs = append(errs, raw.parseRolesRepo(value, cfgPath)...)
		case "overrides":
			errs = append(errs, raw.parseOverrides(value, cfgPath)...)
		default:
			errs = append(errs, &Error{File: cfgPath, Key: name, Msg: "unknown key"})
		}
	}
	return raw, errs
}

// decodeValue decodes one YAML value into target, naming the key on a type
// error. A null value leaves target untouched.
func decodeValue(value *yaml.Node, file, key string, target any) Errors {
	if value.Tag == "!!null" {
		return nil
	}
	if err := value.Decode(target); err != nil {
		return Errors{{File: file, Key: key, Msg: err.Error()}}
	}
	return nil
}

func (ri *rawIntervals) parse(n *yaml.Node, file, prefix string) Errors {
	var errs Errors
	if n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return Errors{{File: file, Key: prefix, Msg: "must be a mapping of loop intervals"}}
	}
	set := func(name string, dst **Duration) {
		value := mappingValue(n, name)
		if value == nil {
			return
		}
		var d Duration
		before := len(errs)
		errs = append(errs, decodeValue(value, file, prefix+"."+name, &d)...)
		if len(errs) == before {
			*dst = &d
		}
	}
	set("scanners", &ri.scanners)
	set("triage", &ri.triage)
	set("workers", &ri.workers)
	set("reviewers", &ri.reviewers)
	set("merger", &ri.merger)
	for i := 0; i < len(n.Content); i += 2 {
		name := n.Content[i].Value
		switch name {
		case "scanners", "triage", "workers", "reviewers", "merger":
		default:
			errs = append(errs, &Error{File: file, Key: prefix + "." + name, Msg: "unknown key (want one of scanners, triage, workers, reviewers, merger)"})
		}
	}
	return errs
}

func (raw *rawConfig) parseMerge(n *yaml.Node, file string) Errors {
	raw.haveMerge = true
	var errs Errors
	if n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return Errors{{File: file, Key: "merge", Msg: "must be a mapping"}}
	}
	for i := 0; i < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		switch key.Value {
		case "protected_paths":
			paths, pathErrs := decodeStringList(value, file, "merge.protected_paths")
			errs = append(errs, pathErrs...)
			raw.protectedPaths = paths
		default:
			errs = append(errs, &Error{File: file, Key: "merge." + key.Value, Msg: "unknown key (want protected_paths)"})
		}
	}
	return errs
}

// decodeString decodes a scalar into *dst, leaving it nil on a decode error
// so the failure is reported once, not again as a bad value.
func decodeString(value *yaml.Node, file, key string, dst **string) Errors {
	var s *string
	errs := decodeValue(value, file, key, &s)
	if len(errs) == 0 {
		*dst = s
	}
	return errs
}

func (raw *rawConfig) parseIntake(n *yaml.Node, file string) Errors {
	var errs Errors
	if n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return Errors{{File: file, Key: "intake", Msg: "must be a mapping with mode (all or label) and, in label mode, label"}}
	}
	for i := 0; i < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		switch key.Value {
		case "mode":
			errs = append(errs, decodeString(value, file, "intake.mode", &raw.intake.mode)...)
		case "label":
			errs = append(errs, decodeString(value, file, "intake.label", &raw.intake.label)...)
		default:
			errs = append(errs, &Error{File: file, Key: "intake." + key.Value, Msg: "unknown key (want mode or label)"})
		}
	}
	return errs
}

func (raw *rawConfig) parseStale(n *yaml.Node, file string) Errors {
	raw.haveStale = true
	var errs Errors
	if n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return Errors{{File: file, Key: "stale", Msg: "must be a mapping"}}
	}
	for i := 0; i < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		switch key.Value {
		case "inactive_for":
			errs = append(errs, decodeValue(value, file, "stale.inactive_for", &raw.stale.inactiveFor)...)
		case "suppress_for":
			errs = append(errs, decodeValue(value, file, "stale.suppress_for", &raw.stale.suppressFor)...)
		default:
			errs = append(errs, &Error{File: file, Key: "stale." + key.Value, Msg: "unknown key (want inactive_for or suppress_for)"})
		}
	}
	return errs
}

func (raw *rawConfig) parseRedact(n *yaml.Node, file string) Errors {
	var errs Errors
	if n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return Errors{{File: file, Key: "redact", Msg: "must be a mapping"}}
	}
	for i := 0; i < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		if key.Value != "gitleaks" {
			errs = append(errs, &Error{File: file, Key: "redact." + key.Value, Msg: "unknown key (want gitleaks)"})
			continue
		}
		if value.Tag == "!!null" {
			continue
		}
		if value.Kind != yaml.MappingNode {
			errs = append(errs, &Error{File: file, Key: "redact.gitleaks", Msg: "must be a mapping"})
			continue
		}
		for j := 0; j < len(value.Content); j += 2 {
			k, v := value.Content[j], value.Content[j+1]
			switch k.Value {
			case "enabled":
				errs = append(errs, decodeValue(v, file, "redact.gitleaks.enabled", &raw.gitleaksEnabled)...)
			case "path":
				errs = append(errs, decodeValue(v, file, "redact.gitleaks.path", &raw.gitleaksPath)...)
			case "block_autoclose_when_missing":
				errs = append(errs, decodeValue(v, file, "redact.gitleaks.block_autoclose_when_missing", &raw.gitleaksBlockAutoclose)...)
			default:
				errs = append(errs, &Error{File: file, Key: "redact.gitleaks." + k.Value, Msg: "unknown key (want enabled, path or block_autoclose_when_missing)"})
			}
		}
	}
	return errs
}

func (raw *rawConfig) parseSidecars(n *yaml.Node, file string) Errors {
	var errs Errors
	if n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return Errors{{File: file, Key: "sidecars", Msg: "must be a mapping of sidecar name to repo settings"}}
	}
	raw.sidecars = map[string]rawSidecar{}
	for i := 0; i < len(n.Content); i += 2 {
		name, value := n.Content[i].Value, n.Content[i+1]
		key := "sidecars." + name
		if value.Kind != yaml.MappingNode {
			errs = append(errs, &Error{File: file, Key: key, Msg: "must be a mapping with repo and ref"})
			continue
		}
		var sc rawSidecar
		for j := 0; j < len(value.Content); j += 2 {
			fkey, fval := value.Content[j], value.Content[j+1]
			switch fkey.Value {
			case "repo":
				errs = append(errs, decodeValue(fval, file, key+".repo", &sc.repo)...)
			case "ref":
				errs = append(errs, decodeValue(fval, file, key+".ref", &sc.ref)...)
			default:
				errs = append(errs, &Error{File: file, Key: key + "." + fkey.Value, Msg: "unknown key (want repo or ref)"})
			}
		}
		raw.sidecars[name] = sc
	}
	return errs
}

func (raw *rawConfig) parseRolesRepo(n *yaml.Node, file string) Errors {
	var errs Errors
	if n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return Errors{{File: file, Key: "roles_repo", Msg: "must be a mapping with repo and ref"}}
	}
	var rr rawSidecar
	for j := 0; j < len(n.Content); j += 2 {
		fkey, fval := n.Content[j], n.Content[j+1]
		switch fkey.Value {
		case "repo":
			errs = append(errs, decodeValue(fval, file, "roles_repo.repo", &rr.repo)...)
		case "ref":
			errs = append(errs, decodeValue(fval, file, "roles_repo.ref", &rr.ref)...)
		default:
			errs = append(errs, &Error{File: file, Key: "roles_repo." + fkey.Value, Msg: "unknown key (want repo or ref)"})
		}
	}
	raw.rolesRepo = &rr
	return errs
}

func (raw *rawConfig) parseOverrides(n *yaml.Node, file string) Errors {
	var errs Errors
	if n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return Errors{{File: file, Key: "overrides", Msg: "must be a mapping of role keys to settings"}}
	}
	seen := map[string]bool{}
	for i := 0; i < len(n.Content); i += 2 {
		key, value := n.Content[i].Value, n.Content[i+1]
		if seen[key] {
			errs = append(errs, &Error{File: file, Key: overrideKeyRef(key), Msg: "duplicate entry"})
			continue
		}
		seen[key] = true
		raw.overrides = append(raw.overrides, rawOverrideRef{key: key, value: value})
	}
	return errs
}

// mappingValue returns the value node for key in a mapping node, or nil.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// decodeStringList decodes a sequence of strings, naming the failing index.
func decodeStringList(n *yaml.Node, file, key string) ([]string, Errors) {
	if n.Tag == "!!null" {
		return nil, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, Errors{{File: file, Key: key, Msg: "must be a list of strings"}}
	}
	out := make([]string, 0, len(n.Content))
	var errs Errors
	for i, elem := range n.Content {
		var s string
		if err := elem.Decode(&s); err != nil {
			errs = append(errs, &Error{File: file, Key: fmt.Sprintf("%s[%d]", key, i), Msg: err.Error()})
			continue
		}
		out = append(out, s)
	}
	return out, errs
}

// overrideKeyRef renders an overrides key the way errors reference it.
func overrideKeyRef(key string) string { return fmt.Sprintf("overrides[%q]", key) }

// roleNamePattern is the role directory name pattern, shared by discovery
// and the overrides key check.
const roleNamePattern = "[a-z0-9][a-z0-9-]*"

func validRoleName(name string) bool {
	if name == "" || !isLowerAlphaNum(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if c != '-' && !isLowerAlphaNum(c) {
			return false
		}
	}
	return true
}

func isLowerAlphaNum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// splitOverrideKey splits "scanners/cve" into its kind directory and role
// name. The second return value reports whether the key matched the
// required shape.
func splitOverrideKey(key string) (dir, name string, ok bool) {
	dir, name, found := strings.Cut(key, "/")
	if !found || strings.Contains(name, "/") {
		return "", "", false
	}
	switch dir {
	case "scanners", "triage", "workers", "reviewers":
	default:
		return "", "", false
	}
	if !validRoleName(name) || isAllDigits(name) {
		return "", "", false
	}
	return dir, name, true
}

package config

import (
	"reflect"
	"strings"
	"testing"
)

// yamlPaths collects the dotted yaml-tag paths of leaf fields of t.
func yamlPaths(t reflect.Type, prefix string, out map[string]bool) {
	for i := 0; i < t.NumField(); i++ {
		tag := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		path := prefix + tag
		ft := t.Field(i).Type
		switch {
		case ft.Kind() == reflect.Struct && ft.PkgPath() == t.PkgPath():
			yamlPaths(ft, path+".", out)
		case ft.Kind() == reflect.Map:
			yamlPaths(ft.Elem(), path+".<name>.", out)
		default:
			out[path] = true
		}
	}
}

func TestKeysCoverRenderedConfig(t *testing.T) {
	have := map[string]bool{}
	for _, k := range Keys() {
		have[k] = true
	}
	paths := map[string]bool{}
	yamlPaths(reflect.TypeOf(resolvedYAML{}), "", paths)
	skip := map[string]bool{"repo": true, "sha": true, "roles": true}
	for p := range paths {
		if skip[p] {
			continue
		}
		if p == "protected_paths" {
			p = "merge.protected_paths"
		}
		if !have[p] {
			t.Errorf("config key %q is rendered but missing from Keys()", p)
		}
	}
	roles := map[string]bool{}
	yamlPaths(reflect.TypeOf(roleYAML{}), "", roles)
	for _, k := range []string{"agent", "model", "timeout", "sidecar", "every", "max_open", "group_size", "autoclose", "route", "after", "plan_section", "tests", "max_test_fixes", "max_review_rounds", "reviewers", "diff_warn_lines", "diff_max_lines"} {
		if !roles[k] {
			t.Errorf("override key %q is no longer a roleYAML field", k)
		}
		if !have[k] && k != "agent" {
			t.Errorf("override key %q missing from Keys()", k)
		}
	}
}

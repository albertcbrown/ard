// Package archtest enforces, at build time, that only internal/effects can
// write to GitHub or git. It has only test files so it adds nothing to the
// shipped binary.
package archtest

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// hit is one rule violation.
type hit struct {
	Rule string
	File string // relative to the loaded module root
	Line int
	Msg  string
}

func (h hit) String() string { return fmt.Sprintf("%s %s:%d: %s", h.Rule, h.File, h.Line, h.Msg) }

// layout names the packages the rules talk about, relative to the module.
type layout struct {
	goGithub string // import prefix of go-github, absolute
	github   string // module-relative paths below
	fake     string
	gitx     string
	effects  string
}

func realLayout() layout {
	return layout{
		goGithub: "github.com/google/go-github",
		github:   "internal/github", fake: "internal/github/fake",
		gitx: "internal/gitx", effects: "internal/effects",
	}
}

func underOrEq(path, base string) bool { return path == base || strings.HasPrefix(path, base+"/") }

// check loads ./... under dir and applies the six rules.
func check(dir string, l layout) ([]hit, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	cfg := &packages.Config{
		Dir:   dir,
		Tests: false,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes |
			packages.NeedTypesInfo | packages.NeedImports | packages.NeedModule,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("load packages: %w", err)
	}
	var hits []hit
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			return nil, fmt.Errorf("package %s: %v", p.PkgPath, p.Errors[0])
		}
		if p.Module == nil {
			return nil, fmt.Errorf("package %s has no module", p.PkgPath)
		}
		mod := p.Module.Path
		rel := strings.TrimPrefix(strings.TrimPrefix(p.PkgPath, mod), "/")
		add := func(rule string, pos token.Pos, msg string) {
			ps := p.Fset.Position(pos)
			f, err := filepath.Rel(dir, ps.Filename)
			if err != nil {
				f = ps.Filename
			}
			hits = append(hits, hit{rule, filepath.ToSlash(f), ps.Line, msg})
		}
		inGitHub := underOrEq(rel, l.github)
		inGitx := underOrEq(rel, l.gitx)
		goGH := l.goGithub
		ghPath, fakePath, gitxPath := mod+"/"+l.github, mod+"/"+l.fake, mod+"/"+l.gitx
		gittestPath := gitxPath + "/gittest"
		var gitPtr *types.Pointer
		for _, q := range pkgs {
			if q.PkgPath == gitxPath && q.Types != nil {
				if tn, ok := q.Types.Scope().Lookup("Git").(*types.TypeName); ok {
					gitPtr = types.NewPointer(tn.Type())
				}
			}
		}

		for _, f := range p.Syntax {
			for _, imp := range f.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				if underOrEq(path, goGH) && !inGitHub {
					add("R1", imp.Pos(), "imports "+path+"; only internal/github may")
				}
				if path == gittestPath {
					add("R7", imp.Pos(), "non-test import of gitx/gittest, whose Run can push")
				}
				if path == fakePath && !inGitHub {
					add("R3", imp.Pos(), "non-test import of the GitHub fake")
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.InterfaceType:
					// A consumer-side interface that *gitx.Git satisfies would let
					// Commit/Push be called without naming gitx.
					if !inGitx && rel != l.effects && gitPtr != nil && hasWriteMethod(p.TypesInfo.Types[n].Type) {
						if iface, ok := p.TypesInfo.Types[n].Type.Underlying().(*types.Interface); ok && types.Implements(gitPtr, iface) {
							add("R4", n.Pos(), "interface satisfied by (*gitx.Git) declares Commit or Push")
						}
					}
				case *ast.BasicLit:
					if n.Kind == token.STRING {
						if s, err := strconv.Unquote(n.Value); err == nil && strings.Contains(s, "api.github.com") && !inGitHub {
							add("R6", n.Pos(), "string constant mentions api.github.com")
						}
					}
				case *ast.CallExpr:
					if !inGitx && isGitExec(p.TypesInfo, n) {
						add("R5", n.Pos(), "exec of git outside internal/gitx")
					}
				}
				return true
			})
		}
		for id, obj := range p.TypesInfo.Uses {
			if obj.Pkg() == nil {
				continue
			}
			switch {
			case obj.Pkg().Path() == ghPath && (obj.Name() == "New" || obj.Name() == "Client" || obj.Name() == "Writer" || obj.Name() == "HTTP"):
				if !inGitHub && rel != l.effects {
					add("R2", id.Pos(), "use of github."+obj.Name()+"; only internal/effects may")
				}
			case obj.Pkg().Path() == gitxPath && (obj.Name() == "Commit" || obj.Name() == "Push") && isGitMethod(obj):
				if !inGitx && rel != l.effects {
					add("R4", id.Pos(), "use of (*gitx.Git)."+obj.Name()+"; only internal/effects may")
				}
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].File != hits[j].File {
			return hits[i].File < hits[j].File
		}
		return hits[i].Line < hits[j].Line
	})
	return hits, nil
}

func hasWriteMethod(t types.Type) bool {
	iface, ok := t.Underlying().(*types.Interface)
	if !ok {
		return false
	}
	for i := 0; i < iface.NumMethods(); i++ {
		if n := iface.Method(i).Name(); n == "Commit" || n == "Push" {
			return true
		}
	}
	return false
}

func isGitMethod(obj types.Object) bool {
	fn, ok := obj.(*types.Func)
	if !ok {
		return false
	}
	recv := fn.Type().(*types.Signature).Recv()
	if recv == nil {
		return false
	}
	t := recv.Type()
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	return ok && named.Obj().Name() == "Git"
}

// isGitExec reports a call to os/exec.Command or CommandContext whose command
// argument is the constant "git".
func isGitExec(info *types.Info, call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	obj := info.Uses[sel.Sel]
	if obj == nil || obj.Pkg() == nil || obj.Pkg().Path() != "os/exec" {
		return false
	}
	idx := 0
	switch obj.Name() {
	case "Command":
	case "CommandContext":
		idx = 1
	default:
		return false
	}
	if len(call.Args) <= idx {
		return false
	}
	tv, ok := info.Types[call.Args[idx]]
	return ok && tv.Value != nil && tv.Value.Kind() == constant.String && constant.StringVal(tv.Value) == "git"
}

// TestOnlyEffectsWrites fails the build if anything but internal/effects can
// write to GitHub or git.
func TestOnlyEffectsWrites(t *testing.T) {
	hits, err := check(filepath.Join("..", "..", ".."), realLayout())
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		t.Error(h)
	}
}

// TestRulesSelf proves each rule fires, and only where it should, against a
// fixture module with one violation per rule.
func TestRulesSelf(t *testing.T) {
	l := layout{goGithub: "archfix/gogithub", github: "internal/github", fake: "internal/github/fake",
		gitx: "internal/gitx", effects: "internal/effects"}
	hits, err := check("testdata", l)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, h := range hits {
		got[h.Rule+" "+h.File]++
	}
	want := map[string]int{
		"R1 internal/bad/r1.go": 1, "R2 internal/bad/r2.go": 1, "R3 internal/bad/r3.go": 1,
		"R4 internal/bad/r4.go": 1, "R5 internal/bad/r5.go": 1, "R6 internal/bad/r6.go": 1,
		"R4 internal/bad/r4iface.go": 1, "R7 internal/bad/r7.go": 1, "R2 internal/bad/r2http.go": 1,
	}
	// R1 file also uses nothing else; r2/r4 import allowed packages only.
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("hits = %v\nwant %v\nall: %v", got, want, hits)
	}
}

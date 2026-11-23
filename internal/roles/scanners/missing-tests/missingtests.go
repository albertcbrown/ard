//go:build ignore

// missingtests lists Go functions that the module's tests do not run, or that
// no test calls by name, as ard scanner findings (JSON lines on stdout).
//
// It is built by the missing-tests scan driver (added in a later change). It
// carries the ignore build tag, so go vet ./... and go build ./... skip it.
// It reads `go list -json` output and a coverage profile that the driver
// produced; it never runs repository code, and uses only the standard
// library. It fails closed: on any error it prints "missing-tests: <what>" to
// stderr, nothing on stdout, and exits 1.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	maxFindings    = 10000
	maxFingerprint = 512
	maxTitleRunes  = 200
	maxGroupBytes  = 256
	maxBodyRunes   = 1900
)

// implicitMethods are called implicitly by the standard library, so a test
// need not name them: a covered one is never reported.
var implicitMethods = map[string]bool{
	"Error": true, "Unwrap": true, "Is": true, "As": true, "String": true,
	"GoString": true, "Format": true, "MarshalJSON": true, "UnmarshalJSON": true,
	"MarshalText": true, "UnmarshalText": true, "MarshalYAML": true,
	"UnmarshalYAML": true, "MarshalBinary": true, "UnmarshalBinary": true,
	"LogValue": true, "ServeHTTP": true,
}

// listedPackage is the subset of `go list -json` that the helper reads.
type listedPackage struct {
	ImportPath, Name, Dir              string
	GoFiles, TestGoFiles, XTestGoFiles []string
}

// block is one coverage block of the profile.
type block struct {
	StartLine, StartCol, EndLine, EndCol, NumStmt int
	Covered                                       bool
}

type funcInfo struct {
	Pkg        *listedPackage
	File       string // repo-relative slash path
	Line       int
	Symbol     string // "F" or "T.M"
	Method     string // "M" for methods, "" for functions
	Exported   bool
	Start, End token.Position
	HasStmts   bool
}

// finding matches scanner.Finding's JSON shape; field order is the schema order.
type finding struct {
	Fingerprint string   `json:"fingerprint"`
	Title       string   `json:"title"`
	Severity    string   `json:"severity"`
	Group       string   `json:"group"`
	Files       []string `json:"files"`
	Body        string   `json:"body"`
}

type testRefs struct {
	internal  map[string]map[string]bool // import path -> idents in its TestGoFiles
	qualified map[string]map[string]bool // import path -> names used as X.Name, X being that package's import name
	selectors map[string]bool            // every Sel.Name in any test file
}

type ignoreRule struct {
	raw     string
	pkg     string
	tree    bool
	symGlob string
}

type config struct {
	module, prefix, goVersion string
	noCoverage                bool
	max                       int
}

func main() {
	out, err := run(os.Args[1:], os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "missing-tests: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintf(os.Stderr, "missing-tests: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stderr io.Writer) ([]byte, error) {
	fs := flag.NewFlagSet("missingtests", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var cfg config
	var packages, profile, ignore string
	fs.StringVar(&cfg.module, "module", "", "module path")
	fs.StringVar(&cfg.prefix, "prefix", "", "repo-relative module directory, . for the root")
	fs.StringVar(&packages, "packages", "", "file with `go list -json ./...` output")
	fs.StringVar(&cfg.goVersion, "go", "", "Go version used for the measurement")
	fs.StringVar(&profile, "profile", "", "coverage profile")
	fs.BoolVar(&cfg.noCoverage, "no-coverage", false, "static rule only")
	fs.StringVar(&ignore, "ignore", "", "optional ignore file")
	fs.IntVar(&cfg.max, "max", maxFindings, "finding limit (for tests)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	for name, v := range map[string]string{"-module": cfg.module, "-prefix": cfg.prefix, "-packages": packages, "-go": cfg.goVersion} {
		if v == "" {
			return nil, fmt.Errorf("%s is required", name)
		}
	}
	if (profile == "") == !cfg.noCoverage {
		return nil, errors.New("exactly one of -profile and -no-coverage is required")
	}
	pkgs, err := readPackages(packages)
	if err != nil {
		return nil, err
	}
	var blocks map[string][]block
	if profile != "" {
		if blocks, err = readProfile(profile); err != nil {
			return nil, err
		}
	}
	var rules []ignoreRule
	if ignore != "" {
		if rules, err = readIgnore(ignore); err != nil {
			return nil, err
		}
	}
	fset := token.NewFileSet()
	funcs, err := collectFuncs(pkgs, cfg.prefix, cfg.module, fset)
	if err != nil {
		return nil, err
	}
	refs, err := collectTestRefs(pkgs, fset)
	if err != nil {
		return nil, err
	}
	used := make([]bool, len(rules))
	var found []finding
	for _, f := range funcs {
		ignored := false
		for i, r := range rules {
			if r.match(f.Pkg.ImportPath, f.Symbol) {
				used[i], ignored = true, true
			}
		}
		if ignored {
			continue
		}
		covered, run, total := false, 0, 0
		if !cfg.noCoverage {
			covered, run, total = coverage(f, blocks[f.Pkg.ImportPath+"/"+path.Base(f.File)])
		}
		named := refs.named(f)
		sev, kind, ok := decide(f, covered, named, cfg.noCoverage)
		if !ok {
			continue
		}
		found = append(found, makeFinding(f, cfg, sev, kind, named, run, total))
	}
	for i, r := range rules {
		if !used[i] {
			fmt.Fprintf(stderr, "missing-tests: warning: ignore pattern %q matches nothing\n", r.raw)
		}
	}
	if len(found) > cfg.max {
		return nil, fmt.Errorf("too many findings (%d, limit %d): add an ignore file or no-coverage", len(found), cfg.max)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Fingerprint < found[j].Fingerprint })
	var out []byte
	for _, f := range found {
		b, err := json.Marshal(f)
		if err != nil {
			return nil, err
		}
		out = append(append(out, b...), '\n')
	}
	return out, nil
}

// readPackages decodes the concatenated JSON objects of `go list -json`.
func readPackages(file string) ([]*listedPackage, error) {
	fh, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	dec := json.NewDecoder(bufio.NewReader(fh))
	var pkgs []*listedPackage
	for {
		var p listedPackage
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("%s: %v", file, err)
		}
		if p.ImportPath == "" || p.Dir == "" {
			return nil, fmt.Errorf("%s: package without import path or directory", file)
		}
		pkgs = append(pkgs, &p)
	}
	return pkgs, nil
}

var profileLine = regexp.MustCompile(`^(.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$`)

// readProfile reads a coverage profile. Blocks are keyed by "importpath/file.go".
// The same block appears once per test binary under -coverpkg; they merge with OR.
func readProfile(file string) (map[string][]block, error) {
	fh, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(nil, 1<<20)
	type key struct {
		file string
		b    block
	}
	merged := map[key]bool{}
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if n == 1 {
			if line != "mode: set" && line != "mode: count" && line != "mode: atomic" {
				return nil, fmt.Errorf("%s:1: malformed mode line %q", file, line)
			}
			continue
		}
		m := profileLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("%s:%d: malformed profile line %q", file, n, line)
		}
		var v [6]int
		for i := range v {
			v[i], _ = strconv.Atoi(m[i+2])
		}
		k := key{m[1], block{StartLine: v[0], StartCol: v[1], EndLine: v[2], EndCol: v[3], NumStmt: v[4]}}
		merged[k] = merged[k] || v[5] > 0
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("%s: empty profile", file)
	}
	out := map[string][]block{}
	for k, cov := range merged {
		k.b.Covered = cov
		out[k.file] = append(out[k.file], k.b)
	}
	return out, nil
}

// relPkg is the package directory relative to the repository root, or the
// module path when the package is at the repository root.
func relPkg(p *listedPackage, module, prefix string) string {
	rel := strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, module), "/")
	dir := path.Join(prefix, rel)
	if dir == "." || dir == "" {
		return module
	}
	return dir
}

func collectFuncs(pkgs []*listedPackage, prefix, module string, fset *token.FileSet) ([]funcInfo, error) {
	var out []funcInfo
	for _, p := range pkgs {
		dir := path.Join(prefix, strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, module), "/"))
		for _, name := range p.GoFiles {
			file, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.ParseComments|parser.SkipObjectResolution)
			if err != nil {
				return nil, err
			}
			if ast.IsGenerated(file) {
				continue
			}
			for _, d := range file.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil || len(fd.Body.List) == 0 {
					continue
				}
				fn := fd.Name.Name
				if fn == "_" || (fd.Recv == nil && (fn == "main" || fn == "init")) {
					continue
				}
				fi := funcInfo{Pkg: p, File: path.Join(dir, name), Symbol: fn, Exported: ast.IsExported(fn), HasStmts: true,
					Start: fset.Position(fd.Pos()), End: fset.Position(fd.End())}
				fi.Line = fi.Start.Line
				if fd.Recv != nil && len(fd.Recv.List) > 0 {
					recv := baseType(fd.Recv.List[0].Type)
					fi.Method = fn
					fi.Symbol = recv + "." + fn
					fi.Exported = fi.Exported && ast.IsExported(recv)
				}
				out = append(out, fi)
			}
		}
	}
	return out, nil
}

// baseType is a receiver's type name with * and type parameters stripped.
func baseType(e ast.Expr) string {
	for {
		switch t := e.(type) {
		case *ast.StarExpr:
			e = t.X
		case *ast.ParenExpr:
			e = t.X
		case *ast.IndexExpr:
			e = t.X
		case *ast.IndexListExpr:
			e = t.X
		case *ast.Ident:
			return t.Name
		default:
			return "?"
		}
	}
}

func collectTestRefs(pkgs []*listedPackage, fset *token.FileSet) (testRefs, error) {
	r := testRefs{internal: map[string]map[string]bool{}, qualified: map[string]map[string]bool{}, selectors: map[string]bool{}}
	names := map[string]string{} // import path -> package name
	for _, p := range pkgs {
		names[p.ImportPath] = p.Name
	}
	for _, p := range pkgs {
		for _, set := range []struct {
			files    []string
			internal bool
		}{{p.TestGoFiles, true}, {p.XTestGoFiles, false}} {
			for _, name := range set.files {
				file, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.SkipObjectResolution)
				if err != nil {
					return r, err
				}
				imports := map[string]string{} // local name -> import path
				for _, im := range file.Imports {
					ip, err := strconv.Unquote(im.Path.Value)
					if err != nil {
						continue
					}
					local := names[ip]
					if im.Name != nil {
						local = im.Name.Name
					}
					if _, inModule := names[ip]; inModule && local != "_" && local != "." {
						imports[local] = ip
					}
				}
				ast.Inspect(file, func(n ast.Node) bool {
					switch x := n.(type) {
					case *ast.Ident:
						if set.internal {
							add(r.internal, p.ImportPath, x.Name)
						}
					case *ast.SelectorExpr:
						r.selectors[x.Sel.Name] = true
						if id, ok := x.X.(*ast.Ident); ok {
							if ip, ok := imports[id.Name]; ok {
								add(r.qualified, ip, x.Sel.Name)
							}
						}
					}
					return true
				})
			}
		}
	}
	return r, nil
}

func add(m map[string]map[string]bool, k, v string) {
	if m[k] == nil {
		m[k] = map[string]bool{}
	}
	m[k][v] = true
}

// named reports whether a test names f. For a method the receiver is not
// checked, so this can only miss findings, never invent one.
func (r testRefs) named(f funcInfo) bool {
	ip := f.Pkg.ImportPath
	if f.Method != "" {
		return r.selectors[f.Method] || r.internal[ip][f.Method]
	}
	return r.internal[ip][f.Symbol] || r.qualified[ip][f.Symbol]
}

// coverage reports whether any block inside f ran, and the statements run and total.
func coverage(f funcInfo, blocks []block) (covered bool, run, total int) {
	for _, b := range blocks {
		if before(b.StartLine, b.StartCol, f.Start.Line, f.Start.Column) || before(f.End.Line, f.End.Column, b.EndLine, b.EndCol) {
			continue
		}
		total += b.NumStmt
		if b.Covered {
			covered = true
			run += b.NumStmt
		}
	}
	return
}

func before(l1, c1, l2, c2 int) bool { return l1 < l2 || (l1 == l2 && c1 < c2) }

// readIgnore reads the optional ignore file. A malformed line is an error.
func readIgnore(file string) ([]ignoreRule, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var rules []ignoreRule
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		bad := func(why string) error { return fmt.Errorf("%s:%d: %s: %q", file, i+1, why, line) }
		if strings.ContainsAny(line, " \t") {
			return nil, bad("pattern contains whitespace")
		}
		r := ignoreRule{raw: line, pkg: line}
		if pkg, glob, ok := strings.Cut(line, ":"); ok {
			if pkg == "" || glob == "" {
				return nil, bad("empty package or symbol")
			}
			if _, err := path.Match(glob, ""); err != nil {
				return nil, bad("bad glob")
			}
			r.pkg, r.symGlob = pkg, glob
		} else if strings.HasSuffix(line, "/...") {
			r.pkg, r.tree = strings.TrimSuffix(line, "/..."), true
		}
		if r.pkg == "" || strings.Contains(r.pkg, "...") {
			return nil, bad("bad package pattern")
		}
		rules = append(rules, r)
	}
	return rules, nil
}

func (r ignoreRule) match(pkg, symbol string) bool {
	if pkg != r.pkg && !(r.tree && strings.HasPrefix(pkg, r.pkg+"/")) {
		return false
	}
	if r.symGlob == "" {
		return true
	}
	ok, _ := path.Match(r.symGlob, symbol)
	return ok
}

// decide applies the severity table. kind is "never" (never run by tests) or
// "uncalled" (no test calls it).
func decide(f funcInfo, covered, named, noCoverage bool) (sev, kind string, ok bool) {
	implicit := f.Method != "" && implicitMethods[f.Method]
	if noCoverage {
		if f.Exported && !implicit && !named {
			return "low", "uncalled", true
		}
		return "", "", false
	}
	switch {
	case f.Exported && !covered:
		return "medium", "never", true
	case f.Exported && !named && !implicit:
		return "low", "uncalled", true
	case !f.Exported && !covered:
		return "low", "never", true
	}
	return "", "", false
}

func makeFinding(f funcInfo, cfg config, sev, kind string, named bool, run, total int) finding {
	ip := f.Pkg.ImportPath
	fp := "missing-tests:go:" + ip + ":" + f.Symbol
	if len(fp) > maxFingerprint {
		sum := sha256.Sum256([]byte(ip + ":" + f.Symbol))
		fp = "missing-tests:go:sha256:" + hex.EncodeToString(sum[:])
	}
	group := "go:" + ip
	if len(group) > maxGroupBytes {
		sum := sha256.Sum256([]byte(ip))
		group = "go:sha256:" + hex.EncodeToString(sum[:])
	}
	what := "is never run by tests"
	if kind == "uncalled" {
		what = "has no test that calls it"
	}
	title := cut(relPkg(f.Pkg, cfg.module, cfg.prefix)+": "+f.Symbol+" "+what, maxTitleRunes)
	exp, fixed := "unexported", "the module's tests execute it"
	if f.Exported {
		exp, fixed = "exported", "a test in this module calls it by name, and the module's tests execute it"
	}
	stmts := fmt.Sprintf("%d of %d", run, total)
	if cfg.noCoverage {
		stmts = "coverage not measured, `no-coverage` is set"
		if f.Exported {
			fixed = "a test in this module calls it by name"
		}
	}
	yn := "no"
	if named {
		yn = "yes"
	}
	body := fmt.Sprintf("`%s` in `%s:%d` (package `%s`, %s).\n\n- Statements run by the module's tests: %s\n- Named by a test: %s\n\nFixed when %s. Measured with `go test -coverpkg=./... ./...` in module `%s` using %s.",
		f.Symbol, f.File, f.Line, ip, exp, stmts, yn, fixed, cfg.prefix, cfg.goVersion)
	return finding{Fingerprint: fp, Title: title, Severity: sev, Group: group, Files: []string{f.File}, Body: cut(body, maxBodyRunes)}
}

// cut shortens s to at most n runes, ending in an ellipsis when it cuts.
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

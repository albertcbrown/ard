//go:build ignore

// docscan lists undocumented exported Go identifiers and missing package
// comments under -root, as ard scanner findings (JSON lines on stdout).
//
// It only parses files: it never runs repository code, needs no network and
// uses only the standard library. It fails closed: on any error it prints
// nothing on stdout and exits 1, so a partial result can never autoclose
// issues. Stdout is written once, at the very end.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxFindings    = 10000
	maxFingerprint = 512
	maxTitleRunes  = 200
	maxGroupBytes  = 256
	hint           = "Add a comment directly above the declaration that starts with its name (https://go.dev/doc/comment); for a const/var block, a comment on the block documents every name in it."
)

// finding matches scanner.Finding's JSON shape.
type finding struct {
	Fingerprint string   `json:"fingerprint"`
	Title       string   `json:"title"`
	Severity    string   `json:"severity"`
	Group       string   `json:"group,omitempty"`
	Files       []string `json:"files,omitempty"`
	Body        string   `json:"body,omitempty"`
}

// decl is one exported declaration. kind is "function", "method", "type",
// "const", "var", "const block" or "var block".
type decl struct {
	dir, symbol, kind, file string
	line                    int
	documented              bool
}

type pkg struct {
	name     string
	files    []string
	firstPos string // "file:line" of the package clause of the first file
	hasDoc   bool
	decls    []decl
}

func main() {
	root := flag.String("root", ".", "directory to scan")
	exclude := flag.String("exclude", "", "optional file of excluded package dirs")
	flag.Parse()
	if err := run(*root, *exclude); err != nil {
		fmt.Fprintln(os.Stderr, "docscan:", err)
		os.Exit(1)
	}
}

func run(root, excludePath string) error {
	var pats []string
	if excludePath != "" {
		var err error
		if pats, err = loadExcludes(excludePath); err != nil {
			return err
		}
	}
	pkgs, err := walk(root, pats)
	if err != nil {
		return err
	}
	found, err := build(pkgs)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	for _, f := range found {
		b, err := json.Marshal(f)
		if err != nil {
			return err
		}
		out.Write(b)
		out.WriteByte('\n')
	}
	_, err = os.Stdout.Write(out.Bytes())
	return err
}

// loadExcludes reads one pattern per line; "#" comments and blanks are skipped.
func loadExcludes(p string) ([]string, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var pats []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if !strings.HasSuffix(l, "/") {
			if _, err := path.Match(l, ""); err != nil {
				return nil, fmt.Errorf("exclude: bad pattern %q: %v", l, err)
			}
		}
		pats = append(pats, l)
	}
	return pats, sc.Err()
}

// excluded reports whether package dir matches an exclude pattern: "x/"
// covers x and everything below it; anything else is a path.Match pattern.
func excluded(dir string, pats []string) bool {
	for _, p := range pats {
		if strings.HasSuffix(p, "/") {
			x := strings.TrimSuffix(p, "/")
			if dir == x || strings.HasPrefix(dir, x+"/") {
				return true
			}
			continue
		}
		if ok, _ := path.Match(p, dir); ok {
			return true
		}
	}
	return false
}

func validText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func skipDir(name string) bool {
	return name == "testdata" || name == "vendor" || name == "node_modules" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func walk(root string, pats []string) (map[string]*pkg, error) {
	pkgs := map[string]*pkg{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if p != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") || !d.Type().IsRegular() {
			return nil
		}
		r, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(r)
		if !validText(rel) {
			return fmt.Errorf("path %q is not valid UTF-8 or has a control character", rel)
		}
		dir := path.Dir(rel)
		if excluded(dir, pats) {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, rel, src, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %v", rel, err)
		}
		if f.Name.Name == "main" || ast.IsGenerated(f) || buildIgnored(f) {
			return nil
		}
		pk := pkgs[dir]
		if pk == nil {
			pk = &pkg{name: f.Name.Name}
			pkgs[dir] = pk
		}
		pk.files = append(pk.files, rel)
		if pk.firstPos == "" || rel < strings.SplitN(pk.firstPos, ":", 2)[0] {
			pk.firstPos = fmt.Sprintf("%s:%d", rel, fset.Position(f.Package).Line)
		}
		if f.Doc != nil {
			pk.hasDoc = true
		}
		pk.decls = append(pk.decls, fileDecls(f, rel, fset)...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pkgs, nil
}

// buildIgnored reports whether the file's //go:build line is exactly "ignore".
func buildIgnored(f *ast.File) bool {
	for _, cg := range f.Comments {
		if cg.Pos() >= f.Package {
			break
		}
		for _, c := range cg.List {
			if strings.TrimSpace(c.Text) == "//go:build ignore" {
				return true
			}
		}
	}
	return false
}

// recvBase returns the receiver's base type name, or "".
func recvBase(e ast.Expr) string {
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
			return ""
		}
	}
}

func fileDecls(f *ast.File, rel string, fset *token.FileSet) []decl {
	dir := path.Dir(rel)
	var out []decl
	add := func(sym, kind string, pos token.Pos, doc bool) {
		out = append(out, decl{dir: dir, symbol: sym, kind: kind, file: rel, line: fset.Position(pos).Line, documented: doc})
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			if d.Recv == nil || len(d.Recv.List) == 0 {
				add(d.Name.Name, "function", d.Name.Pos(), d.Doc != nil)
				continue
			}
			base := recvBase(d.Recv.List[0].Type)
			if base == "" || !ast.IsExported(base) {
				continue
			}
			add(base+"."+d.Name.Name, "method", d.Name.Pos(), d.Doc != nil)
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, s := range d.Specs {
					ts := s.(*ast.TypeSpec)
					if ts.Name.IsExported() {
						add(ts.Name.Name, "type", ts.Name.Pos(), ts.Doc != nil || ts.Comment != nil || d.Doc != nil)
					}
				}
			case token.CONST, token.VAR:
				kind := d.Tok.String()
				block := d.Lparen.IsValid()
				if block {
					kind += " block"
					if d.Doc != nil {
						continue
					}
				}
				for _, s := range d.Specs {
					vs := s.(*ast.ValueSpec)
					var first *ast.Ident
					for _, n := range vs.Names {
						if n.IsExported() {
							first = n
							break
						}
					}
					if first == nil {
						continue
					}
					documented := vs.Doc != nil || vs.Comment != nil || d.Doc != nil
					add(first.Name, kind, first.Pos(), documented)
					if block && !documented {
						break // one finding per block
					}
				}
			}
		}
	}
	return out
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func cutTitle(s string) string {
	if utf8.RuneCountInString(s) <= maxTitleRunes {
		return s
	}
	r := []rune(s)
	return string(r[:maxTitleRunes-1]) + "…"
}

func uniq(in []string) []string {
	sort.Strings(in)
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

func fp(kind, dir, sym string) string {
	s := "docs:" + kind + ":" + dir
	if sym != "" {
		s += ":" + sym
	}
	if len(s) > maxFingerprint {
		s = "docs:" + kind + ":sha256:" + hash(dir+":"+sym)
	}
	return s
}

func build(pkgs map[string]*pkg) ([]finding, error) {
	var out []finding
	dirs := make([]string, 0, len(pkgs))
	for d := range pkgs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		pk := pkgs[dir]
		group := dir
		if len(group) > maxGroupBytes {
			group = "sha256:" + hash(dir)
		}
		if !pk.hasDoc {
			file := strings.SplitN(pk.firstPos, ":", 2)[0]
			out = append(out, finding{
				Fingerprint: fp("pkg", dir, ""),
				Title:       cutTitle(fmt.Sprintf("%s: package %s has no package comment", dir, pk.name)),
				Severity:    "low",
				Group:       group,
				Files:       []string{file},
				Body:        fmt.Sprintf("Package without a package comment.\n\n- `%s`\n\nAdd a comment directly above the package clause that starts with \"Package %s\" (https://go.dev/doc/comment).", pk.firstPos, pk.name),
			})
		}
		// Merge same-symbol declarations (build-tagged twins).
		bySym := map[string][]decl{}
		var syms []string
		for _, d := range pk.decls {
			if !validText(d.symbol) {
				return nil, fmt.Errorf("%s: identifier %q has a control character or is not valid UTF-8", d.file, d.symbol)
			}
			if _, ok := bySym[d.symbol]; !ok {
				syms = append(syms, d.symbol)
			}
			bySym[d.symbol] = append(bySym[d.symbol], d)
		}
		for _, sym := range syms {
			var bad []decl
			for _, d := range bySym[sym] {
				if !d.documented {
					bad = append(bad, d)
				}
			}
			if len(bad) == 0 {
				continue
			}
			sort.Slice(bad, func(i, j int) bool {
				if bad[i].file != bad[j].file {
					return bad[i].file < bad[j].file
				}
				return bad[i].line < bad[j].line
			})
			var files []string
			var body strings.Builder
			kind := bad[0].kind
			title := fmt.Sprintf("%s: %s has no doc comment", dir, sym)
			if strings.HasSuffix(kind, " block") {
				title = fmt.Sprintf("%s: %s starting %s has no doc comment", dir, kind, sym)
			}
			fmt.Fprintf(&body, "Undocumented exported %s `%s`.\n\n", kind, sym)
			for _, d := range bad {
				files = append(files, d.file)
				fmt.Fprintf(&body, "- `%s:%d`\n", d.file, d.line)
			}
			body.WriteString("\n" + hint)
			out = append(out, finding{
				Fingerprint: fp("undoc", dir, sym),
				Title:       cutTitle(title),
				Severity:    "low",
				Group:       group,
				Files:       uniq(files),
				Body:        body.String(),
			})
		}
	}
	if len(out) > maxFindings {
		return nil, fmt.Errorf("%d findings exceed the limit of %d: add entries to the exclude file", len(out), maxFindings)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Fingerprint < out[j].Fingerprint })
	for i := 1; i < len(out); i++ {
		if out[i].Fingerprint == out[i-1].Fingerprint {
			return nil, errors.New("duplicate fingerprint " + out[i].Fingerprint)
		}
	}
	return out, nil
}

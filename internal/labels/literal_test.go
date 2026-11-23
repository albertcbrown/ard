package labels

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestNoLabelLiteralsElsewhere keeps every spelling of an ard: label inside
// this package: other code must call the constructors or use the constants.
func TestNoLabelLiteralsElsewhere(t *testing.T) {
	root := filepath.Join("..", "..")
	own := filepath.Join(root, "internal", "labels")
	label := regexp.MustCompile(`^ard:[a-z]`)
	fset := token.NewFileSet()
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && filepath.Clean(path) == filepath.Clean(own) {
				// Skip this package's own files but still walk subpackages.
				return nil
			}
			if d.IsDir() && d.Name() == "testdata" {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if filepath.Dir(path) == filepath.Clean(own) {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				bl, ok := n.(*ast.BasicLit)
				if !ok || bl.Kind != token.STRING {
					return true
				}
				if s, err := strconv.Unquote(bl.Value); err == nil && label.MatchString(s) {
					t.Errorf("%s: literal %s spells an ard: label; use package labels", fset.Position(bl.Pos()), bl.Value)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

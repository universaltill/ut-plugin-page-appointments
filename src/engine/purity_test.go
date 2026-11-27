package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const modPath = "github.com/universaltill/ut-plugin-page-appointments/"

var allowedImports = map[string]bool{
	"time": true, "sort": true, "strings": true, "errors": true, "fmt": true, "unicode/utf8": true,
	"encoding/json": true, "database/sql/driver": true, "math": true, "math/bits": true, "strconv": true,
}

func nonTestFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(p string, i os.FileInfo, err error) error {
		if err == nil && !i.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			out = append(out, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("no files under %s", dir)
	}
	return out
}

func TestPurity_Imports(t *testing.T) {
	for _, dir := range []string{".", "../money"} {
		for _, f := range nonTestFiles(t, dir) {
			af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, im := range af.Imports {
				p := strings.Trim(im.Path.Value, `"`)
				if strings.HasPrefix(p, modPath) || allowedImports[p] {
					if p == "database/sql/driver" && !strings.Contains(f, "money") {
						t.Errorf("%s imports %s (money only)", f, p)
					}
					continue
				}
				t.Errorf("%s imports forbidden package %q", f, p)
			}
		}
	}
}

func TestPurity_NoTimeNow(t *testing.T) {
	for _, dir := range []string{".", "../money"} {
		for _, f := range nonTestFiles(t, dir) {
			af, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(af, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "time" && sel.Sel.Name == "Now" {
						t.Errorf("%s calls time.Now", f)
					}
				}
				return true
			})
		}
	}
}

func isMoney(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.ArrayType:
		return isMoney(x.Elt)
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		return ok && id.Name == "money" && x.Sel.Name == "Money"
	}
	return false
}

func moneyName(n string) bool {
	l := strings.ToLower(n)
	for _, w := range []string{"amount", "total", "deposit", "price", "remainder", "applied", "excess"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

func TestMoney_NoMoneyAsInt(t *testing.T) {
	for _, f := range nonTestFiles(t, ".") {
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			switch x := d.(type) {
			case *ast.FuncDecl:
				if !x.Name.IsExported() {
					continue
				}
				var fl []*ast.Field
				if x.Type.Params != nil {
					fl = append(fl, x.Type.Params.List...)
				}
				if x.Type.Results != nil {
					fl = append(fl, x.Type.Results.List...)
				}
				for _, fld := range fl {
					for _, n := range fld.Names {
						if moneyName(n.Name) && !isMoney(fld.Type) {
							t.Errorf("%s: %s param/result %q is not money.Money", f, x.Name.Name, n.Name)
						}
					}
				}
			case *ast.GenDecl:
				for _, s := range x.Specs {
					ts, ok := s.(*ast.TypeSpec)
					st, ok2 := func() (*ast.StructType, bool) {
						if !ok {
							return nil, false
						}
						v, k := ts.Type.(*ast.StructType)
						return v, k
					}()
					if !ok2 || !ts.Name.IsExported() {
						continue
					}
					for _, fld := range st.Fields.List {
						for _, n := range fld.Names {
							if n.IsExported() && moneyName(n.Name) && !isMoney(fld.Type) {
								t.Errorf("%s: %s.%s is not money.Money", f, ts.Name.Name, n.Name)
							}
						}
					}
				}
			}
		}
	}
}

package objectpath_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"golang.org/x/tools/go/gcexportdata"
	"golang.org/x/tools/go/types/objectpath"
)

const src = `
package src

type I int

func (I) F() *struct{ X, Y int } {
	return nil
}

type Foo interface {
	Method() (string, func(int) struct{ X int })
}

var X chan struct{ Z int }
var Z map[string]struct{ A int }
`

func Test(t *testing.T) {
	// Parse source file and type-check it as a package, "src".
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "src.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	conf := types.Config{}
	info := &types.Info{
		Defs: make(map[*ast.Ident]types.Object),
	}
	srcpkg, err := conf.Check("src", fset, []*ast.File{f}, info)
	if err != nil {
		t.Fatal(err)
	}

	// Export binary export data then reload it as a new package, "bin".
	var buf bytes.Buffer
	if err := gcexportdata.Write(&buf, fset, srcpkg); err != nil {
		t.Fatal(err)
	}

	imports := make(map[string]*types.Package)
	binpkg, err := gcexportdata.Read(&buf, fset, imports, "bin")
	if err != nil {
		t.Fatal(err)
	}

	// Now find the correspondences between them.
	for _, srcobj := range info.Defs {
		if srcobj == nil {
			continue // e.g. package declaration
		}
		path, err := objectpath.PathOf(srcobj)
		if err != nil {
			t.Errorf("PathOf(%v): %v", srcobj, err)
			continue
		}
		binobj, err := objectpath.FindObject(binpkg, path)
		if err != nil {
			t.Errorf("Find(%q, %v): %v", binpkg.Path(), path, err)
		}
		t.Logf("path %q\n\t%s\n\t%s", path, srcobj, binobj)
	}
}

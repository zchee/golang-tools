package inspector_test

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"log"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/go/ast/inspector"
)

var netFiles []*ast.File

func init() {
	files, err := parseNetFiles()
	if err != nil {
		log.Fatal(err)
	}
	netFiles = files
}

func parseNetFiles() ([]*ast.File, error) {
	pkg, err := build.Default.Import("net", "", 0)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, filename := range pkg.GoFiles {
		filename = filepath.Join(pkg.Dir, filename)
		f, err := parser.ParseFile(fset, filename, nil, 0)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// TestAllNodes compares Inspector.Inspect against ast.Inspect.
func TestInspectAllNodes(t *testing.T) {
	in := inspector.New(netFiles)

	var nodesA []ast.Node
	in.Inspect(func(n ast.Node) bool {
		nodesA = append(nodesA, n)
		return true
	})
	var nodesB []ast.Node
	for _, f := range netFiles {
		ast.Inspect(f, func(n ast.Node) bool {
			nodesB = append(nodesB, n)
			return true
		})
	}
	compare(t, nodesA, nodesB)
}

// TestPruning compares Inspector.Inspect against ast.Inspect, pruning
// descent within ast.CallExpr nodes.
func TestInspectPruning(t *testing.T) {
	in := inspector.New(netFiles)

	var nodesA []ast.Node
	in.Inspect(func(n ast.Node) bool {
		nodesA = append(nodesA, n)
		_, isCall := n.(*ast.CallExpr)
		return !isCall // don't descend into function calls
	})
	var nodesB []ast.Node
	for _, f := range netFiles {
		ast.Inspect(f, func(n ast.Node) bool {
			nodesB = append(nodesB, n)
			_, isCall := n.(*ast.CallExpr)
			return !isCall // don't descend into function calls
		})
	}
	compare(t, nodesA, nodesB)
}

func compare(t *testing.T, nodesA, nodesB []ast.Node) {
	if len(nodesA) != len(nodesB) {
		t.Errorf("inconsistent node lists: %d vs %d", len(nodesA), len(nodesB))
	} else {
		for i := range nodesA {
			if a, b := nodesA[i], nodesB[i]; a != b {
				t.Errorf("node %d is inconsistent: %T, %T", i, a, b)
			}
		}
	}
}

func TestInspectTypes(t *testing.T) {
	const src = `package a
func f() {
	print("hi")
	panic("oops")
}
`
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "a.go", src, 0)
	inspect := inspector.New([]*ast.File{f})

	var got []string
	fn := func(n ast.Node, push bool) bool {
		if push {
			got = append(got, typeOf(n))
		}
		return true
	}

	// Types: match all types.
	inspect.Types(nil, fn)
	if want := strings.Fields("File Ident FuncDecl Ident FuncType FieldList BlockStmt ExprStmt CallExpr Ident BasicLit ExprStmt CallExpr Ident BasicLit"); !reflect.DeepEqual(got, want) {
		t.Errorf("inspect: got %s, want %s", got, want)
	}

	// Types: match only two node types.
	nodeTypes := []ast.Node{
		(*ast.BasicLit)(nil),
		(*ast.CallExpr)(nil),
	}
	got = nil
	inspect.Types(nodeTypes, fn)
	if want := strings.Fields("CallExpr BasicLit CallExpr BasicLit"); !reflect.DeepEqual(got, want) {
		t.Errorf("inspect: got %s, want %s", got, want)
	}

	// TypesWithStack
	got = nil
	inspect.TypesWithStack(nodeTypes, func(n ast.Node, push bool, stack []ast.Node) bool {
		if push {
			var line []string
			for _, n := range stack {
				line = append(line, typeOf(n))
			}
			got = append(got, strings.Join(line, " "))
		}
		return true
	})
	want := []string{
		"File FuncDecl BlockStmt ExprStmt CallExpr",
		"File FuncDecl BlockStmt ExprStmt CallExpr BasicLit",
		"File FuncDecl BlockStmt ExprStmt CallExpr",
		"File FuncDecl BlockStmt ExprStmt CallExpr BasicLit",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("inspect: got %s, want %s", got, want)
	}
}

func typeOf(n ast.Node) string {
	return strings.TrimPrefix(reflect.TypeOf(n).String(), "*ast.")
}

// The numbers on linux/amd64 (3.5GHz Haswell) show a marginal
// improvement of about 2.3x but a break-even point of 5 traversals.
//
// BenchmarkNewInspector-12             500           3753182 ns/op
// BenchmarkInspect-12                 3000            507081 ns/op
// BenchmarkASTInspect-12              2000           1147959 ns/op

func BenchmarkNewInspector(b *testing.B) {
	// Measure one-time construction overhead.
	for i := 0; i < b.N; i++ {
		inspector.New(netFiles)
	}
}

func BenchmarkInspect(b *testing.B) {
	b.StopTimer()
	in := inspector.New(netFiles)
	b.StartTimer()

	// Measure marginal cost of traversal.
	var ndecls, nlits int
	for i := 0; i < b.N; i++ {
		in.Inspect(func(n ast.Node) bool {
			switch n.(type) {
			case *ast.FuncDecl:
				ndecls++
			case *ast.FuncLit:
				nlits++
			}
			return true
		})
	}
}

func BenchmarkASTInspect(b *testing.B) {
	var ndecls, nlits int
	for i := 0; i < b.N; i++ {
		for _, f := range netFiles {
			ast.Inspect(f, func(n ast.Node) bool {
				switch n.(type) {
				case *ast.FuncDecl:
					ndecls++
				case *ast.FuncLit:
					nlits++
				}
				return true
			})
		}
	}
}

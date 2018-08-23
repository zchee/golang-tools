// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vet

import (
	"go/ast"
	"go/types"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/go/analysis/plugin/inspect"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/inspector"
)

var TestFunctionsAnalysis = &analysis.Analysis{
	Name:     "tests",
	Doc:      "check for common mistaken usages of tests/documentation examples",
	Requires: []*analysis.Analysis{inspect.Analysis},
	Run:      runTestFunctions,
}

// runTestFunctions walks Test, Benchmark and Example functions checking
// malformed names, wrong signatures and examples documenting nonexistent
// identifiers.
func runTestFunctions(unit *analysis.Unit) error {
	inspect := unit.Inputs[inspect.Analysis].(*inspector.Inspector)

	nodeTypes := []ast.Node{
		(*ast.FuncDecl)(nil),
	}
	inspect.Types(nodeTypes, func(n ast.Node, push bool) bool {
		if !push {
			return true
		}
		fn := n.(*ast.FuncDecl)
		if fn.Recv != nil {
			// Ignore non-functions or functions with receivers.
			return true
		}

		if !strings.HasSuffix(unit.Fset.File(fn.Pos()).Name(), "_test.go") {
			return true
		}

		report := func(format string, args ...interface{}) {
			unit.Findingf(fn.Pos(), format, args...)
		}

		switch {
		case strings.HasPrefix(fn.Name.Name, "Example"):
			checkExample(unit, fn, report)
		case strings.HasPrefix(fn.Name.Name, "Test"):
			checkTest(fn, "Test", report)
		case strings.HasPrefix(fn.Name.Name, "Benchmark"):
			checkTest(fn, "Benchmark", report)
		}
		return true
	})
	return nil
}

type reporter func(format string, args ...interface{})

func checkExample(unit *analysis.Unit, fn *ast.FuncDecl, report reporter) {
	fnName := fn.Name.Name
	if params := fn.Type.Params; len(params.List) != 0 {
		report("%s should be niladic", fnName)
	}
	if results := fn.Type.Results; results != nil && len(results.List) != 0 {
		report("%s should return nothing", fnName)
	}

	if fnName == "Example" {
		// Nothing more to do.
		return
	}

	var (
		exName = strings.TrimPrefix(fnName, "Example")
		elems  = strings.SplitN(exName, "_", 3)
		ident  = elems[0]
		obj    = lookup(ident, extendedScope(unit.Pkg))
	)
	if ident != "" && obj == nil {
		// Check ExampleFoo and ExampleBadFoo.
		report("%s refers to unknown identifier: %s", fnName, ident)
		// Abort since obj is absent and no subsequent checks can be performed.
		return
	}
	if len(elems) < 2 {
		// Nothing more to do.
		return
	}

	if ident == "" {
		// Check Example_suffix and Example_BadSuffix.
		if residual := strings.TrimPrefix(exName, "_"); !isExampleSuffix(residual) {
			report("%s has malformed example suffix: %s", fnName, residual)
		}
		return
	}

	mmbr := elems[1]
	if !isExampleSuffix(mmbr) {
		// Check ExampleFoo_Method and ExampleFoo_BadMethod.
		if obj, _, _ := types.LookupFieldOrMethod(obj.Type(), true, obj.Pkg(), mmbr); obj == nil {
			report("%s refers to unknown field or method: %s.%s", fnName, ident, mmbr)
		}
	}
	if len(elems) == 3 && !isExampleSuffix(elems[2]) {
		// Check ExampleFoo_Method_suffix and ExampleFoo_Method_Badsuffix.
		report("%s has malformed example suffix: %s", fnName, elems[2])
	}
}

func isExampleSuffix(s string) bool {
	r, size := utf8.DecodeRuneInString(s)
	return size > 0 && unicode.IsLower(r)
}

func isTestSuffix(name string) bool {
	if len(name) == 0 {
		// "Test" is ok.
		return true
	}
	r, _ := utf8.DecodeRuneInString(name)
	return !unicode.IsLower(r)
}

func isTestParam(typ ast.Expr, wantType string) bool {
	ptr, ok := typ.(*ast.StarExpr)
	if !ok {
		// Not a pointer.
		return false
	}
	// No easy way of making sure it's a *testing.T or *testing.B:
	// ensure the name of the type matches.
	if name, ok := ptr.X.(*ast.Ident); ok {
		return name.Name == wantType
	}
	if sel, ok := ptr.X.(*ast.SelectorExpr); ok {
		return sel.Sel.Name == wantType
	}
	return false
}

func lookup(name string, scopes []*types.Scope) types.Object {
	for _, scope := range scopes {
		if o := scope.Lookup(name); o != nil {
			return o
		}
	}
	return nil
}

func extendedScope(pkg *types.Package) []*types.Scope {
	scopes := []*types.Scope{pkg.Scope()}
	var basePkg *types.Package // TODO(adonovan): how to relate test to package under test?
	if basePkg != nil {
		scopes = append(scopes, basePkg.Scope())
	} else {
		// If basePkg is not specified (e.g. when checking a single file) try to
		// find it among imports.
		pkgName := pkg.Name()
		if strings.HasSuffix(pkgName, "_test") {
			basePkgName := strings.TrimSuffix(pkgName, "_test")
			for _, p := range pkg.Imports() {
				if p.Name() == basePkgName {
					scopes = append(scopes, p.Scope())
					break
				}
			}
		}
	}
	return scopes
}

func checkTest(fn *ast.FuncDecl, prefix string, report reporter) {
	// Want functions with 0 results and 1 parameter.
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 ||
		fn.Type.Params == nil ||
		len(fn.Type.Params.List) != 1 ||
		len(fn.Type.Params.List[0].Names) > 1 {
		return
	}

	// The param must look like a *testing.T or *testing.B.
	if !isTestParam(fn.Type.Params.List[0].Type, prefix[:1]) {
		return
	}

	if !isTestSuffix(fn.Name.Name[len(prefix):]) {
		report("%s has malformed name: first letter after '%s' must not be lowercase", fn.Name.Name, prefix)
	}
}

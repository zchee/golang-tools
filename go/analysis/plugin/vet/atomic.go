// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vet

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis/plugin/inspect"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/inspector"
)

var AtomicAnalysis = &analysis.Analysis{
	Name:     "atomic",
	Doc:      "check for common mistaken usages of the sync/atomic package",
	Requires: []*analysis.Analysis{inspect.Analysis},
	Run:      runAtomicAssignment,
}

// runAtomicAssignment walks the assignment statement checking for common
// mistaken usage of atomic package, such as: x = atomic.AddUint64(&x, 1)
func runAtomicAssignment(unit *analysis.Unit) error {
	inspect := unit.Inputs[inspect.Analysis].(*inspector.Inspector)

	nodeTypes := []ast.Node{
		(*ast.AssignStmt)(nil),
	}
	inspect.Types(nodeTypes, func(node ast.Node, push bool) bool {
		if !push {
			return true
		}
		n := node.(*ast.AssignStmt)
		if len(n.Lhs) != len(n.Rhs) {
			return true
		}
		if len(n.Lhs) == 1 && n.Tok == token.DEFINE {
			return true
		}

		for i, right := range n.Rhs {
			call, ok := right.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			pkgIdent, _ := sel.X.(*ast.Ident)
			pkgName, ok := unit.Info.Uses[pkgIdent].(*types.PkgName)
			if !ok || pkgName.Imported().Path() != "sync/atomic" {
				continue
			}

			switch sel.Sel.Name {
			case "AddInt32", "AddInt64", "AddUint32", "AddUint64", "AddUintptr":
				checkAtomicAddAssignment(unit, n.Lhs[i], call)
			}
		}
		return true
	})
	return nil
}

// checkAtomicAddAssignment walks the atomic.Add* method calls checking
// for assigning the return value to the same variable being used in the
// operation
func checkAtomicAddAssignment(unit *analysis.Unit, left ast.Expr, call *ast.CallExpr) {
	if len(call.Args) != 2 {
		return
	}
	arg := call.Args[0]
	broken := false

	if uarg, ok := arg.(*ast.UnaryExpr); ok && uarg.Op == token.AND {
		broken = gofmt(unit, left) == gofmt(unit, uarg.X)
	} else if star, ok := left.(*ast.StarExpr); ok {
		broken = gofmt(unit, star.X) == gofmt(unit, arg)
	}

	if broken {
		unit.Findingf(left.Pos(), "direct assignment to atomic value")
	}
}

// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vet

/*
This file contains the code to check for useless function comparisons.
A useless comparison is one like f == nil as opposed to f() == nil.
*/

// TODO: delete this if/when it is subsumed by the SSA-based nilness checker.

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis/plugin/inspect"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/inspector"
)

var NilFuncAnalysis = &analysis.Analysis{
	Name:     "nilfunc",
	Doc:      "check for comparisons between functions and nil",
	Requires: []*analysis.Analysis{inspect.Analysis},
	Run:      runNilFunc,
}

func runNilFunc(unit *analysis.Unit) error {
	inspect := unit.Inputs[inspect.Analysis].(*inspector.Inspector)

	nodeTypes := []ast.Node{
		(*ast.BinaryExpr)(nil),
	}
	inspect.Types(nodeTypes, func(n ast.Node, push bool) bool {
		if !push {
			return true
		}
		e := n.(*ast.BinaryExpr)

		// Only want == or != comparisons.
		if e.Op != token.EQL && e.Op != token.NEQ {
			return true
		}

		// Only want comparisons with a nil identifier on one side.
		var e2 ast.Expr
		switch {
		case unit.Info.Types[e.X].IsNil():
			e2 = e.Y
		case unit.Info.Types[e.Y].IsNil():
			e2 = e.X
		default:
			return true
		}

		// Only want identifiers or selector expressions.
		var obj types.Object
		switch v := e2.(type) {
		case *ast.Ident:
			obj = unit.Info.Uses[v]
		case *ast.SelectorExpr:
			obj = unit.Info.Uses[v.Sel]
		default:
			return true
		}

		// Only want functions.
		if _, ok := obj.(*types.Func); !ok {
			return true
		}

		unit.Findingf(e.Pos(), "comparison of function %v %v nil is always %v", obj.Name(), e.Op, e.Op == token.NEQ)
		return true
	})
	return nil
}

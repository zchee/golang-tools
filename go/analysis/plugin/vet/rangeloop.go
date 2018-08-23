// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
This file contains the code to check range loop variables bound inside function
literals that are deferred or launched in new goroutines. We only check
instances where the defer or go statement is the last statement in the loop
body, as otherwise we would need whole program analysis.

For example:

	for i, v := range s {
		go func() {
			println(i, v) // not what you might expect
		}()
	}

See: https://golang.org/doc/go_faq.html#closures_and_goroutines
*/

package vet

import (
	"go/ast"

	"golang.org/x/tools/go/analysis/plugin/inspect"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/inspector"
)

var RangeLoopAnalysis = &analysis.Analysis{
	Name:     "rangeloops",
	Doc:      "check that loop variables are used correctly",
	Requires: []*analysis.Analysis{inspect.Analysis},
	Run:      runRangeLoop,
}

// runRangeLoop walks the body of the provided loop statement, checking whether
// its index or value variables are used unsafely inside goroutines or deferred
// function literals.
func runRangeLoop(unit *analysis.Unit) error {
	inspect := unit.Inputs[inspect.Analysis].(*inspector.Inspector)

	nodeTypes := []ast.Node{
		(*ast.RangeStmt)(nil),
		(*ast.ForStmt)(nil),
	}
	inspect.Types(nodeTypes, func(n ast.Node, push bool) bool {
		if !push {
			return true
		}

		// Find the variables updated by the loop statement.
		var vars []*ast.Ident
		addVar := func(expr ast.Expr) {
			if id, ok := expr.(*ast.Ident); ok {
				vars = append(vars, id)
			}
		}
		var body *ast.BlockStmt
		switch n := n.(type) {
		case *ast.RangeStmt:
			body = n.Body
			addVar(n.Key)
			addVar(n.Value)
		case *ast.ForStmt:
			body = n.Body
			switch post := n.Post.(type) {
			case *ast.AssignStmt:
				// e.g. for p = head; p != nil; p = p.next
				for _, lhs := range post.Lhs {
					addVar(lhs)
				}
			case *ast.IncDecStmt:
				// e.g. for i := 0; i < n; i++
				addVar(post.X)
			}
		}
		if vars == nil {
			return true
		}

		// Inspect a go or defer statement
		// if it's the last one in the loop body.
		// (We give up if there are following statements,
		// because it's hard to prove go isn't followed by wait,
		// or defer by return.)
		if len(body.List) == 0 {
			return true
		}
		var last *ast.CallExpr
		switch s := body.List[len(body.List)-1].(type) {
		case *ast.GoStmt:
			last = s.Call
		case *ast.DeferStmt:
			last = s.Call
		default:
			return true
		}
		lit, ok := last.Fun.(*ast.FuncLit)
		if !ok {
			return true
		}
		ast.Inspect(lit.Body, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || id.Obj == nil {
				return true
			}
			if unit.Info.Types[id].Type == nil {
				// Not referring to a variable (e.g. struct field name)
				return true
			}
			for _, v := range vars {
				if v.Obj == id.Obj {
					unit.Findingf(id.Pos(), "loop variable %s captured by func literal",
						id.Name)
				}
			}
			return true
		})
		return true
	})
	return nil
}

// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
This file contains the code to check for useless assignments.
*/

package vet

import (
	"go/ast"
	"go/token"
	"reflect"

	"golang.org/x/tools/go/analysis/plugin/inspect"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/inspector"
)

var AssignAnalysis = &analysis.Analysis{
	Name:     "assign",
	Doc:      "check for useless assignments",
	Requires: []*analysis.Analysis{inspect.Analysis},
	Run:      runAssign,
}

// TODO: should also check for assignments to struct fields inside methods
// that are on T instead of *T.

// runAssign checks for assignments of the form "<expr> = <expr>".
// These are almost always useless, and even when they aren't they are usually a mistake.
func runAssign(unit *analysis.Unit) error {
	inspect := unit.Inputs[inspect.Analysis].(*inspector.Inspector)

	nodeTypes := []ast.Node{
		(*ast.AssignStmt)(nil),
	}
	inspect.Types(nodeTypes, func(n ast.Node, push bool) bool {
		if !push {
			return true
		}
		stmt := n.(*ast.AssignStmt)
		if stmt.Tok != token.ASSIGN {
			return true // ignore :=
		}
		if len(stmt.Lhs) != len(stmt.Rhs) {
			// If LHS and RHS have different cardinality, they can't be the same.
			return true
		}
		for i, lhs := range stmt.Lhs {
			rhs := stmt.Rhs[i]
			if hasSideEffects(unit.Info, lhs) || hasSideEffects(unit.Info, rhs) {
				continue // expressions may not be equal
			}
			if reflect.TypeOf(lhs) != reflect.TypeOf(rhs) {
				continue // short-circuit the heavy-weight gofmt check
			}
			le := gofmt(unit, lhs)
			re := gofmt(unit, rhs)
			if le == re {
				unit.Findingf(stmt.Pos(), "self-assignment of %s to %s", re, le)
			}
		}
		return true
	})

	return nil
}

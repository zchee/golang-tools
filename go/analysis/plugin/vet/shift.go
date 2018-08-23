// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vet

// This file contains the code to check for suspicious shifts.

// TODO(adonovan): integrate with ctrflow (CFG-based) dead code analysis. May
// have impedance mismatch due to its (non-)treatment of constant
// expressions (such as runtime.GOARCH=="386").

import (
	"go/ast"
	"go/build"
	"go/constant"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis/plugin/inspect"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/inspector"
)

var ShiftAnalysis = &analysis.Analysis{
	Name:     "shift",
	Doc:      "check for useless shifts",
	Requires: []*analysis.Analysis{inspect.Analysis},
	Run:      runShift,
}

func runShift(unit *analysis.Unit) error {
	inspect := unit.Inputs[inspect.Analysis].(*inspector.Inspector)

	// Do a complete pass to compute dead nodes.
	// TODO: make this more efficient.
	dead := make(map[ast.Node]bool)
	inspect.Inspect(func(n ast.Node) bool {
		updateDead(unit.Info, dead, n)
		return true
	})

	nodeTypes := []ast.Node{
		(*ast.AssignStmt)(nil),
		(*ast.BinaryExpr)(nil),
	}
	inspect.Types(nodeTypes, func(node ast.Node, push bool) bool {
		if !push {
			return true
		}

		if dead[node] {
			// Skip shift checks on unreachable nodes.
			return true
		}

		switch node := node.(type) {
		case *ast.BinaryExpr:
			if node.Op == token.SHL || node.Op == token.SHR {
				checkLongShift(unit, node, node.X, node.Y)
			}
		case *ast.AssignStmt:
			if len(node.Lhs) != 1 || len(node.Rhs) != 1 {
				return true
			}
			if node.Tok == token.SHL_ASSIGN || node.Tok == token.SHR_ASSIGN {
				checkLongShift(unit, node, node.Lhs[0], node.Rhs[0])
			}
		}
		return true
	})
	return nil
}

// checkLongShift checks if shift or shift-assign operations shift by more than
// the length of the underlying variable.
func checkLongShift(unit *analysis.Unit, node ast.Node, x, y ast.Expr) {
	if unit.Info.Types[x].Value != nil {
		// Ignore shifts of constants.
		// These are frequently used for bit-twiddling tricks
		// like ^uint(0) >> 63 for 32/64 bit detection and compatibility.
		return
	}

	v := unit.Info.Types[y].Value
	if v == nil {
		return
	}
	amt, ok := constant.Int64Val(v)
	if !ok {
		return
	}
	t := unit.Info.Types[x].Type
	if t == nil {
		return
	}
	b, ok := t.Underlying().(*types.Basic)
	if !ok {
		return
	}
	var size int64
	switch b.Kind() {
	case types.Uint8, types.Int8:
		size = 8
	case types.Uint16, types.Int16:
		size = 16
	case types.Uint32, types.Int32:
		size = 32
	case types.Uint64, types.Int64:
		size = 64
	case types.Int, types.Uint:
		size = uintBitSize
	case types.Uintptr:
		size = uintptrBitSize
	default:
		return
	}
	if amt >= size {
		ident := gofmt(unit, x)
		unit.Findingf(node.Pos(), "%s (%d bits) too small for shift of %d", ident, size, amt)
	}
}

var (
	uintBitSize    = 8 * archSizes.Sizeof(types.Typ[types.Uint])
	uintptrBitSize = 8 * archSizes.Sizeof(types.Typ[types.Uintptr])
)

var archSizes = types.SizesFor("gc", build.Default.GOARCH)

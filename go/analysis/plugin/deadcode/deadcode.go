// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package deadcode reports dead code.
package deadcode

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/plugin/ctrlflow"
	"golang.org/x/tools/go/analysis/plugin/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/cfg"
)

var Analysis = &analysis.Analysis{
	Name:             "deadcode",
	Doc:              "check for unreachable code",
	Run:              run,
	RunDespiteErrors: true,
	Requires: []*analysis.Analysis{
		ctrlflow.Analysis,
		inspect.Analysis,
	},
}

func run(unit *analysis.Unit) error {
	cfgs := unit.Inputs[ctrlflow.Analysis].(*ctrlflow.CFGs)
	inspect := unit.Inputs[inspect.Analysis].(*inspector.Inspector)

	nodeTypes := []ast.Node{
		(*ast.FuncDecl)(nil),
		(*ast.FuncLit)(nil),
	}
	inspect.Types(nodeTypes, func(n ast.Node, push bool) bool {
		if push {
			switch n := n.(type) {
			case *ast.FuncDecl:
				if n.Body != nil {
					checkCFG(unit, cfgs.FuncDecl(n))
				}
			case *ast.FuncLit:
				checkCFG(unit, cfgs.FuncLit(n))
			}
		}
		return true
	})

	return nil
}

func checkCFG(unit *analysis.Unit, g *cfg.CFG) {
	// Two thirds of CFGs have trivial dead blocks due to
	// the CFG construction mechanism. If we look only at
	// dead blocks with nodes or succs, about one third pass.
	hasDead := false
	for _, b := range g.Blocks {
		if !b.Live && (len(b.Nodes) > 0 || len(b.Succs) > 0) {
			hasDead = true
			break
		}
	}
	if !hasDead {
		return // no dead blocks
	}

	// Ideally we would report a finding only for the most
	// dominating unseen ancestor of each unseen block,
	// but we don't have a dominator tree.

	// Build predecessor (inverse) graph over dead blocks.
	// TODO: should CFG include Preds edges?
	preds := make([][]int32, len(g.Blocks))
	for _, b := range g.Blocks {
		if !b.Live {
			for _, succ := range b.Succs {
				if !succ.Live {
					preds[succ.Index] = append(preds[succ.Index], b.Index)
				}
			}
		}
	}

	// Now report an error only for each leaf in this graph.
	// Break cycles arbitrarily.
	marked := make([]bool, len(g.Blocks))

	// Mark all blocks reachable from this one,
	// to avoid redundant findings.
	// The result of mark indicates whether
	// an finding was reported on this path.
	var mark func(b *cfg.Block) bool
	mark = func(b *cfg.Block) bool {
		if !marked[b.Index] {
			marked[b.Index] = true
			if preds[b.Index] == nil {
				// A leaf: the first dead block in a path,
				// often the dominating one.
				// If the block is nonempty, report an error.
				// Otherwise back up.
				if len(b.Nodes) > 0 {
					if n := b.Nodes[0]; n.Pos().IsValid() {
						// Suppress an error for an explicitly unreachable
						// statement such as panic("unreachable").
						// Return true nonetheless to mark the whole path.
						if !explicitlyUnreachable(unit.Info, n) {
							unit.Findingf(n.Pos(), "unreachable statement")
						}
						return true
					}
				}
			} else {
				// Prune the traversal once we've returned (or suppressed) an error.
				for _, pred := range preds[b.Index] {
					if mark(g.Blocks[pred]) {
						return true
					}
				}
			}
			return false
		}
		return true
	}
	for _, b := range g.Blocks {
		if !b.Live {
			mark(b)
		}
	}

	// TODO: in tests, t.Skip is always intentionally followed by dead code.
	// Suppress errors in that case.
}

// explicitlyUnreachable reports whether the specified
// statement is an explicit call such as panic("unreachable").
// It also permits a plain or zero return statement,
// as these are common after log.Fatal.
func explicitlyUnreachable(info *types.Info, n ast.Node) bool {
	switch stmt := n.(type) {
	case *ast.ExprStmt:
		// call to panic("unreachable")?
		if call, ok := stmt.X.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok {
				if info.Uses[id] == panicBuiltin && len(call.Args) == 1 {
					if k := info.Types[call.Args[0]].Value; k != nil && k.Kind() == constant.String {
						s := constant.StringVal(k)
						return strings.Contains(s, "unreachable") || strings.Contains(s, "not reached")
					}
				}
			}
		}
	case *ast.ReturnStmt:
		// Permit a blank return, or a return of all zeros.
		for _, r := range stmt.Results {
			if !isZeroExpr(info, r) {
				return false
			}
		}
		return true
	}
	return false
}

// isZeroExpr reports whether e trivially denotes the zero value of its type.
func isZeroExpr(info *types.Info, e ast.Expr) bool {
	tv := info.Types[e]
	if tv.IsNil() {
		return true // nil
	}

	if tv.Value != nil && isConstZero(tv.Value) {
		return true // 0, 0.0, 0i, ""
	}

	if complit, ok := e.(*ast.CompositeLit); ok && complit.Elts == nil {
		switch tv.Type.Underlying().(type) {
		case *types.Array, *types.Struct:
			return true // empty struct/array literal, T{}
		}
	}

	return false
}

func isConstZero(v constant.Value) bool {
	switch v.Kind() {
	case constant.Bool:
		return !constant.BoolVal(v)
	case constant.String:
		return constant.StringVal(v) == ""
	case constant.Int, constant.Float, constant.Complex:
		return constant.Compare(v, token.EQL, zero)
	}
	return false
}

var zero = constant.MakeInt64(0)

var panicBuiltin = types.Universe.Lookup("panic").(*types.Builtin)

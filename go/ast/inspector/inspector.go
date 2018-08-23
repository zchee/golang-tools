// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package inspector provides helper functions for traversal over the
// syntax trees of a package, including node filtering by type, and
// materialization of the traversal stack.
//
// During construction, the inspector does a complete traversal and
// builds a list of push/pop events and their node type. Subsequent
// method calls that request a traversal scan this list, rather than walk
// the AST, and perform type filtering using efficient bit sets.
//
// Experiments suggest the inspector's traversals are about 2.5x faster
// than ast.Inspect, but it may take around 5 traversals for this
// benefit to amortize the inspector's construction cost, so if
// efficiency is your primary concern, do not use use Inspector for
// one-off traversals.
package inspector

import (
	"go/ast"
	"reflect"
)

// TODO: the method names may be a bit cryptic.

// An Inspector provides methods for inspecting
// (traversing) the syntax trees of a package.
type Inspector struct {
	events []event
}

// New returns an Inspector for the specified file syntax trees.
func New(files []*ast.File) *Inspector {
	return &Inspector{traverse(files)}
}

// An event represents a push or a pop
// of an ast.Node during a traversal.
type event struct {
	node     ast.Node
	typ      uint64
	popindex int // 1 + index of corresponding pop event, or 0 if this is a pop
}

// Inspect behaves like ast.Inspect applied to all of the files in the package.
func (in *Inspector) Inspect(f func(ast.Node) bool) {
	for i := 0; i < len(in.events); {
		ev := in.events[i]
		if ev.popindex > 0 {
			// push
			if !f(ev.node) {
				i = int(ev.popindex)
				continue
			}
		} else {
			// pop
			f(nil)
		}
		i++
	}
}

// A Func is a function applied to each node n
// before (push) and after (!push) visiting its children.
type Func func(n ast.Node, push bool) bool

// Types traverses all the syntax trees of the package in depth-first
// order. Like Inspect, it applies f to each of the nodes in the tree,
// and uses the boolean return value to determine whether to descend
// into the children of the node. In addition, it provides f with a
// non-nil node argument on all events; "push" and "pop" events are
// instead distinguished by the boolean argument to f.
//
// The types argument, if non-empty, enables type-based filtering of
// events. The function if is called only for nodes whose type matches
// an element of the types slice.
//
// TODO: for many clients, f's boolean result is a chore. Get rid of it?
func (in *Inspector) Types(types []ast.Node, f Func) {
	mask := maskOf(types)
	for i := 0; i < len(in.events); {
		ev := in.events[i]
		if ev.typ&mask != 0 {
			if ev.popindex > 0 {
				// push
				if !f(ev.node, true) {
					i = int(ev.popindex)
					continue
				}
			} else {
				// pop
				f(ev.node, false)
			}
		}
		i++
	}
}

// A FuncWithStack is like Func but additionally
// provides the current traversal stack.
// The stack's first element is the outermost, an *ast.File;
// its last is the innermost, n.
type FuncWithStack func(n ast.Node, push bool, stack []ast.Node) bool

// InspectTypesWithStack is like InspectTypes but it additionally
// supplies the current stack of nodes to the function f.
func (in *Inspector) TypesWithStack(types []ast.Node, f FuncWithStack) {
	mask := maskOf(types)
	var stack []ast.Node
	for i := 0; i < len(in.events); {
		ev := in.events[i]
		if ev.popindex > 0 {
			// push
			stack = append(stack, ev.node)
			if ev.typ&mask != 0 {
				if !f(ev.node, true, stack) {
					i = ev.popindex
					stack = stack[:len(stack)-1]
					continue
				}
			}
		} else {
			// pop
			if ev.typ&mask != 0 {
				f(ev.node, false, stack)
			}
			stack = stack[:len(stack)-1]
		}
		i++
	}
}

// traverse builds the table of events representing a traversal.
func traverse(files []*ast.File) []event {
	// Preallocate estimated number of events
	// based on source file extent.
	// This makes traverse faster by 4x (!).
	var extent int
	for _, f := range files {
		extent += int(f.End() - f.Pos())
	}
	events := make([]event, 0, extent*33/100)

	type item struct {
		node      ast.Node
		typ       uint64
		pushindex int
	}
	var stack []item
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if n != nil {
				// push
				typ := typeOf(n)
				stack = append(stack, item{
					node:      n,
					typ:       typ,
					pushindex: len(events), // index of this event
				})
				events = append(events, event{
					node:     n,
					typ:      typ,
					popindex: -1, // filled in later
				})
			} else {
				// pop
				it := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				events = append(events, event{
					node:     it.node,
					typ:      it.typ,
					popindex: 0,
				})
				events[it.pushindex].popindex = len(events)
			}
			return true
		})
	}

	return events
}

func typeOf(n ast.Node) uint64 { return typebit[reflect.TypeOf(n)] }

func maskOf(nodes []ast.Node) uint64 {
	if nodes == nil {
		return 1<<64 - 1 // match all node types
	}
	var mask uint64
	for _, n := range nodes {
		mask |= typebit[reflect.TypeOf(n)]
	}
	return mask
}

var typebit = make(map[reflect.Type]uint64)

func init() {
	for i, n := range nodeTypes {
		typebit[reflect.TypeOf(n)] = 1 << uint(1+i)
	}
}

// This list includes all nodes encountered by ast.Inspect.
var nodeTypes = []ast.Node{
	(*ast.ArrayType)(nil),
	(*ast.AssignStmt)(nil),
	(*ast.BadDecl)(nil),
	(*ast.BadExpr)(nil),
	(*ast.BadStmt)(nil),
	(*ast.BasicLit)(nil),
	(*ast.BinaryExpr)(nil),
	(*ast.BlockStmt)(nil),
	(*ast.BranchStmt)(nil),
	(*ast.CallExpr)(nil),
	(*ast.CaseClause)(nil),
	(*ast.ChanType)(nil),
	(*ast.CommClause)(nil),
	(*ast.Comment)(nil),
	(*ast.CommentGroup)(nil),
	(*ast.CompositeLit)(nil),
	(*ast.DeclStmt)(nil),
	(*ast.DeferStmt)(nil),
	(*ast.Ellipsis)(nil),
	(*ast.EmptyStmt)(nil),
	(*ast.ExprStmt)(nil),
	(*ast.Field)(nil),
	(*ast.FieldList)(nil),
	(*ast.File)(nil),
	(*ast.ForStmt)(nil),
	(*ast.FuncDecl)(nil),
	(*ast.FuncLit)(nil),
	(*ast.FuncType)(nil),
	(*ast.GenDecl)(nil),
	(*ast.GoStmt)(nil),
	(*ast.Ident)(nil),
	(*ast.IfStmt)(nil),
	(*ast.ImportSpec)(nil),
	(*ast.IncDecStmt)(nil),
	(*ast.IndexExpr)(nil),
	(*ast.InterfaceType)(nil),
	(*ast.KeyValueExpr)(nil),
	(*ast.LabeledStmt)(nil),
	(*ast.MapType)(nil),
	(*ast.Package)(nil),
	(*ast.ParenExpr)(nil),
	(*ast.RangeStmt)(nil),
	(*ast.ReturnStmt)(nil),
	(*ast.SelectStmt)(nil),
	(*ast.SelectorExpr)(nil),
	(*ast.SendStmt)(nil),
	(*ast.SliceExpr)(nil),
	(*ast.StarExpr)(nil),
	(*ast.StructType)(nil),
	(*ast.SwitchStmt)(nil),
	(*ast.TypeAssertExpr)(nil),
	(*ast.TypeSpec)(nil),
	(*ast.TypeSwitchStmt)(nil),
	(*ast.UnaryExpr)(nil),
	(*ast.ValueSpec)(nil),
}

// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains the code to check that locks are not passed by value.

package vet

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis/plugin/inspect"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/inspector"
)

var CopyLocksAnalysis = &analysis.Analysis{
	Name:     "copylocks",
	Doc:      "check that locks are not passed by value",
	Requires: []*analysis.Analysis{inspect.Analysis},
	Run:      runCopyLocks,
}

// runCopyLocks checks whether node might
// inadvertently copy a lock.
func runCopyLocks(unit *analysis.Unit) error {
	inspect := unit.Inputs[inspect.Analysis].(*inspector.Inspector)

	nodeTypes := []ast.Node{
		(*ast.AssignStmt)(nil),
		(*ast.BinaryExpr)(nil),
		(*ast.CallExpr)(nil),
		(*ast.CompositeLit)(nil),
		(*ast.FuncDecl)(nil),
		(*ast.FuncLit)(nil),
		(*ast.GenDecl)(nil),
		(*ast.RangeStmt)(nil),
		(*ast.ReturnStmt)(nil),
	}
	inspect.Types(nodeTypes, func(node ast.Node, push bool) bool {
		if !push {
			return true
		}
		switch node := node.(type) {
		case *ast.RangeStmt:
			checkCopyLocksRange(unit, node)
		case *ast.FuncDecl:
			checkCopyLocksFunc(unit, node.Name.Name, node.Recv, node.Type)
		case *ast.FuncLit:
			checkCopyLocksFunc(unit, "func", nil, node.Type)
		case *ast.CallExpr:
			checkCopyLocksCallExpr(unit, node)
		case *ast.AssignStmt:
			checkCopyLocksAssign(unit, node)
		case *ast.GenDecl:
			checkCopyLocksGenDecl(unit, node)
		case *ast.CompositeLit:
			checkCopyLocksCompositeLit(unit, node)
		case *ast.ReturnStmt:
			checkCopyLocksReturnStmt(unit, node)
		}
		return true
	})
	return nil
}

// checkCopyLocksAssign checks whether an assignment
// copies a lock.
func checkCopyLocksAssign(unit *analysis.Unit, as *ast.AssignStmt) {
	for i, x := range as.Rhs {
		if path := lockPathRhs(unit, x); path != nil {
			unit.Findingf(x.Pos(), "assignment copies lock value to %v: %v", gofmt(unit, as.Lhs[i]), path)
		}
	}
}

// checkCopyLocksGenDecl checks whether lock is copied
// in variable declaration.
func checkCopyLocksGenDecl(unit *analysis.Unit, gd *ast.GenDecl) {
	if gd.Tok != token.VAR {
		return
	}
	for _, spec := range gd.Specs {
		valueSpec := spec.(*ast.ValueSpec)
		for i, x := range valueSpec.Values {
			if path := lockPathRhs(unit, x); path != nil {
				unit.Findingf(x.Pos(), "variable declaration copies lock value to %v: %v", valueSpec.Names[i].Name, path)
			}
		}
	}
}

// checkCopyLocksCompositeLit detects lock copy inside a composite literal
func checkCopyLocksCompositeLit(unit *analysis.Unit, cl *ast.CompositeLit) {
	for _, x := range cl.Elts {
		if node, ok := x.(*ast.KeyValueExpr); ok {
			x = node.Value
		}
		if path := lockPathRhs(unit, x); path != nil {
			unit.Findingf(x.Pos(), "literal copies lock value from %v: %v", gofmt(unit, x), path)
		}
	}
}

// checkCopyLocksReturnStmt detects lock copy in return statement
func checkCopyLocksReturnStmt(unit *analysis.Unit, rs *ast.ReturnStmt) {
	for _, x := range rs.Results {
		if path := lockPathRhs(unit, x); path != nil {
			unit.Findingf(x.Pos(), "return copies lock value: %v", path)
		}
	}
}

// checkCopyLocksCallExpr detects lock copy in the arguments to a function call
func checkCopyLocksCallExpr(unit *analysis.Unit, ce *ast.CallExpr) {
	var id *ast.Ident
	switch fun := ce.Fun.(type) {
	case *ast.Ident:
		id = fun
	case *ast.SelectorExpr:
		id = fun.Sel
	}
	if fun, ok := unit.Info.Uses[id].(*types.Builtin); ok {
		switch fun.Name() {
		case "new", "len", "cap", "Sizeof":
			return
		}
	}
	for _, x := range ce.Args {
		if path := lockPathRhs(unit, x); path != nil {
			unit.Findingf(x.Pos(), "call of %s copies lock value: %v", gofmt(unit, ce.Fun), path)
		}
	}
}

// checkCopyLocksFunc checks whether a function might
// inadvertently copy a lock, by checking whether
// its receiver, parameters, or return values
// are locks.
func checkCopyLocksFunc(unit *analysis.Unit, name string, recv *ast.FieldList, typ *ast.FuncType) {
	if recv != nil && len(recv.List) > 0 {
		expr := recv.List[0].Type
		if path := lockPath(unit.Pkg, unit.Info.Types[expr].Type); path != nil {
			unit.Findingf(expr.Pos(), "%s passes lock by value: %v", name, path)
		}
	}

	if typ.Params != nil {
		for _, field := range typ.Params.List {
			expr := field.Type
			if path := lockPath(unit.Pkg, unit.Info.Types[expr].Type); path != nil {
				unit.Findingf(expr.Pos(), "%s passes lock by value: %v", name, path)
			}
		}
	}

	// Don't check typ.Results. If T has a Lock field it's OK to write
	//     return T{}
	// because that is returning the zero value. Leave result checking
	// to the return statement.
}

// checkCopyLocksRange checks whether a range statement
// might inadvertently copy a lock by checking whether
// any of the range variables are locks.
func checkCopyLocksRange(unit *analysis.Unit, r *ast.RangeStmt) {
	checkCopyLocksRangeVar(unit, r.Tok, r.Key)
	checkCopyLocksRangeVar(unit, r.Tok, r.Value)
}

func checkCopyLocksRangeVar(unit *analysis.Unit, rtok token.Token, e ast.Expr) {
	if e == nil {
		return
	}
	id, isId := e.(*ast.Ident)
	if isId && id.Name == "_" {
		return
	}

	var typ types.Type
	if rtok == token.DEFINE {
		if !isId {
			return
		}
		obj := unit.Info.Defs[id]
		if obj == nil {
			return
		}
		typ = obj.Type()
	} else {
		typ = unit.Info.Types[e].Type
	}

	if typ == nil {
		return
	}
	if path := lockPath(unit.Pkg, typ); path != nil {
		unit.Findingf(e.Pos(), "range var %s copies lock: %v", gofmt(unit, e), path)
	}
}

type typePath []types.Type

// String pretty-prints a typePath.
func (path typePath) String() string {
	n := len(path)
	var buf bytes.Buffer
	for i := range path {
		if i > 0 {
			fmt.Fprint(&buf, " contains ")
		}
		// The human-readable path is in reverse order, outermost to innermost.
		fmt.Fprint(&buf, path[n-i-1].String())
	}
	return buf.String()
}

func lockPathRhs(unit *analysis.Unit, x ast.Expr) typePath {
	if _, ok := x.(*ast.CompositeLit); ok {
		return nil
	}
	if _, ok := x.(*ast.CallExpr); ok {
		// A call may return a zero value.
		return nil
	}
	if star, ok := x.(*ast.StarExpr); ok {
		if _, ok := star.X.(*ast.CallExpr); ok {
			// A call may return a pointer to a zero value.
			return nil
		}
	}
	return lockPath(unit.Pkg, unit.Info.Types[x].Type)
}

// lockPath returns a typePath describing the location of a lock value
// contained in typ. If there is no contained lock, it returns nil.
func lockPath(tpkg *types.Package, typ types.Type) typePath {
	if typ == nil {
		return nil
	}

	for {
		atyp, ok := typ.Underlying().(*types.Array)
		if !ok {
			break
		}
		typ = atyp.Elem()
	}

	// We're only interested in the case in which the underlying
	// type is a struct. (Interfaces and pointers are safe to copy.)
	styp, ok := typ.Underlying().(*types.Struct)
	if !ok {
		return nil
	}

	// We're looking for cases in which a pointer to this type
	// is a sync.Locker, but a value is not. This differentiates
	// embedded interfaces from embedded values.
	if types.Implements(types.NewPointer(typ), lockerType) && !types.Implements(typ, lockerType) {
		return []types.Type{typ}
	}

	nfields := styp.NumFields()
	for i := 0; i < nfields; i++ {
		ftyp := styp.Field(i).Type()
		subpath := lockPath(tpkg, ftyp)
		if subpath != nil {
			return append(subpath, typ)
		}
	}

	return nil
}

var lockerType *types.Interface

// Construct a sync.Locker interface type.
func init() {
	nullary := types.NewSignature(nil, nil, nil, false) // func()
	methods := []*types.Func{
		types.NewFunc(token.NoPos, nil, "Lock", nullary),
		types.NewFunc(token.NoPos, nil, "Unlock", nullary),
	}
	lockerType = types.NewInterface(methods, nil).Complete()
}

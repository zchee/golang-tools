// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Check for invalid cgo pointer passing.
// This looks for code that uses cgo to call C code passing values
// whose types are almost always invalid according to the cgo pointer
// sharing rules.
// Specifically, it warns about attempts to pass a Go chan, map, func,
// or slice to C, either directly, or via a pointer, array, or struct.

package vet

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis/plugin/inspect"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/inspector"
)

var CgoCallAnalysis = &analysis.Analysis{
	Name:     "cgocall",
	Doc:      "check for types that may not be passed to cgo calls",
	Requires: []*analysis.Analysis{inspect.Analysis},
	Run:      runCgoCall,
}

func runCgoCall(unit *analysis.Unit) error {
	inspect := unit.Inputs[inspect.Analysis].(*inspector.Inspector)

	nodeTypes := []ast.Node{
		(*ast.CallExpr)(nil),
	}
	inspect.Types(nodeTypes, func(n ast.Node, push bool) bool {
		if !push {
			return true
		}
		x := n.(*ast.CallExpr)

		// We are only looking for calls to functions imported from
		// the "C" package.
		sel, ok := x.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}

		pkgname, ok := unit.Info.Uses[id].(*types.PkgName)
		if !ok || pkgname.Imported().Path() != "C" {
			return true
		}

		// A call to C.CBytes passes a pointer but is always safe.
		if sel.Sel.Name == "CBytes" {
			return true
		}

		for _, arg := range x.Args {
			if !typeOKForCgoCall(cgoBaseType(unit.Info, arg), make(map[types.Type]bool)) {
				unit.Findingf(arg.Pos(), "possibly passing Go type with embedded pointer to C")
			}

			// Check for passing the address of a bad type.
			if conv, ok := arg.(*ast.CallExpr); ok && len(conv.Args) == 1 && hasBasicType(unit.Info, conv.Fun, types.UnsafePointer) {
				arg = conv.Args[0]
			}
			if u, ok := arg.(*ast.UnaryExpr); ok && u.Op == token.AND {
				if !typeOKForCgoCall(cgoBaseType(unit.Info, u.X), make(map[types.Type]bool)) {
					unit.Findingf(arg.Pos(), "possibly passing Go type with embedded pointer to C")
				}
			}
		}
		return true
	})
	return nil
}

// cgoBaseType tries to look through type conversions involving
// unsafe.Pointer to find the real type. It converts:
//   unsafe.Pointer(x) => x
//   *(*unsafe.Pointer)(unsafe.Pointer(&x)) => x
func cgoBaseType(info *types.Info, arg ast.Expr) types.Type {
	switch arg := arg.(type) {
	case *ast.CallExpr:
		if len(arg.Args) == 1 && hasBasicType(info, arg.Fun, types.UnsafePointer) {
			return cgoBaseType(info, arg.Args[0])
		}
	case *ast.StarExpr:
		call, ok := arg.X.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			break
		}
		// Here arg is *f(v).
		t := info.Types[call.Fun].Type
		if t == nil {
			break
		}
		ptr, ok := t.Underlying().(*types.Pointer)
		if !ok {
			break
		}
		// Here arg is *(*p)(v)
		elem, ok := ptr.Elem().Underlying().(*types.Basic)
		if !ok || elem.Kind() != types.UnsafePointer {
			break
		}
		// Here arg is *(*unsafe.Pointer)(v)
		call, ok = call.Args[0].(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			break
		}
		// Here arg is *(*unsafe.Pointer)(f(v))
		if !hasBasicType(info, call.Fun, types.UnsafePointer) {
			break
		}
		// Here arg is *(*unsafe.Pointer)(unsafe.Pointer(v))
		u, ok := call.Args[0].(*ast.UnaryExpr)
		if !ok || u.Op != token.AND {
			break
		}
		// Here arg is *(*unsafe.Pointer)(unsafe.Pointer(&v))
		return cgoBaseType(info, u.X)
	}

	return info.Types[arg].Type
}

// typeOKForCgoCall reports whether the type of arg is OK to pass to a
// C function using cgo. This is not true for Go types with embedded
// pointers. m is used to avoid infinite recursion on recursive types.
func typeOKForCgoCall(t types.Type, m map[types.Type]bool) bool {
	if t == nil || m[t] {
		return true
	}
	m[t] = true
	switch t := t.Underlying().(type) {
	case *types.Chan, *types.Map, *types.Signature, *types.Slice:
		return false
	case *types.Pointer:
		return typeOKForCgoCall(t.Elem(), m)
	case *types.Array:
		return typeOKForCgoCall(t.Elem(), m)
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			if !typeOKForCgoCall(t.Field(i).Type(), m) {
				return false
			}
		}
	}
	return true
}

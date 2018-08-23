// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Check for invalid uintptr -> unsafe.Pointer conversions.

package vet

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis/plugin/inspect"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/inspector"
)

var UnsafePointerAnalysis = &analysis.Analysis{
	Name:     "unsafeptr",
	Doc:      "check for misuse of unsafe.Pointer",
	Requires: []*analysis.Analysis{inspect.Analysis},
	Run:      runUnsafePointer,
}

func runUnsafePointer(unit *analysis.Unit) error {
	inspect := unit.Inputs[inspect.Analysis].(*inspector.Inspector)

	nodeTypes := []ast.Node{
		(*ast.CallExpr)(nil),
	}
	inspect.Types(nodeTypes, func(n ast.Node, push bool) bool {
		if !push {
			return true
		}
		x := n.(*ast.CallExpr)
		if len(x.Args) != 1 {
			return true
		}
		if hasBasicType(unit.Info, x.Fun, types.UnsafePointer) &&
			hasBasicType(unit.Info, x.Args[0], types.Uintptr) &&
			!isSafeUintptr(unit.Info, x.Args[0]) {
			unit.Findingf(x.Pos(), "possible misuse of unsafe.Pointer")
		}
		return true
	})
	return nil
}

// isSafeUintptr reports whether x - already known to be a uintptr -
// is safe to convert to unsafe.Pointer. It is safe if x is itself derived
// directly from an unsafe.Pointer via conversion and pointer arithmetic
// or if x is the result of reflect.Value.Pointer or reflect.Value.UnsafeAddr
// or obtained from the Data field of a *reflect.SliceHeader or *reflect.StringHeader.
func isSafeUintptr(info *types.Info, x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.ParenExpr:
		return isSafeUintptr(info, x.X)

	case *ast.SelectorExpr:
		switch x.Sel.Name {
		case "Data":
			// reflect.SliceHeader and reflect.StringHeader are okay,
			// but only if they are pointing at a real slice or string.
			// It's not okay to do:
			//	var x SliceHeader
			//	x.Data = uintptr(unsafe.Pointer(...))
			//	... use x ...
			//	p := unsafe.Pointer(x.Data)
			// because in the middle the garbage collector doesn't
			// see x.Data as a pointer and so x.Data may be dangling
			// by the time we get to the conversion at the end.
			// For now approximate by saying that *Header is okay
			// but Header is not.
			pt, ok := info.Types[x.X].Type.(*types.Pointer)
			if ok {
				t, ok := pt.Elem().(*types.Named)
				if ok && t.Obj().Pkg().Path() == "reflect" {
					switch t.Obj().Name() {
					case "StringHeader", "SliceHeader":
						return true
					}
				}
			}
		}

	case *ast.CallExpr:
		switch len(x.Args) {
		case 0:
			// maybe call to reflect.Value.Pointer or reflect.Value.UnsafeAddr.
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				break
			}
			switch sel.Sel.Name {
			case "Pointer", "UnsafeAddr":
				t, ok := info.Types[sel.X].Type.(*types.Named)
				if ok && t.Obj().Pkg().Path() == "reflect" && t.Obj().Name() == "Value" {
					return true
				}
			}

		case 1:
			// maybe conversion of uintptr to unsafe.Pointer
			return hasBasicType(info, x.Fun, types.Uintptr) &&
				hasBasicType(info, x.Args[0], types.UnsafePointer)
		}

	case *ast.BinaryExpr:
		switch x.Op {
		case token.ADD, token.SUB, token.AND_NOT:
			return isSafeUintptr(info, x.X) && !isSafeUintptr(info, x.Y)
		}
	}
	return false
}

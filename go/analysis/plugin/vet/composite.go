// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vet

// This file contains the test for unkeyed struct literals.

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis/plugin/inspect"
	"golang.org/x/tools/go/analysis/plugin/vet/internal/whitelist"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/inspector"
)

var UnkeyedLiteralAnalysis = &analysis.Analysis{
	Name:     "composites",
	Doc:      "check that composite literals of types from imported packages use field-keyed elements",
	Requires: []*analysis.Analysis{inspect.Analysis},
	Run:      runUnkeyedLiteral,
}

var compositeWhiteList = true

func init() {
	UnkeyedLiteralAnalysis.Flags.BoolVar(&compositeWhiteList, "compositewhitelist", compositeWhiteList, "use composite white list; for testing only")
}

// runUnkeyedLiteral checks if a composite literal is a struct literal with
// unkeyed fields.
func runUnkeyedLiteral(unit *analysis.Unit) error {
	inspect := unit.Inputs[inspect.Analysis].(*inspector.Inspector)

	nodeTypes := []ast.Node{
		(*ast.CompositeLit)(nil),
	}
	inspect.Types(nodeTypes, func(n ast.Node, push bool) bool {
		if !push {
			return true
		}
		cl := n.(*ast.CompositeLit)

		typ := unit.Info.Types[cl].Type
		if typ == nil {
			// cannot determine composite literals' type, skip it
			return true
		}
		typeName := typ.String()
		if compositeWhiteList && whitelist.UnkeyedLiteral[typeName] {
			// skip whitelisted types
			return true
		}
		under := typ.Underlying()
		for {
			ptr, ok := under.(*types.Pointer)
			if !ok {
				break
			}
			under = ptr.Elem().Underlying()
		}
		if _, ok := under.(*types.Struct); !ok {
			// skip non-struct composite literals
			return true
		}
		if isLocalType(unit, typ) {
			// allow unkeyed locally defined composite literal
			return true
		}

		// check if the CompositeLit contains an unkeyed field
		allKeyValue := true
		for _, e := range cl.Elts {
			if _, ok := e.(*ast.KeyValueExpr); !ok {
				allKeyValue = false
				break
			}
		}
		if allKeyValue {
			// all the composite literal fields are keyed
			return true
		}

		unit.Findingf(cl.Pos(), "%s composite literal uses unkeyed fields", typeName)
		return true
	})
	return nil
}

func isLocalType(unit *analysis.Unit, typ types.Type) bool {
	switch x := typ.(type) {
	case *types.Struct:
		// struct literals are local types
		return true
	case *types.Pointer:
		return isLocalType(unit, x.Elem())
	case *types.Named:
		// names in package foo are local to foo_test too
		return strings.TrimSuffix(x.Obj().Pkg().Path(), "_test") == strings.TrimSuffix(unit.Pkg.Path(), "_test")
	}
	return false
}

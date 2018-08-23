// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file defines the check for unused results of calls to certain
// pure functions.

package vet

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis/plugin/inspect"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/inspector"
)

var UnusedResultAnalysis = &analysis.Analysis{
	Name:     "unusedresult",
	Doc:      "check for unused result of calls to functions in -unusedfuncs list and methods in -unusedstringmethods list",
	Requires: []*analysis.Analysis{inspect.Analysis},
	Run:      runUnusedResult,
}

// flags
var (
	unusedFuncs, unusedStringMethods stringSetFlag
)

func init() {
	// TODO: provide a comment syntax to allow users to add their
	// functions to this set using lemmas.
	flag := UnusedResultAnalysis.Flags
	unusedFuncs.Set("errors.New,fmt.Errorf,fmt.Sprintf,fmt.Sprint,sort.Reverse")
	flag.Var(&unusedFuncs, "unusedfuncs",
		"comma-separated list of functions whose results must be used")

	unusedFuncs.Set("Error,String")
	flag.Var(&unusedStringMethods, "unusedstringmethods",
		"comma-separated list of names of methods of type func() string whose results must be used")
}

func runUnusedResult(unit *analysis.Unit) error {
	inspect := unit.Inputs[inspect.Analysis].(*inspector.Inspector)

	nodeTypes := []ast.Node{
		(*ast.ExprStmt)(nil),
	}
	inspect.Types(nodeTypes, func(n ast.Node, push bool) bool {
		if !push {
			return true
		}

		call, ok := unparen(n.(*ast.ExprStmt).X).(*ast.CallExpr)
		if !ok {
			return true // not a call statement
		}
		fun := unparen(call.Fun)

		if unit.Info.Types[fun].IsType() {
			return true // a conversion, not a call
		}

		selector, ok := fun.(*ast.SelectorExpr)
		if !ok {
			return true // neither a method call nor a qualified ident
		}

		sel, ok := unit.Info.Selections[selector]
		if ok && sel.Kind() == types.MethodVal {
			// method (e.g. foo.String())
			obj := sel.Obj().(*types.Func)
			sig := sel.Type().(*types.Signature)
			if types.Identical(sig, sigNoArgsStringResult) {
				if unusedStringMethods[obj.Name()] {
					unit.Findingf(call.Lparen, "result of (%s).%s call not used",
						sig.Recv().Type(), obj.Name())
				}
			}
		} else if !ok {
			// package-qualified function (e.g. fmt.Errorf)
			obj := unit.Info.Uses[selector.Sel]
			if obj, ok := obj.(*types.Func); ok {
				qname := obj.Pkg().Path() + "." + obj.Name()
				if unusedFuncs[qname] {
					unit.Findingf(call.Lparen, "result of %v call not used", qname)
				}
			}
		}
		return true
	})
	return nil
}

// func() string
var sigNoArgsStringResult = types.NewSignature(nil, nil,
	types.NewTuple(types.NewVar(token.NoPos, nil, "", types.Typ[types.String])),
	false)

type stringSetFlag map[string]bool

func (ss *stringSetFlag) String() string {
	var items []string
	for item := range *ss {
		items = append(items, item)
	}
	sort.Strings(items)
	return strings.Join(items, ",")
}

func (ss *stringSetFlag) Set(s string) error {
	m := make(map[string]bool) // clobber previous value
	if s != "" {
		for _, name := range strings.Split(s, ",") {
			if name == "" {
				continue // TODO: report error? proceed?
			}
			m[name] = true
		}
	}
	*ss = m
	return nil
}

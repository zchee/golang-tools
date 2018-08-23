// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package inspect is an analysis that provides an AST inspector
// (golang.org/x/tools/go/ast/inspect.Inspect) for the syntax trees of a
// package. It is only a building block for other analyses.
//
// Example of use in another analysis:
//
// 	func run(unit *analysis.Unit) error {
// 		inspect := unit.Inputs[inspect.Analysis].(*inspector.Inspector)
// 		inspect.Inspect(func(n ast.Node) bool {
// 			...
// 		})
// 		return nil
// 	}
//
package inspect

import (
	"reflect"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/inspector"
)

var Analysis = &analysis.Analysis{
	Name:             "inspect",
	Doc:              "optimize AST traversal for later passes",
	Run:              run,
	RunDespiteErrors: true,
	OutputType:       reflect.TypeOf(new(inspector.Inspector)),
}

func run(unit *analysis.Unit) error {
	unit.Output = inspector.New(unit.Syntax)
	return nil
}

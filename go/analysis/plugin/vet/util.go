// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vet

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// gofmt returns a string representation of the expression.
func gofmt(unit *analysis.Unit, x ast.Expr) string {
	var buf bytes.Buffer // TODO: opt: cache and reuse this?
	printer.Fprint(&buf, unit.Fset, x)
	return buf.String()
}

// hasBasicType reports whether x's type is a types.Basic with the given kind.
func hasBasicType(info *types.Info, x ast.Expr, kind types.BasicKind) bool {
	if t := info.Types[x].Type; t != nil {
		if b, ok := t.Underlying().(*types.Basic); ok && b.Kind() == kind {
			return true
		}
	}
	return false
}

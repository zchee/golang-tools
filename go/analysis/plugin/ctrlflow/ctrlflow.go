// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package ctrlflow is an analysis that provides a syntactic
// control-flow graph (CFG) for the body of a function.
// It records whether a function cannot return,
// By itself, it does not report any findings.
package ctrlflow

// TODO: false positives are possible.
// This function is spuriously marked noReturn:
// 	func f() {
// 		defer func() { recover() }()
//		panic(nil)
//	}
// Please don't write code like that.

import (
	"go/ast"
	"go/types"
	"log"
	"reflect"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/plugin/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/cfg"
	"golang.org/x/tools/go/types/typeutil"
)

var Analysis = &analysis.Analysis{
	Name:             "ctrlflow",
	Doc:              "build a control-flow graph",
	Run:              run,
	RunDespiteErrors: true,
	OutputType:       reflect.TypeOf(new(CFGs)),
	LemmaTypes:       []reflect.Type{reflect.TypeOf(new(noReturn))},
	Requires:         []*analysis.Analysis{inspect.Analysis},
}

// noReturn is a lemma indicating that a function does not return.
type noReturn struct{}

func (*noReturn) IsLemma() {}

// A CFGs is a container of control-flow graphs
// for all the functions of the current package.
type CFGs struct {
	unit *analysis.Unit

	funcDecls map[*types.Func]*declInfo
	funcLits  map[*ast.FuncLit]*litInfo
}

type declInfo struct {
	decl     *ast.FuncDecl
	cfg      *cfg.CFG
	started  bool
	noReturn bool
}

type litInfo struct {
	cfg      *cfg.CFG
	noReturn bool
}

// FuncDecl returns the control-flow graph for a named function.
// It return nil if decl.Body==nil.
func (c *CFGs) FuncDecl(decl *ast.FuncDecl) *cfg.CFG {
	if decl.Body == nil {
		return nil
	}
	fn := c.unit.Info.Defs[decl.Name].(*types.Func)
	return c.funcDecls[fn].cfg
}

// FuncLit returns the control-flow graph for a literal function.
func (c *CFGs) FuncLit(lit *ast.FuncLit) *cfg.CFG {
	return c.funcLits[lit].cfg
}

func run(unit *analysis.Unit) error {
	inspect := unit.Inputs[inspect.Analysis].(*inspector.Inspector)

	// Because CFG construction consumes and produces noReturn
	// lemmas, all CFGs must be built before 'run' returns; we
	// cannot construct them lazily.

	// Pass 1. Map types.Funcs to ast.FuncDecls in this package.
	funcDecls := make(map[*types.Func]*declInfo) // functions and methods
	funcLits := make(map[*ast.FuncLit]*litInfo)

	var decls []*types.Func // keys(funcDecls), in order
	var lits []*ast.FuncLit // keys(funcLits), in order

	nodeTypes := []ast.Node{
		(*ast.FuncDecl)(nil),
		(*ast.FuncLit)(nil),
	}
	inspect.Types(nodeTypes, func(n ast.Node, push bool) bool {
		if push {
			switch n := n.(type) {
			case *ast.FuncDecl:
				fn := unit.Info.Defs[n.Name].(*types.Func)
				funcDecls[fn] = &declInfo{decl: n}
				decls = append(decls, fn)

			case *ast.FuncLit:
				funcLits[n] = new(litInfo)
				lits = append(lits, n)
			}
		}
		return true
	})

	c := &CFGs{
		unit:      unit,
		funcDecls: funcDecls,
		funcLits:  funcLits,
	}

	// Pass 2. Build CFGs.

	// Build CFGs for named functions.
	// Cycles in the static call graph are broken
	// arbitrarily but deterministically.
	// We create noReturn lemmas as discovered.
	for _, fn := range decls {
		c.buildDecl(fn, funcDecls[fn])
	}

	// Build CFGs for literal functions.
	// These aren't relevant to lemmas (since they aren't named)
	// but are required for the CFGs.FuncLit API.
	for _, lit := range lits {
		li := funcLits[lit]
		if li.cfg == nil {
			li.cfg = cfg.New(lit.Body, c.callMayReturn)
			if !hasReachableReturn(unit, li.cfg) {
				li.noReturn = true
			}
			// TODO optionally dump CFG
		}
	}

	// All CFGs are now built.
	unit.Output = c

	return nil
}

// di.cfg may be nil on return.
func (c *CFGs) buildDecl(fn *types.Func, di *declInfo) {
	if di.cfg == nil && di.decl.Body != nil {
		if di.started {
			return // break cycle
		}
		di.started = true
		di.cfg = cfg.New(di.decl.Body, c.callMayReturn)
		if !hasReachableReturn(c.unit, di.cfg) || isPrimitiveNoReturn(fn) {
			di.noReturn = true
			c.unit.SetObjectLemma(fn, &noReturn{})
		}
		if false {
			log.Printf("CFG for %s:\n%s (noreturn=%t)\n", fn, di.cfg.Format(c.unit.Fset), di.noReturn)
		}
	}
}

func (c *CFGs) callMayReturn(call *ast.CallExpr) (r bool) {
	if id, ok := call.Fun.(*ast.Ident); ok && c.unit.Info.Uses[id] == panicBuiltin {
		return false // panic never returns
	}

	fn := typeutil.StaticCallee(c.unit.Info, call)
	if fn == nil {
		return true // not a static call
	}

	// Function or method declared in this package?
	if di, ok := c.funcDecls[fn]; ok {
		c.buildDecl(fn, di)
		return !di.noReturn
	}

	// Not declared in this package.
	// Is there a lemma from another package?
	return !c.unit.ObjectLemma(fn, new(noReturn))
}

var panicBuiltin = types.Universe.Lookup("panic").(*types.Builtin)

func hasReachableReturn(unit *analysis.Unit, g *cfg.CFG) bool {
	for _, b := range g.Blocks {
		if b.Live && b.Return() != nil {
			return true
		}
	}
	return false
}

func isPrimitiveNoReturn(fn *types.Func) bool {
	// Add functions here as the need arises, but don't allocate memory.
	path, name := fn.Pkg().Path(), fn.Name()
	return path == "syscall" && (name == "Exit" || name == "ExitProcess") ||
		path == "runtime" && name == "Goexit"
}

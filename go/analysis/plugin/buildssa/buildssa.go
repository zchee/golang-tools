// Package buildssa is an analysis that constructs the SSA
// representation of an error-free package and returns the set of all
// functions within it. It does not report any findings itself but may
// be used as an input to other analyses.
package buildssa

import (
	"go/ast"
	"go/types"
	"reflect"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"
)

var Analysis = &analysis.Analysis{
	Name:       "buildssa",
	Doc:        "build SSA-form IR for later passes",
	Run:        run,
	OutputType: reflect.TypeOf(new(SSA)),
}

type SSA struct {
	Pkg      *ssa.Package
	SrcFuncs []*ssa.Function
}

func run(unit *analysis.Unit) error {
	// Plundered from ssautil.BuildPackage.

	// We must create a new Program for each Package because the
	// analysis API provides no place to hang a Program shared by
	// all Packages. Consequently, SSA Packages and Functions do not
	// have a canonical representation across an analysis session of
	// multiple packages. This is unlikely to be a problem in
	// practice because the analysis API essentially forces all
	// packages to be analysed independently, so any given call to
	// Analysis.Run on a package will see only SSA objects belonging
	// to a single Program.

	prog := ssa.NewProgram(unit.Fset, ssa.BuilderMode(0))

	// Create SSA packages for all imports.
	// Order is not significant.
	created := make(map[*types.Package]bool)
	var createAll func(pkgs []*types.Package)
	createAll = func(pkgs []*types.Package) {
		for _, p := range pkgs {
			if !created[p] {
				created[p] = true
				prog.CreatePackage(p, nil, nil, true)
				createAll(p.Imports())
			}
		}
	}
	createAll(unit.Pkg.Imports())

	// Create and build the primary package.
	ssapkg := prog.CreatePackage(unit.Pkg, unit.Syntax, unit.Info, false)
	ssapkg.Build()

	// Compute list of source functions, including literals,
	// in source order.
	// TODO: relate to syntax?
	var funcs []*ssa.Function
	for _, f := range unit.Syntax {
		for _, decl := range f.Decls {
			if fdecl, ok := decl.(*ast.FuncDecl); ok {

				// TODO: SSA will not build a Function
				// for a FuncDecl named blank. That's
				// arguably too strict but relaxing it
				// would break uniqueness of names of
				// package members. What to do?
				if fdecl.Name.Name == "_" {
					continue
				}

				// (init functions have distinct Func
				// objects named "init" and distinct
				// ssa.Functions named "init#1", ...)

				fn := unit.Info.Defs[fdecl.Name].(*types.Func)
				if fn == nil {
					panic(fn)
				}

				f := ssapkg.Prog.FuncValue(fn)
				if f == nil {
					panic(fn)
				}

				var addAnons func(f *ssa.Function)
				addAnons = func(f *ssa.Function) {
					funcs = append(funcs, f)
					for _, anon := range f.AnonFuncs {
						addAnons(anon)
					}
				}
				addAnons(f)
			}
		}
	}

	// TODO: what should we do about wrappers?
	// See google3/third_party/golang/analysis/ssaext.SourceFunctions
	// for one approach.

	unit.Output = &SSA{Pkg: ssapkg, SrcFuncs: funcs}

	return nil
}

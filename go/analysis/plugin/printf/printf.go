// Package printf identifies "printf wrappers", that is, functions that
// delegate their last two arguments, through a chain of static calls,
// to fmt.Fprintf, then checks the arguments of all calls to such
// functions. It uses lemmas to communicate the set of wrappers from one
// analysis unit to another.
//
// A function that wants to avail itself of printf checking
// but does not get found by this heuristic (e.g. due to use of
// dynamic calls) can insert a bogus call:
//
//    if false {
//      fmt.Sprintf(format, args...) // for printf-checking tools
//    }
//
package printf

// This file was adapted from the SSA-based google3/go/src/gobugs/printfwrappers.go.
// TODO: harmonize it with vet's printf checker, which has recently become modular.
// TODO: bring across the tests for both.

// TODO: identify interface methods (e.g. testing.TB) that require the same checking.
// Currently, we can see calls to the abstract method, and we know the
// concrete method is a printf wrapper, but we don't connect these facts.

import (
	"go/ast"
	"go/types"
	"log"
	"reflect"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/plugin/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
)

var Analysis = &analysis.Analysis{
	Name:       "printf",
	Doc:        "check consistency of Printf format strings and arguments",
	Run:        run,
	LemmaTypes: []reflect.Type{reflect.TypeOf(new(isWrapper))},
	Requires:   []*analysis.Analysis{inspect.Analysis},
}

// isWrapper is a lemma indicating that a function is a printf wrapper.
// It carries no information besides its existence.
type isWrapper struct{}

func (*isWrapper) IsLemma() {}

func run(unit *analysis.Unit) error {
	// Terms:
	// - A "printf-like function" is one whose last two parameters
	//   are (format string, args ...interface{}).
	// - A "printf delegation" is a static call from one printf-like
	//   function to another that passes along the last two parameters.
	// - A "printf wrapper" is a printf-like function that delegates
	//   all the way to fmt.Fprintf.

	// deleg is an inverted static call graph over printf-like functions:
	// it maps each function to its callers.
	deleg := make(map[*types.Func][]*types.Func)

	// calls is the set of calls to printf-like functions in this package.
	type printfLikeCall struct {
		call   *ast.CallExpr
		callee *types.Func
	}
	var calls []printfLikeCall

	// During the traversal, when there is an enclosing printf-like
	// FuncDecl, these are its (format string, args ...interface{})
	// parameters and its object.
	var formatParam, argsParam *types.Var
	var caller *types.Func

	var stack []bool // is node a FuncDecl?

	// Find all calls to printf-like functions and populate deleg and calls.
	// We ignore FuncLits because we can't easily identify calls to them;
	// we treat them as just more statements of the enclosing FuncDecl.
	inspect := unit.Inputs[inspect.Analysis].(*inspector.Inspector)
	inspect.Inspect(func(n ast.Node) bool {
		if n == nil { // pop
			if stack[len(stack)-1] { // popped a FuncDecl
				formatParam = nil
				argsParam = nil
			}
			stack = stack[:len(stack)-1]
			return true
		}

		if decl, ok := n.(*ast.FuncDecl); ok {
			stack = append(stack, true) // is a FuncDecl
			caller = unit.Info.Defs[decl.Name].(*types.Func)
			callerSig := caller.Type().(*types.Signature)
			formatParam, argsParam = isPrintfLike(callerSig) // may be (nil, nil)
			return true
		}

		stack = append(stack, false) // not a FuncDecl

		if call, ok := n.(*ast.CallExpr); ok { // call
			if callee := typeutil.StaticCallee(unit.Info, call); callee != nil { // static call
				if p, _ := isPrintfLike(callee.Type().(*types.Signature)); p != nil { // to a printf-like function

					// Record this call to a printf-like function.
					// If it turns out to be a wrapper, we'll need to check the call.
					calls = append(calls, printfLikeCall{call, callee})

					// Are we in a printf-like function,
					// and does the call delegate the its last two parameters?
					if formatParam != nil && delegates(unit.Info, callee, call, formatParam, argsParam) {
						if false {
							log.Printf("%s: call from %s to %s(...)",
								unit.Fset.Position(call.Lparen), caller, callee)
						}
						deleg[callee] = append(deleg[callee], caller)
					}
				}
			}
		}
		return true
	})

	// Seed the graph with initial lemmas.
	if unit.Pkg.Path() == "fmt" {
		for _, name := range []string{"Sprintf", "Fprintf"} {
			fn := unit.Pkg.Scope().Lookup(name).(*types.Func)
			unit.SetObjectLemma(fn, new(isWrapper))
		}
	}

	// Propagate "wrapperness" starting with existing lemmas
	// so that later units have the necessary lemmas.
	wrappers := make(map[*types.Func]bool)
	var mark func(fn *types.Func)
	mark = func(fn *types.Func) {
		if !wrappers[fn] {
			wrappers[fn] = true
			if fn.Pkg() == unit.Pkg {
				unit.SetObjectLemma(fn, new(isWrapper))
			}
			for _, caller := range deleg[fn] {
				mark(caller)
			}
		}
	}
	for fn := range deleg {
		if unit.ObjectLemma(fn, new(isWrapper)) {
			mark(fn)
		}
	}

	// wrappers now contains all printf wrappers that were defined
	// or delegated to (by another wrapper) in this package,
	// but it does not contain wrappers that were merely called
	// in this package and there is no way to enumerate them.
	// However, the set of lemmas is complete.
	wrappers = nil

	// Now check all calls to printf wrappers.
	for _, c := range calls {
		call, callee := c.call, c.callee
		if unit.ObjectLemma(callee, new(isWrapper)) {
			if false {
				log.Printf("%s: call to printf wrapper %s", unit.Fset.Position(call.Lparen), callee)
			}
			checkPrintf(unit, call, callee.Name())
		}
	}

	return nil
}

// isPrintfLike reports whether sig is variadic and its
// final two parameters are (format string, args ...interface{}).
// If so, it returns those two parameters.
func isPrintfLike(sig *types.Signature) (_, _ *types.Var) {
	params := sig.Params()
	if sig.Variadic() && params.Len() >= 2 {
		format := params.At(params.Len() - 2)
		args := params.At(params.Len() - 1)
		if format.Type() == types.Typ[types.String] &&
			types.Identical(args.Type(), efaceSlice) {
			return format, args
		}
	}
	return nil, nil
}

var efaceSlice = types.NewSlice(types.NewInterface(nil, nil).Complete())

// delegates reports whether call is a variadic call
// to a printf-like function, passing fparam and
// aparam as the last two arguments.
func delegates(info *types.Info, callee *types.Func, call *ast.CallExpr, fparam, aparam *types.Var) bool {
	if call.Ellipsis.IsValid() {
		if id, ok := call.Args[len(call.Args)-2].(*ast.Ident); ok && info.Uses[id] == fparam {
			if id, ok := call.Args[len(call.Args)-1].(*ast.Ident); ok && info.Uses[id] == aparam {
				return true
			}
		}
	}
	return false
}

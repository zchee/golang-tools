// Package httpheader inspects the control-flow graph of an SSA function
// and reports possible attempts to set the header of an
// http.ResponseWriter after the body may already have been written.
//
// See Go issue 27668.
package httpheader

import (
	"fmt"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/plugin/buildssa"
	"golang.org/x/tools/go/ssa"
)

var Analysis = &analysis.Analysis{
	Name:     "httpheader",
	Doc:      "check header written to http.ResponseWriter before body",
	Run:      run,
	Requires: []*analysis.Analysis{buildssa.Analysis},
}

func run(unit *analysis.Unit) error {
	ssainput := unit.Inputs[buildssa.Analysis].(*buildssa.SSA)

	// Skip the analysis unless the package directly imports net/http.
	// (In theory problems could occur even in packages that depend
	// on net/http only indirectly, but that's quite unlikely.)
	var httpPkg *types.Package
	for _, imp := range unit.Pkg.Imports() {
		if imp.Path() == "net/http" {
			httpPkg = imp
			break
		}
	}
	if httpPkg == nil {
		return nil // doesn't import net/http
	}

	// Find net/http objects.
	responseWriterType := httpPkg.Scope().Lookup("ResponseWriter")
	headerType := httpPkg.Scope().Lookup("Header")
	headerSetMethod, _, _ := types.LookupFieldOrMethod(headerType.Type(), false, nil, "Set")
	if headerSetMethod == nil {
		return fmt.Errorf("internal error: no (net/http.Header).Set method")
	}

	// isWriter reports whether t satisfies io.Writer.
	isWriter := func(t types.Type) bool {
		obj, _, _ := types.LookupFieldOrMethod(t, false, nil, "Write")
		return obj != nil
	}

	for _, fn := range ssainput.SrcFuncs {
		// visit visits reachable blocks of the CFG in dominance
		// order, maintaining a stack of dominating facts.
		//
		// The stack records values of type http.ResponseWriter
		// that were converted to io.Writer. This is taken as
		// a proxy for writing the HTTP response body.
		type fact struct {
			w        ssa.Value
			reported bool
		}

		seen := make([]bool, len(fn.Blocks)) // seen[i] means visit should ignore block i
		var visit func(b *ssa.BasicBlock, stack []fact)
		visit = func(b *ssa.BasicBlock, stack []fact) {
			if seen[b.Index] {
				return
			}
			seen[b.Index] = true

			for _, instr := range b.Instrs {
				switch instr := instr.(type) {
				case *ssa.ChangeInterface:
					// Sadly there's no point recording instr.Pos
					// as the conversion is invariably implicit.
					if types.Identical(instr.X.Type(), responseWriterType.Type()) && isWriter(instr.Type()) {
						stack = append(stack, fact{w: instr.X})
					}

				case *ssa.Call:
					// Call to w.Header().Set()?
					if callee := instr.Common().StaticCallee(); callee != nil && callee.Object() == headerSetMethod {
						hdr := instr.Common().Args[0]
						if headerCall, ok := hdr.(*ssa.Call); ok {
							w := headerCall.Common().Value
							for i, fact := range stack {
								if fact.w == w {
									if !fact.reported { // avoid dups
										stack[i].reported = true
										unit.Findingf(instr.Pos(), "call to w.Header().Set() after response body written")
									}
									break
								}
							}
						}
					}
				}
			}

			for _, d := range b.Dominees() {
				visit(d, stack)
			}
		}

		// Visit the entry block.  No need to visit fn.Recover.
		if fn.Blocks != nil {
			visit(fn.Blocks[0], make([]fact, 0, 20)) // 20 is plenty
		}
	}

	return nil
}

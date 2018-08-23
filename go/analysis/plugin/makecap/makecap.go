// Package makecap warns on usage of append() after make([]T, x).
//
// Only warns if make() is followed by append() and there are no
// indexed assignments (s[i] = ...) to the slice.
package makecap

import (
	"go/types"

	"golang.org/x/tools/go/analysis/plugin/buildssa"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"
)

var Analysis = &analysis.Analysis{
	Name:     "makecap",
	Doc:      "report make([]T, 0, n) followed by append",
	Run:      run,
	Requires: []*analysis.Analysis{buildssa.Analysis},
}

func run(unit *analysis.Unit) error {
	// Find append() after make() pattern for all functions in SSA.
	ssainput := unit.Inputs[buildssa.Analysis].(*buildssa.SSA)
	for _, fn := range ssainput.SrcFuncs {
		analyze(unit, fn)
	}
	return nil
}

func analyze(unit *analysis.Unit, fn *ssa.Function) {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			// The slice in SSA. This is present in φ-node if append() is called on it.
			var slice ssa.Value
			switch v := instr.(type) {
			case *ssa.MakeSlice:
				if v.Len != v.Cap {
					continue // length explicitly specified
				}
				slice = v
			case *ssa.Alloc:
				// make([]T, len, cap) with constant cap is
				// treated as ssa.Slice applied to ssa.Alloc.
				referrers := v.Referrers()
				if len(*referrers) != 1 {
					continue // not a make([]...)
				}

				for _, x := range *referrers {
					if s, ok := x.(*ssa.Slice); ok {
						// Arrays are the only type for which new(T)[:] is valid.
						capacity := v.Type().(*types.Pointer).Elem().(*types.Array).Len()
						if high, ok := s.High.(*ssa.Const); ok && high.Int64() == capacity && capacity > 0 {
							slice = s
							break
						}
					}
				}

				if slice == nil {
					// ssa.Slice not found
					continue
				}
			default:
				continue
			}

			if hasAssignments(slice) {
				// skip slices with assignments
				continue
			}

			// now do a depth first search on all φ referrers of slice and warn if
			// any φ node has append operand.
			for _, referrer := range *slice.Referrers() {
				if phi, ok := referrer.(*ssa.Phi); ok {
					hasAppend := false
					for _, phiRef := range *phi.Referrers() {
						if isAppend(phiRef, phi) {
							hasAppend = true
							break
						}
					}

					if hasAppend {
						unit.Findingf(slice.Pos(),
							"append after make(%[1]s, n); did you mean make(%[1]s, 0, n)?",
							types.TypeString(slice.Type(), (*types.Package).Name))
					}
				}
			}
		}
	}
}

// isAppend reports whether the given Instruction is a call to append(op, ...).
func isAppend(instr ssa.Instruction, op ssa.Value) bool {
	if call, ok := instr.(*ssa.Call); ok {
		if b, ok := call.Common().Value.(*ssa.Builtin); ok {
			return b.Name() == "append" && call.Common().Args[0] == op
		}
	}
	return false
}

// checks if a slice has assignment.
func hasAssignments(slice ssa.Value) bool {
	for _, ref := range *slice.Referrers() {
		if idx, ok := ref.(*ssa.IndexAddr); ok && idx.X == slice {
			return true
		}
	}
	return false
}

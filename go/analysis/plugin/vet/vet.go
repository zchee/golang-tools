package vet

import "golang.org/x/tools/go/analysis"

// Analyses contains the vet suite of checkers.
//
// The 'shadow' analysis is omitted, as it is still experimental.
var Analyses = []*analysis.Analysis{
	AssemblyAnalysis,
	AssignAnalysis,
	AtomicAnalysis,
	BoolAnalysis,
	CanonicalMethodsAnalysis,
	CgoCallAnalysis,
	CopyLocksAnalysis,
	HTTPResponseAnalysis,
	NilFuncAnalysis,
	RangeLoopAnalysis,
	ShiftAnalysis,
	StructTagsAnalysis,
	TestFunctionsAnalysis,
	UnkeyedLiteralAnalysis,
	UnreachableAnalysis,
	UnsafePointerAnalysis,
	UnusedResultAnalysis,
}

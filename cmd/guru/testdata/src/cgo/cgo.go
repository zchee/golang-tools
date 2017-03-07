package cgo

// Tests of five query modes when cgo files are involved.
// See golang.org/x/tools/cmd/guru/guru_test.go for explanation.
// See cgo/definition.golden for expected query results.
//
// A possible shortcoming of these tests is that none of the cgo files
// actually require the C compiler.  The import "C" statement is just
// thrown into this file, the other local file in this directory,
// and the extra file in the libc package, to make the loader recognize
// them as cgo files.  So there are no parsing errors being introduced.
//
// The following guru modes are tested as working in and with cgo files:
// (It can be useful during unit testing to put a panic in front of the
// loader's cgo processing call, to ensure none of these tests cause
// actual cgo processing to be invoked - we just want cgo file parsing.)
//
//	definition
//	describe
//	freevars
//	implements
//	referrers
//
// The remaining guru modes are not tested as doing anything special with cgo
// files; they are expected to incur the normal cgo processing overhead:
//
//	callees
//	callers
//	callstack
//	peers
//	pointsto
//	what
//	whicherrs
//
// A future direction for guru's cgo handling could be to allow the queryPos
// file itself, if it is a cgo file, to be processed by cgo.  Presumably
// someone working directly in a cgo file is not going to mind a few extra
// tenths of a second per guru command if it means their results are more
// accurate.  But this concern may be unfounded - I haven't run into a cgo file
// that required cgo processing for at least the guru definition command.

import (
	"libc"
)

import "C"

func definition_tests() {
	var x libc.T // @definition cgo-definition-lexical-pkgname "libc"
	print(x)     // @definition cgo-definition-lexical-var "x"

	var _ libc.Type  // @definition cgo-definition-qualified-type "Type"
	var _ libc.Const // @definition cgo-definition-qualified-const "Const"

	var u U        // @definition cgo-definition-local-type "U"
	print(u.field) // @definition cgo-definition-select-field "field"
	u.method()     // @definition cgo-definition-select-method "method"

	var _ W // @definition cgo-definition-other-local-file "W"

	cs := libc.Cfoo() // @definition cgo-definition-other-cgo-pkg "Cfoo"
	ct := cs.Method() // @definition cgo-definition-other-cgo-pkg-method-level1 "Method"
	ct.Method()       // @definition cgo-definition-other-cgo-pkg-method-level2 "Method"
}

func describe_tests() {
	var _ U // @describe cgo-describe-local-type "U"
	var _ W // @describe cgo-describe-other-local-file "W"

	cs := libc.Cfoo() // @describe cgo-describe-other-cgo-pkg "Cfoo"
	ct := cs.Method() // @describe cgo-describe-other-cgo-pkg-method-level1 "Method"
	ct.Method()       // @describe cgo-describe-other-cgo-pkg-method-level2 "Method"
	_ = cs            // @describe cgo-describe-other-cgo-pkg-type "cs"
	_ = ct            // @describe cgo-describe-other-cgo-pkg-type2 "ct"
}

func freevars_tests() {
	type C int
	x := 1
	const exp = 6
	if y := 2; x+y+int(C(3)) != exp { // @freevars cgo-fv1 "if.*{"
		panic("expected 6")
	}
}

type T struct{ field int }

func (T) method()

type U struct{ T }

// implements tests

type F interface { // @implements cgo-F "F"
	f()
}

type FG interface { // @implements cgo-FG "FG"
	f()
	g() []int // @implements cgo-slice "..int"
}

type CC int // @implements cgo-CC "CC"
type D struct{}

func (c *CC) f() {} // @implements cgo-starCC ".CC"
func (d D) f()   {} // @implements cgo-D "D"

func (d *D) g() []int { return nil } // @implements cgo-starD ".D"

type I interface { // @implements cgo-I "I"
	Method(*int) *int
}

// referrers

type s struct { // @referrers cgo-type " s "
	f int
}

type T int

func referrs_tests() {
	var _ W // @referrers cgo-ref-other-local-file "W"

	cs := libc.Cfoo()
	// Two referrers tests that seem too long to be worth it, each can be about .3s
	ct := cs.Method() // toolong... cgo-ref-other-cgo-pkg-method-level1 "Method"
	ct.Method()       // toolong... cgo-ref-other-cgo-pkg-method-level2 "Method"

	var v libc.Type = libc.Const // @referrers cgo-ref-package "libc"
	_ = v.Method                 // @referrers cgo-ref-method "Method"
	_ = v.Method
	v++ //@referrers cgo-ref-local "v"
	v++

	_ = s{}.f // @referrers cgo-ref-field "f"

	var s2 s
	s2.f = 1
}

// Test //line directives:

type V int // @referrers cgo-ref-type-V "V"

var u1 V

//line nosuchfile.y:123
var u2 V

// The analyze command is a static checker for Go programs, similar to
// vet, but with pluggable analyses defined using the analysis
// interface, and using the go/packages API to load packages in any
// build system.
//
// Each analysis flag name is preceded by the analysis name: --analysis.flag.
// In addition, the --analysis.enabled flag controls whether the
// findings of that analysis are displayed. (A disabled analysis may yet
// be run if it is required by some other analysis that is enabled.)
package main

import (
	"log"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/multichecker"

	// analysis plug-ins
	"golang.org/x/tools/go/analysis/plugin/deadcode"
	"golang.org/x/tools/go/analysis/plugin/findcall"
	"golang.org/x/tools/go/analysis/plugin/httpheader"
	"golang.org/x/tools/go/analysis/plugin/lostcancel"
	"golang.org/x/tools/go/analysis/plugin/makecap"
	"golang.org/x/tools/go/analysis/plugin/nilness"
	"golang.org/x/tools/go/analysis/plugin/printf"
	"golang.org/x/tools/go/analysis/plugin/vet"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("analyze: ")

	multichecker.Run(append([]*analysis.Analysis{
		deadcode.Analysis,
		findcall.Analysis,
		lostcancel.Analysis,
		makecap.Analysis,
		nilness.Analysis,
		printf.Analysis,
		httpheader.Analysis,
	}, vet.Analyses...)...)
}

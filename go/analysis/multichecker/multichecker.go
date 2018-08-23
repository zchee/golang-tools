// Package multichecker defines the main function for an analysis driver
// with several analyses. This package makes it easy for anyone to build
// an analysis tool containing just the analyses they need.
package multichecker

import (
	"flag"
	"log"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/internal/checker"
)

func Run(analyses ...*analysis.Analysis) {
	if err := analysis.Validate(analyses); err != nil {
		log.Fatal(err)
	}

	checker.RegisterFlags()

	// Connect each analysis flag to the command line as --analysis.flag.
	enabled := make(map[*analysis.Analysis]*bool)
	for _, a := range analyses {
		prefix := a.Name + "."

		// Add --foo.enable flag.
		enable := new(bool)
		flag.BoolVar(enable, prefix+"enable", false, "enable only "+a.Name+" analysis")
		enabled[a] = enable

		a.Flags.VisitAll(func(f *flag.Flag) {
			flag.Var(f.Value, prefix+f.Name, f.Usage)
		})
	}

	flag.Parse() // (ExitOnError)

	// If any --foo.enable flag is set,
	// run only those analyses.
	var keep []*analysis.Analysis
	for _, a := range analyses {
		if *enabled[a] {
			keep = append(keep, a)
		}
	}
	if keep != nil {
		analyses = keep
	}

	if err := checker.Run(flag.Args(), analyses); err != nil {
		log.Fatal(err)
	}
}

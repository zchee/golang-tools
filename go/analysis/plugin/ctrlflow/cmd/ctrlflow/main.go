// The ctrlflow command applies the golang.org/x/tools/go/analysis/plugin/ctrlflow
// analysis to the specified packages of Go source code.
package main

import (
	"golang.org/x/tools/go/analysis/plugin/ctrlflow"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(ctrlflow.Analysis) }

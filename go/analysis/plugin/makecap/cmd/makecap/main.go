// The makecap command applies the golang.org/x/tools/go/analysis/plugin/makecap
// analysis to the specified packages of Go source code.
package main

import (
	"golang.org/x/tools/go/analysis/plugin/makecap"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(makecap.Analysis) }

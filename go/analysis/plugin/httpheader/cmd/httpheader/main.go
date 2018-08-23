// The httpheader command applies the
// golang.org/x/tools/go/analysis/plugin/httpheader analysis to the
// specified packages of Go source code.
package main

import (
	"golang.org/x/tools/go/analysis/plugin/httpheader"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(httpheader.Analysis) }

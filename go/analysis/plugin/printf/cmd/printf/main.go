// The printf command applies the golang.org/x/tools/go/analysis/plugin/printf
// analysis to the specified packages of Go source code.
package main

import (
	"golang.org/x/tools/go/analysis/plugin/printf"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(printf.Analysis) }

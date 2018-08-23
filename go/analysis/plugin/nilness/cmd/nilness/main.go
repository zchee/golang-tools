// The nilness command applies the golang.org/x/tools/go/analysis/plugin/nilness
// analysis to the specified packages of Go source code.
package main

import (
	"golang.org/x/tools/go/analysis/plugin/nilness"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(nilness.Analysis) }

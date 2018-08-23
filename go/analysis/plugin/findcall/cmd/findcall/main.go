// The findcall command applies the golang.org/x/tools/go/analysis/plugin/findcall
// analysis to the specified packages of Go source code.
package main

import (
	"golang.org/x/tools/go/analysis/plugin/findcall"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(findcall.Analysis) }

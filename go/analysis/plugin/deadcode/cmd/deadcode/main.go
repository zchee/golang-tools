// The deadcode command runs the golang.org/x/tools/go/analysis/plugin/deadcode
// analysis, which reports dead code.
package main

import (
	"golang.org/x/tools/go/analysis/plugin/deadcode"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() { singlechecker.Main(deadcode.Analysis) }

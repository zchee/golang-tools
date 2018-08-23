package pkglemma_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/analysis/plugin/pkglemma"
)

func Test(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, pkglemma.Analysis,
		"c", // loads testdata/src/c/c.go.
	)
}

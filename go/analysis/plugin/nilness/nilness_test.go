package nilness_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/plugin/nilness"
	"golang.org/x/tools/go/analysis/analysistest"
)

func Test(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, nilness.Analysis,
		"a", // loads testdata/src/a/a.go.
	)
}

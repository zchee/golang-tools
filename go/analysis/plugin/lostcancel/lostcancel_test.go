package lostcancel_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/plugin/lostcancel"
	"golang.org/x/tools/go/analysis/analysistest"
)

func Test(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, lostcancel.Analysis,
		"a", // loads testdata/src/a/a.go.
	)
}

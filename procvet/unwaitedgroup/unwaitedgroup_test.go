package unwaitedgroup

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestUnwaitedGroup(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "example.test/example")
}

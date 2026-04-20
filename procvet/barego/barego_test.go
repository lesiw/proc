package barego

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestBareGo(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "example.test/example")
}

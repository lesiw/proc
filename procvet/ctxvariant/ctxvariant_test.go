package ctxvariant

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestCtxVariant(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.RunWithSuggestedFixes(t, testdata, Analyzer, "example")
}

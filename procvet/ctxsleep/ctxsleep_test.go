package ctxsleep

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestCtxSleep(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "example")
}

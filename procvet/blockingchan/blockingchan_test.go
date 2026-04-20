package blockingchan

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestBlockingChan(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "example")
}

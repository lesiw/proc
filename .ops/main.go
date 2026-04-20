package main

import (
	"os"

	"labs.lesiw.io/ops/golang"
	"labs.lesiw.io/ops/golib"

	"lesiw.io/ops"
)

type Ops struct{ golib.Ops }

func main() {
	// vet/ is an aggregator module that uses replace directives
	// to reference sibling modules in this repo. The replace
	// check in op doesn't distinguish intra-repo from cross-repo
	// replaces, so opt out.
	golang.GoModReplaceAllowed = true
	if len(os.Args) < 2 {
		os.Args = append(os.Args, "check")
	}
	ops.Handle(Ops{})
}

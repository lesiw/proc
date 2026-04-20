// Package ctxsleep defines an analyzer that reports calls to [time.Sleep]
// inside functions that take a [context.Context] parameter.
//
// time.Sleep does not honor ctx cancellation: a ctx-aware body that calls
// time.Sleep(d) will ignore an external cancel for the full duration d. The
// idiomatic replacement is a select that races the ctx against a timer:
//
//	select {
//	case <-ctx.Done():
//		return ctx.Err()
//	case <-time.After(d):
//	}
//
// The analyzer flags any time.Sleep inside a function whose signature includes
// a parameter of type context.Context. Functions that don't take a ctx are
// ignored.
package ctxsleep

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"

	"lesiw.io/proc/procvet/internal/check"
)

// Analyzer reports time.Sleep inside functions taking a context.Context
// parameter.
var Analyzer = &analysis.Analyzer{
	Name: "ctxsleep",
	Doc:  "report time.Sleep inside functions that take " + "context.Context",
	Run:  run,
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		checkFile(pass, file)
	}
	return nil, nil
}

func checkFile(pass *analysis.Pass, file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body != nil && check.TakesCtx(pass, fn.Type) {
				checkBody(pass, fn.Body)
			}
		case *ast.FuncLit:
			if check.TakesCtx(pass, fn.Type) {
				checkBody(pass, fn.Body)
			}
		}
		return true
	})
}

// checkBody walks body and flags time.Sleep calls. Nested function literals
// are skipped — the outer Inspect visits them separately and decides whether
// to check their body.
func checkBody(pass *analysis.Pass, body *ast.BlockStmt) {
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isTimeSleep(pass, call) {
			pass.Reportf(
				call.Pos(),
				"time.Sleep inside a ctx-aware body ignores "+
					"ctx cancellation; use a select with "+
					"ctx.Done and time.After instead",
			)
		}
		return true
	})
}

// isTimeSleep reports whether call is a call to time.Sleep.
func isTimeSleep(pass *analysis.Pass, call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel.Name != "Sleep" {
		return false
	}
	obj, ok := pass.TypesInfo.Uses[sel.Sel].(*types.Func)
	if !ok {
		return false
	}
	pkg := obj.Pkg()
	if pkg == nil {
		return false
	}
	return pkg.Path() == "time"
}

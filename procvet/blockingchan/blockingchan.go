// Package blockingchan defines an analyzer that reports bare channel send and
// receive operations inside functions that take a [context.Context] parameter.
//
// A bare ch <- v or <-ch outside a select will block the goroutine
// indefinitely if the other side isn't ready. In a ctx-aware body, blocking
// operations should be wrapped in a select that also cases on ctx.Done so the
// goroutine can unwind on cancellation:
//
//	select {
//	case ch <- v:
//	case <-ctx.Done():
//		return ctx.Err()
//	}
//
// The analyzer flags send and receive statements (not expressions inside a
// select) in functions whose signature has a context.Context parameter. It
// skips:
//
//   - Operations already inside a select statement.
package blockingchan

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"

	"lesiw.io/proc/procvet/internal/check"
)

// Analyzer reports bare channel operations in functions that take
// context.Context.
var Analyzer = &analysis.Analyzer{
	Name: "blockingchan",
	Doc: "report channel send/receive outside a select in " +
		"functions that take context.Context",
	Run: run,
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

// checkBody walks body looking for bare channel sends and receives. It tracks
// select nesting so operations inside a select case are not flagged.
func checkBody(pass *analysis.Pass, body *ast.BlockStmt) {
	var inSelect int
	var inspect func(ast.Node) bool
	inspect = func(n ast.Node) bool {
		if n == nil {
			return false
		}
		switch node := n.(type) {
		case *ast.FuncLit:
			return false

		case *ast.SelectStmt:
			inSelect++
			ast.Inspect(node.Body, inspect)
			inSelect--
			return false

		case *ast.RangeStmt:
			if isChanType(pass, node.X) {
				pass.Reportf(
					node.Pos(),
					"for-range over a channel blocks "+
						"until the channel is closed; "+
						"use select with ctx.Done to "+
						"observe cancellation",
				)
			}
			return true

		case *ast.SendStmt:
			if inSelect > 0 {
				return true
			}
			if isChanType(pass, node.Chan) {
				pass.Reportf(
					node.Pos(),
					"bare channel send in ctx-aware "+
						"body may block without honoring "+
						"ctx cancellation; wrap in a "+
						"select with ctx.Done",
				)
			}
			return true

		case *ast.ExprStmt:
			// A bare <-ch as a statement (value discarded).
			if inSelect > 0 {
				return true
			}
			unary, ok := node.X.(*ast.UnaryExpr)
			if !ok {
				return true
			}
			if unary.Op.String() != "<-" {
				return true
			}
			if isChanType(pass, unary.X) {
				pass.Reportf(
					node.Pos(),
					"bare channel receive in ctx-aware "+
						"body may block without honoring "+
						"ctx cancellation; wrap in a "+
						"select with ctx.Done",
				)
			}
			return true

		case *ast.AssignStmt:
			// v := <-ch or v = <-ch
			if inSelect > 0 {
				return true
			}
			for _, rhs := range node.Rhs {
				unary, ok := rhs.(*ast.UnaryExpr)
				if !ok {
					continue
				}
				if unary.Op.String() != "<-" {
					continue
				}
				if isChanType(pass, unary.X) {
					pass.Reportf(
						unary.Pos(),
						"bare channel receive in "+
							"ctx-aware body may block "+
							"without honoring ctx "+
							"cancellation; wrap in a "+
							"select with ctx.Done",
					)
				}
			}
			return true
		}
		return true
	}
	ast.Inspect(body, inspect)
}

// isChanType reports whether expr is a channel type.
func isChanType(pass *analysis.Pass, expr ast.Expr) bool {
	tv, ok := pass.TypesInfo.Types[expr]
	if !ok {
		return false
	}
	_, isChan := tv.Type.Underlying().(*types.Chan)
	return isChan
}

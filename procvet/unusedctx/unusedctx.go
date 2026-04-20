// Package unusedctx defines an analyzer that reports functions whose signature
// includes a [context.Context] parameter that is never referenced in the body.
//
// A ctx parameter that nothing reads is lying about cancellability: the caller
// believes the function honors cancellation, but the function body ignores ctx
// entirely, so an external cancel has no effect. The fix is either to actually
// use ctx (honor ctx.Done, pass it to downstream calls) or to drop the
// parameter and be honest that the function is uncancellable.
//
// The analyzer flags any function (including methods and function literals)
// whose signature has a named ctx context.Context parameter that is never read
// in the body. A parameter named "_" is an explicit opt-out and is ignored.
package unusedctx

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"

	"lesiw.io/proc/procvet/internal/check"
)

// Analyzer reports ctx parameters that are never referenced in the function
// body.
var Analyzer = &analysis.Analyzer{
	Name: "unusedctx",
	Doc: "report context.Context parameters that are " +
		"never referenced in the function body",
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
			if fn.Body != nil {
				checkFunc(pass, fn.Type, fn.Body)
			}
		case *ast.FuncLit:
			checkFunc(pass, fn.Type, fn.Body)
		}
		return true
	})
}

// checkFunc finds each named ctx.Context parameter in fnType and reports it if
// the body never references it.
func checkFunc(
	pass *analysis.Pass, fnType *ast.FuncType, body *ast.BlockStmt,
) {
	if fnType.Params == nil {
		return
	}
	for _, field := range fnType.Params.List {
		if !check.IsContextType(pass, field.Type) {
			continue
		}
		for _, name := range field.Names {
			if name.Name == "_" {
				continue
			}
			obj := pass.TypesInfo.Defs[name]
			if obj == nil {
				continue
			}
			if !referenced(pass, body, obj) {
				pass.Reportf(
					name.Pos(),
					"context.Context parameter %q is "+
						"never referenced; either use "+
						"it or rename to _",
					name.Name,
				)
			}
		}
	}
}

// referenced reports whether obj is used anywhere inside body.
func referenced(
	pass *analysis.Pass, body *ast.BlockStmt, obj types.Object,
) bool {
	var found bool
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if pass.TypesInfo.Uses[id] == obj {
			found = true
			return false
		}
		return true
	})
	return found
}

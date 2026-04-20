// Package check provides shared type-checking predicates for the
// procvet analyzers.
package check

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// ProcPkg is the import path of the proc package.
const ProcPkg = "lesiw.io/proc"

// TakesCtx reports whether fn has a context.Context parameter.
func TakesCtx(pass *analysis.Pass, fn *ast.FuncType) bool {
	if fn.Params == nil {
		return false
	}
	for _, field := range fn.Params.List {
		if IsContextType(pass, field.Type) {
			return true
		}
	}
	return false
}

// IsContextType reports whether expr denotes context.Context.
func IsContextType(pass *analysis.Pass, expr ast.Expr) bool {
	tv, ok := pass.TypesInfo.Types[expr]
	if !ok {
		return false
	}
	named, ok := tv.Type.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Pkg().Path() == "context" && obj.Name() == "Context"
}

// ImportsProc reports whether pkg imports lesiw.io/proc.
func ImportsProc(pkg *types.Package) bool {
	for _, imp := range pkg.Imports() {
		if imp.Path() == ProcPkg {
			return true
		}
	}
	return false
}

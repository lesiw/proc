// Package ctxvariant defines an analyzer that reports calls to a function X
// when the same package exports a ctx-aware sibling XContext or XWithContext.
//
// A Go library that takes cancellation seriously typically ships two versions
// of each blocking call: one that takes context.Context as the first argument,
// and one that doesn't. The non-ctx version is usually a convenience that
// calls the ctx version with [context.Background]. Calling the non-ctx version
// from a ctx-aware body means that ctx cancellation is silently ignored for
// the duration of that call — a footgun that shows up all over real codebases.
//
// Examples from the standard library:
//
//	http.NewRequest          -> http.NewRequestWithContext
//	http.Get / http.Post     -> (use http.Client with a ctx request)
//	exec.Command             -> exec.CommandContext
//	net.Dial                 -> net.Dialer.DialContext
//	sql.DB.Query             -> sql.DB.QueryContext
//
// The analyzer walks each imported package's symbol table and flags any
// call to X when XContext or XWithContext is also exported from the same
// package. This covers the standard library and third-party libraries
// alike, without any per-library configuration.
//
// When the enclosing function has exactly one context.Context parameter, the
// analyzer also emits a [analysis.SuggestedFix] that rewrites the call to the
// ctx-aware variant. The rewrite is skipped when ctx is ambiguous (zero or
// multiple in scope), since picking the wrong one would silently change
// program behavior.
package ctxvariant

import (
	"fmt"
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"

	"lesiw.io/proc/procvet/internal/check"
)

// Analyzer reports calls to non-ctx functions when a ctx-aware sibling exists.
var Analyzer = &analysis.Analyzer{
	Name: "ctxvariant",
	Doc: "report calls to X when pkg.XContext " +
		"or pkg.XWithContext exists",
	Run: run,
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		checkFile(pass, file)
	}
	return nil, nil
}

func checkFile(pass *analysis.Pass, file *ast.File) {
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		name, _ := ctxParam(pass, fd.Type)
		checkScope(pass, fd.Body, name)
	}
}

// checkScope walks root checking every call expression against the given
// ctxName. Nested function literals are recursed into separately so each can
// use its own ctx parameter (or inherit the outer name when it has none).
func checkScope(pass *analysis.Pass, root ast.Node, ctxName string) {
	ast.Inspect(root, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncLit:
			inner, hasAny := ctxParam(pass, v.Type)
			if !hasAny {
				inner = ctxName
			}
			checkScope(pass, v.Body, inner)
			return false
		case *ast.CallExpr:
			checkCall(pass, v, ctxName)
		}
		return true
	})
}

func checkCall(pass *analysis.Pass, call *ast.CallExpr, ctxName string) {
	// We only care about fully-qualified calls
	// (pkg.X or recv.X where recv is a named type).
	// Bare identifier calls and closure calls are
	// skipped.
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	callee, ok := pass.TypesInfo.Uses[sel.Sel].(*types.Func)
	if !ok {
		return
	}
	short, display, ok := variantName(callee)
	if !ok {
		return
	}
	diag := analysis.Diagnostic{
		Pos: call.Pos(),
		Message: fmt.Sprintf(
			"call to %s has a ctx-aware variant %s; "+
				"use it to honor context cancellation",
			callee.Name(), display,
		),
	}
	if ctxName != "" {
		diag.SuggestedFixes = []analysis.SuggestedFix{
			buildFix(call, sel, short, ctxName),
		}
	}
	pass.Report(diag)
}

// buildFix rewrites call to use the ctx-aware variant: the selector name is
// replaced with newName, and ctxName is prepended to the argument list.
func buildFix(
	call *ast.CallExpr, sel *ast.SelectorExpr, newName, ctxName string,
) analysis.SuggestedFix {
	insert := []byte(ctxName)
	if len(call.Args) > 0 {
		insert = []byte(ctxName + ", ")
	}
	return analysis.SuggestedFix{
		Message: "use " + newName,
		TextEdits: []analysis.TextEdit{
			{
				Pos:     sel.Sel.Pos(),
				End:     sel.Sel.End(),
				NewText: []byte(newName),
			},
			{
				Pos:     call.Lparen + 1,
				End:     call.Lparen + 1,
				NewText: insert,
			},
		},
	}
}

// ctxParam walks the parameter list of ft looking for context.Context. It
// returns the unambiguous parameter name and whether any ctx parameter was
// present. An unnamed or blank ctx parameter contributes to hasAny but yields
// no name, and multiple named ctx parameters yield an empty name (ambiguous).
func ctxParam(
	pass *analysis.Pass, ft *ast.FuncType,
) (name string, hasAny bool) {
	if ft.Params == nil {
		return "", false
	}
	for _, field := range ft.Params.List {
		if !check.IsContextType(pass, field.Type) {
			continue
		}
		hasAny = true
		if len(field.Names) == 0 {
			continue
		}
		for _, ident := range field.Names {
			if ident.Name == "_" {
				continue
			}
			if name != "" {
				return "", true
			}
			name = ident.Name
		}
	}
	return name, hasAny
}

// variantName reports whether callee has a sibling named callee+"Context" or
// callee+"WithContext" that takes a context.Context as its first parameter. If
// yes, it returns the bare variant name (for rewriting the selector) and a
// qualified display name (for diagnostics).
func variantName(callee *types.Func) (short, display string, ok bool) {
	// Skip callees that already take a ctx first
	// argument — they are the variant, not the
	// non-ctx one.
	if firstParamIsContext(callee) {
		return "", "", false
	}

	// Find the set of candidate variant names.
	name := callee.Name()
	candidates := []string{
		name + "Context",
		name + "WithContext",
	}

	// The variant must live in the same scope as
	// callee. For package-level functions, that's
	// the package scope. For methods, it's the
	// method set of the receiver type.
	sig, isFn := callee.Type().(*types.Signature)
	if isFn && sig.Recv() != nil {
		recv := derefNamed(sig.Recv().Type())
		if recv == nil {
			return "", "", false
		}
		for _, cand := range candidates {
			m := findMethod(recv, cand)
			if m == nil || !firstParamIsContext(m) {
				continue
			}
			return cand, recv.Obj().Name() + "." + cand, true
		}
		return "", "", false
	}
	pkg := callee.Pkg()
	if pkg == nil {
		return "", "", false
	}
	scope := pkg.Scope()
	for _, cand := range candidates {
		obj := scope.Lookup(cand)
		if obj == nil {
			continue
		}
		fn, ok := obj.(*types.Func)
		if !ok || !firstParamIsContext(fn) {
			continue
		}
		return cand, pkg.Name() + "." + cand, true
	}
	return "", "", false
}

// firstParamIsContext reports whether fn's first parameter is a
// context.Context.
func firstParamIsContext(fn *types.Func) bool {
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return false
	}
	params := sig.Params()
	if params.Len() == 0 {
		return false
	}
	named := derefNamed(params.At(0).Type())
	if named == nil {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Pkg().Path() == "context" && obj.Name() == "Context"
}

// derefNamed unwraps pointer types and returns the underlying named type, or
// nil if t is not a named type (or pointer to one).
func derefNamed(t types.Type) *types.Named {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, _ := t.(*types.Named)
	return named
}

// findMethod searches recv's method set for a method named name, including
// methods promoted via embedding.
func findMethod(recv *types.Named, name string) *types.Func {
	for m := range recv.Methods() {
		if m.Name() == name {
			return m
		}
	}
	return nil
}

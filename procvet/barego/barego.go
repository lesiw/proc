// Package barego defines an analyzer that flags bare go statements inside
// [lesiw.io/proc.Func] bodies, helping enforce the [lesiw.io/proc.Func]
// expectation that a Func does not spawn goroutines that outlive its own
// return.
//
// A bare go statement inside a Func spawns a goroutine that the Func does not
// wait on and that runs outside any protected call — any panic inside it
// crashes the program, and its completion is invisible to the Func's
// supervisor.
//
// barego operates only in packages that import lesiw.io/proc. It finds every
// call that passes a proc.Func value to the proc package — Group.Go, Watch, or
// any other proc function that accepts a Func — and walks the local call graph
// from there, reporting any bare go statement reachable from the Func's body.
//
// A go statement is not flagged if:
//
//   - Its expression is a method call to (proc.Func).Exec — Exec is itself a
//     protected call, so the new goroutine upholds the contract regardless of
//     what the inner Func does.
//   - Its expression is a function literal whose body contains a deferred
//     function literal that calls recover() and does not re-panic.
//
// Any other bare go statement reachable from a supervised Func is reported
// with the call chain that led to it.
package barego

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"

	"lesiw.io/proc/procvet/internal/check"
)

// Analyzer reports bare go statements inside proc.Func bodies passed to the
// proc package.
var Analyzer = &analysis.Analyzer{
	Name: "barego",
	Doc:  "report bare go statements inside supervised proc.Func bodies",
	Run:  run,
}

func run(pass *analysis.Pass) (any, error) {
	if !check.ImportsProc(pass.Pkg) {
		return nil, nil
	}
	locals := collectLocalFuncs(pass)
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			for _, arg := range funcArgs(pass, call) {
				name, body := resolveEntryArg(pass, arg, locals)
				if body == nil {
					continue
				}
				walkCheck(
					pass, body, []string{name}, map[string]struct{}{}, locals,
				)
			}
			return true
		})
	}
	return nil, nil
}

// funcArgs returns the arguments of call that have type proc.Func, but only
// when the callee is a function or method defined in lesiw.io/proc. This
// catches Group.Go, Watch, and any future proc function that accepts a Func,
// without hardcoding specific names.
func funcArgs(pass *analysis.Pass, call *ast.CallExpr) []ast.Expr {
	fn := resolveCallTarget(pass, call)
	if fn == nil {
		return nil
	}
	pkg := fn.Pkg()
	if pkg == nil || pkg.Path() != check.ProcPkg {
		return nil
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return nil
	}
	params := sig.Params()
	var out []ast.Expr
	for i, arg := range call.Args {
		if i >= params.Len() {
			break
		}
		if isProcFunc(params.At(i).Type()) {
			out = append(out, arg)
		}
	}
	return out
}

// isProcFunc reports whether typ is lesiw.io/proc.Func.
func isProcFunc(typ types.Type) bool {
	named, ok := typ.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Pkg().Path() == check.ProcPkg && obj.Name() == "Func"
}

// localFunc pairs a function's display name with its body.
type localFunc struct {
	name string
	body *ast.BlockStmt
}

// collectLocalFuncs returns a map from the types.Func object of each local
// function declaration to its name and body. This lets walkCheck resolve a
// call to a local function and descend into its body.
func collectLocalFuncs(pass *analysis.Pass) map[*types.Func]localFunc {
	out := make(map[*types.Func]localFunc)
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			obj, _ := pass.TypesInfo.Defs[fd.Name].(*types.Func)
			if obj == nil {
				continue
			}
			out[obj] = localFunc{name: fd.Name.Name, body: fd.Body}
		}
	}
	return out
}

// resolveEntryArg returns the display name and body of a function argument. If
// the argument is a function literal, the body is returned directly. If it is
// an identifier that resolves to a local function, the local function's body
// is returned. Imported functions and indirected values yield (name, nil).
func resolveEntryArg(
	pass *analysis.Pass, arg ast.Expr, locals map[*types.Func]localFunc,
) (string, *ast.BlockStmt) {
	switch a := arg.(type) {
	case *ast.FuncLit:
		return "<func literal>", a.Body
	case *ast.Ident:
		if fn, ok := pass.TypesInfo.Uses[a].(*types.Func); ok {
			if lf, ok := locals[fn]; ok {
				return lf.name, lf.body
			}
		}
		return a.Name, nil
	case *ast.SelectorExpr:
		if fn, ok := pass.TypesInfo.Uses[a.Sel].(*types.Func); ok {
			if lf, ok := locals[fn]; ok {
				return lf.name, lf.body
			}
		}
		return exprString(a), nil
	}
	return exprString(arg), nil
}

// exprString renders an expression for diagnostics. Best-effort.
func exprString(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprString(v.X) + "." + v.Sel.Name
	}
	return "?"
}

// walkCheck walks body looking for bare go statements, descending into local
// function calls transitively. chain accumulates the function names leading to
// the current scope for diagnostics. visited guards against cycles.
func walkCheck(
	pass *analysis.Pass, body *ast.BlockStmt, chain []string,
	visited map[string]struct{}, locals map[*types.Func]localFunc,
) {
	// The key uses the full chain joined by → (U+2192). Function
	// names cannot contain that character, so the join is
	// unambiguous.
	key := strings.Join(chain, "→")
	if _, ok := visited[key]; ok {
		return
	}
	visited[key] = struct{}{}

	ast.Inspect(body, func(n ast.Node) bool {
		// Don't descend into nested function literals — they are
		// handled as entry points if passed to a proc call, or as
		// the callee of a go statement below.
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		switch stmt := n.(type) {
		case *ast.GoStmt:
			if hasOwnProtection(pass, stmt) {
				return true
			}
			pass.Reportf(
				stmt.Go,
				"bare go statement inside a proc.Func "+
					"reachable via %s spawns a goroutine that "+
					"outlives the Func and can take the "+
					"program down on panic. Convert to a "+
					"proc.Func and call .Exec(ctx), or add "+
					"a deferred recover inside the goroutine.",
				strings.Join(chain, " → "),
			)
			return true
		case *ast.CallExpr:
			callee := resolveCallTarget(pass, stmt)
			if callee == nil {
				return true
			}
			lf, ok := locals[callee]
			if !ok {
				return true
			}
			walkCheck(pass, lf.body, append(chain, lf.name), visited, locals)
			return true
		}
		return true
	})
}

// resolveCallTarget returns the types.Func for a direct call expression, or
// nil if the call is an indirect call, a conversion, or otherwise
// unresolvable.
func resolveCallTarget(pass *analysis.Pass, call *ast.CallExpr) *types.Func {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if fn, ok := pass.TypesInfo.Uses[fun].(*types.Func); ok {
			return fn
		}
	case *ast.SelectorExpr:
		if fn, ok := pass.TypesInfo.Uses[fun.Sel].(*types.Func); ok {
			return fn
		}
	}
	return nil
}

// hasOwnProtection reports whether a go statement spawns a goroutine that is
// itself protected against panics. Two shapes are recognized:
//
//  1. go someFunc.Exec(ctx) — Exec is itself a protected call on proc.Func,
//     so the new goroutine upholds the contract regardless of what someFunc
//     does.
//  2. go func() { defer func() { recover(); ... }(); ... }() — the expression
//     is a function literal whose body contains a deferred function literal
//     that calls recover() and does not re-panic.
func hasOwnProtection(pass *analysis.Pass, stmt *ast.GoStmt) bool {
	if isExecCall(pass, stmt.Call) {
		return true
	}
	fl, ok := stmt.Call.Fun.(*ast.FuncLit)
	if !ok {
		return false
	}
	return bodyHasUnconditionalRecover(fl.Body)
}

// isExecCall reports whether call is a method call to (proc.Func).Exec.
func isExecCall(pass *analysis.Pass, call *ast.CallExpr) bool {
	fn := resolveCallTarget(pass, call)
	if fn == nil || fn.Name() != "Exec" {
		return false
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return false
	}
	named, ok := sig.Recv().Type().(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	return obj.Pkg().Path() == check.ProcPkg && obj.Name() == "Func"
}

// bodyHasUnconditionalRecover reports whether body contains a deferred
// function literal with an unconditional recover and no re-panic.
// "Unconditional" here means the deferred function's body syntactically
// contains a recover() call and does not syntactically contain a panic() call.
func bodyHasUnconditionalRecover(body *ast.BlockStmt) bool {
	for _, stmt := range body.List {
		def, ok := stmt.(*ast.DeferStmt)
		if !ok {
			continue
		}
		fl, ok := def.Call.Fun.(*ast.FuncLit)
		if !ok {
			continue
		}
		if bodyCallsBuiltin(fl.Body, "recover") &&
			!bodyCallsBuiltin(fl.Body, "panic") {
			return true
		}
	}
	return false
}

// bodyCallsBuiltin reports whether body contains a call to the named builtin.
// Local shadows are ignored.
func bodyCallsBuiltin(body *ast.BlockStmt, name string) bool {
	var found bool
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != name {
			return true
		}
		if ident.Obj != nil {
			return true // local shadow, not the builtin
		}
		found = true
		return false
	})
	return found
}

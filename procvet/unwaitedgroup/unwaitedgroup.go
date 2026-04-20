// Package unwaitedgroup defines an analyzer that reports [lesiw.io/proc.Group]
// instances constructed via [lesiw.io/proc.NewGroup] whose Wait method is
// never called in the enclosing function.
//
// A Group whose Wait is never called leaks: workers scheduled via Group.Go may
// outlive the caller, any error they return is dropped, and the group's
// context may never be observed by anything that blocks on it. This is the
// proc-specific shape of the broader "unobserved goroutine" bug class.
//
// The analyzer only inspects NewGroup results assigned to a local variable. It
// walks the enclosing function body and classifies each use of the variable:
//
//   - A call to v.Wait() is the canonical satisfying use.
//     The analyzer does not check whether Wait is reached
//     on every return path — a single syntactic occurrence
//     in the function body is enough.
//   - Being returned, being stored in a composite literal,
//     being sent on a channel, or being passed as an
//     argument to another function is treated as escape:
//     the analyzer assumes the receiving code is
//     responsible for Wait and does not flag.
//   - Method calls other than Wait (for example, v.Go) are
//     ignored; they neither satisfy nor escape.
//
// If no use satisfies and no use escapes, the analyzer reports the NewGroup
// call.
//
// The analyzer only runs in packages that import lesiw.io/proc. Groups
// constructed elsewhere (stdlib sync.WaitGroup,
// golang.org/x/sync/errgroup.Group) have the same bug class but are out of
// scope here.
package unwaitedgroup

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"

	"lesiw.io/proc/procvet/internal/check"
)

// Analyzer reports proc.NewGroup results whose Wait method is never called in
// the enclosing function.
var Analyzer = &analysis.Analyzer{
	Name: "unwaitedgroup",
	Doc: "report proc.NewGroup results whose Wait method is " +
		"never called in the enclosing function",
	Run: run,
}

func run(pass *analysis.Pass) (any, error) {
	if !check.ImportsProc(pass.Pkg) {
		return nil, nil
	}
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch fn := n.(type) {
			case *ast.FuncDecl:
				if fn.Body != nil {
					checkFunc(pass, fn.Body)
				}
			case *ast.FuncLit:
				checkFunc(pass, fn.Body)
			}
			return true
		})
	}
	return nil, nil
}

// checkFunc finds every local assignment whose RHS is a direct call to
// proc.NewGroup, and reports it if the enclosing function body does not
// satisfy or escape the resulting variable.
func checkFunc(pass *analysis.Pass, body *ast.BlockStmt) {
	ast.Inspect(body, func(n ast.Node) bool {
		// Don't descend into nested function literals;
		// they're visited as separate scopes by the outer
		// run loop.
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, rhs := range assign.Rhs {
			if !isNewGroupCall(pass, rhs) {
				continue
			}
			if i >= len(assign.Lhs) {
				continue
			}
			ident, ok := assign.Lhs[i].(*ast.Ident)
			if !ok {
				continue
			}
			obj := identObj(pass, ident)
			if obj == nil {
				continue
			}
			if satisfiedOrEscaped(pass, body, obj) {
				continue
			}
			pass.Reportf(
				rhs.Pos(),
				"proc.NewGroup result %q is never Wait'd "+
					"in the enclosing function; call "+
					"%s.Wait() before returning, or "+
					"return %s so a caller can",
				ident.Name, ident.Name, ident.Name,
			)
		}
		return true
	})
}

// identObj returns the types.Object for ident, whether it is being defined (`g
// := ...`) or reassigned (`g = ...`).
func identObj(pass *analysis.Pass, ident *ast.Ident) types.Object {
	if obj := pass.TypesInfo.Defs[ident]; obj != nil {
		return obj
	}
	return pass.TypesInfo.Uses[ident]
}

// isNewGroupCall reports whether expr is a direct call to
// lesiw.io/proc.NewGroup.
func isNewGroupCall(pass *analysis.Pass, expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	fn := resolveCallTarget(pass, call)
	if fn == nil {
		return false
	}
	if fn.Pkg() == nil || fn.Pkg().Path() != check.ProcPkg {
		return false
	}
	return fn.Name() == "NewGroup"
}

// satisfiedOrEscaped reports whether body contains either a Wait call on obj
// (satisfying use) or a use of obj that plausibly transfers responsibility
// elsewhere (escape).
func satisfiedOrEscaped(
	pass *analysis.Pass, body *ast.BlockStmt, obj types.Object,
) bool {
	var done bool
	ast.Inspect(body, func(n ast.Node) bool {
		if done {
			return false
		}
		switch node := n.(type) {
		case *ast.CallExpr:
			if isWaitCall(pass, node, obj) {
				done = true
				return false
			}
			for _, arg := range node.Args {
				if refersTo(pass, arg, obj) {
					done = true
					return false
				}
			}
		case *ast.ReturnStmt:
			for _, res := range node.Results {
				if refersTo(pass, res, obj) {
					done = true
					return false
				}
			}
		case *ast.SendStmt:
			if refersTo(pass, node.Value, obj) {
				done = true
				return false
			}
		case *ast.CompositeLit:
			for _, elt := range node.Elts {
				if refersTo(pass, elt, obj) {
					done = true
					return false
				}
			}
		case *ast.AssignStmt:
			// A store into a non-local location (field,
			// index, dereference) hands responsibility
			// off. A plain local reassignment does not.
			for i, lhs := range node.Lhs {
				if i >= len(node.Rhs) {
					break
				}
				if _, ok := lhs.(*ast.Ident); ok {
					continue
				}
				if refersTo(pass, node.Rhs[i], obj) {
					done = true
					return false
				}
			}
		}
		return true
	})
	return done
}

// isWaitCall reports whether call is a method call of the form obj.Wait().
func isWaitCall(
	pass *analysis.Pass, call *ast.CallExpr, obj types.Object,
) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel.Name != "Wait" {
		return false
	}
	recv, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pass.TypesInfo.Uses[recv] == obj
}

// refersTo reports whether expr contains any identifier that resolves to obj.
// The walk does not descend into selector tails (x.Sel is treated as a
// reference to x, not Sel).
func refersTo(pass *analysis.Pass, expr ast.Expr, obj types.Object) bool {
	var found bool
	ast.Inspect(expr, func(n ast.Node) bool {
		if found {
			return false
		}
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if refersTo(pass, sel.X, obj) {
				found = true
			}
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

// Package ctxexit defines an analyzer that reports calls to escape-hatch
// termination functions inside functions that take a [context.Context]
// parameter, helping enforce the [lesiw.io/proc.Func] expectation that a Func
// signals completion by returning.
//
// A [lesiw.io/proc.Func] is expected to exit only by returning. Some
// termination mechanisms bypass the return path entirely and take down the
// program or the goroutine in a way that no supervisor can observe:
//
//   - os.Exit and syscall.Exit terminate the process
//     immediately without running deferred functions.
//   - log.Fatal, log.Fatalf, and log.Fatalln all call
//     os.Exit(1) internally after logging.
//   - runtime.Goexit terminates the calling goroutine
//     cleanly, running deferred functions — but recover()
//     returns nil for Goexit, so a process that calls
//     runtime.Goexit vanishes silently without surfacing a
//     failure through [lesiw.io/proc.Func.Exec].
//
// None of these are panics; none are recoverable by a protected call. Calling
// them inside a ctx-aware body is a promise-breaking bug: the function took a
// context (claiming to be supervisable) and then exited through a channel its
// supervisor cannot observe.
//
// The analyzer flags calls to:
//
//	os.Exit
//	syscall.Exit
//	runtime.Goexit
//	log.Fatal
//	log.Fatalf
//	log.Fatalln
//
// panic and log.Panic are NOT flagged because they go through recover and are
// caught by [lesiw.io/proc.Func.Exec] cleanly.
package ctxexit

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"

	"lesiw.io/proc/procvet/internal/check"
)

// bannedCalls maps a (package, function) pair to the reason it is banned. Only
// functions that bypass panic recovery are listed here. The map values are
// included in the diagnostic message.
var bannedCalls = map[string]map[string]string{
	"os": {
		"Exit": "os.Exit terminates the process without " +
			"running deferred functions",
	},
	"syscall": {
		"Exit": "syscall.Exit terminates the process " +
			"without running deferred functions",
	},
	"runtime": {
		"Goexit": "runtime.Goexit terminates the goroutine " +
			"silently; recover() does not catch it",
	},
	"log": {
		"Fatal":   "log.Fatal calls os.Exit(1) after logging",
		"Fatalf":  "log.Fatalf calls os.Exit(1) " + "after logging",
		"Fatalln": "log.Fatalln calls os.Exit(1) " + "after logging",
	},
}

// Analyzer reports banned termination calls inside functions taking
// context.Context.
var Analyzer = &analysis.Analyzer{
	Name: "ctxexit",
	Doc: "report os.Exit, runtime.Goexit, and log.Fatal " +
		"inside functions that take context.Context",
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

// checkBody walks body and flags banned calls. Nested function literals are
// skipped — the outer Inspect visits them separately and decides whether to
// check their body based on their own signature.
func checkBody(pass *analysis.Pass, body *ast.BlockStmt) {
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		pkg, name, reason, ok := bannedCall(pass, call)
		if !ok {
			return true
		}
		pass.Reportf(
			call.Pos(),
			"%s.%s inside a ctx-aware body exits through a "+
				"channel the supervisor cannot observe: %s. "+
				"Return an error instead.",
			pkg, name, reason,
		)
		return true
	})
}

// bannedCall reports whether call targets one of the banned (package,
// function) pairs. If yes, it returns the pkg name, function name, and the
// reason.
func bannedCall(
	pass *analysis.Pass, call *ast.CallExpr,
) (pkg, name, reason string, ok bool) {
	sel, isSel := call.Fun.(*ast.SelectorExpr)
	if !isSel {
		return "", "", "", false
	}
	obj, isFunc := pass.TypesInfo.Uses[sel.Sel].(*types.Func)
	if !isFunc {
		return "", "", "", false
	}
	p := obj.Pkg()
	if p == nil {
		return "", "", "", false
	}
	funcs, found := bannedCalls[p.Path()]
	if !found {
		return "", "", "", false
	}
	r, found := funcs[obj.Name()]
	if !found {
		return "", "", "", false
	}
	return p.Name(), obj.Name(), r, true
}

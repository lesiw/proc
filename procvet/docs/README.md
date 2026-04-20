# lesiw.io/proc/procvet

[![Go Reference](https://pkg.go.dev/badge/lesiw.io/proc/procvet.svg)](https://pkg.go.dev/lesiw.io/proc/procvet)

Command procvet bundles the proc static analyzers into a single vettool
binary.

```sh
go install lesiw.io/proc/procvet@latest
go vet -vettool=$(which procvet) ./...
```

procvet helps Go code adhere to the expectations described in the
`lesiw.io/proc` package documentation: a supervised Func should signal
completion by returning, not spawn goroutines that outlive its return, and
honor its context. proc's runtime mechanism (`Func.Exec`)
catches panics and surfaces them as a `*Error`; the analyzers
in this module catch shapes that violate the other expectations at write
time. A bare go statement, a time.Sleep that ignores cancellation, an
os.Exit from inside a process: each is a violation procvet can flag. These checks
are necessary but not sufficient — they catch common shapes of violation,
not every one.

The procvet command wraps seven analyzers, each of which is also importable as a
package under lesiw.io/proc/procvet/\<name\>. Users who want to compose their own
analyzer set can import the individual analyzers directly instead of the
combined tool.

| Analyzer | Reports |
|---|---|
| `barego` | bare go statements inside supervised proc.Func bodies |
| `blockingchan` | bare channel sends/receives and for-range over channels in ctx-aware bodies |
| `ctxexit` | os.Exit, syscall.Exit, runtime.Goexit, log.Fatal/Fatalf/Fatalln inside ctx-aware bodies |
| `ctxsleep` | time.Sleep inside ctx-aware bodies |
| `ctxvariant` | calls to X when pkg.XContext or pkg.XWithContext exists |
| `unusedctx` | context.Context parameters that are never referenced |
| `unwaitedgroup` | proc.NewGroup results whose Wait method is never called in the enclosing function |

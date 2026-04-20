# lesiw.io/proc

[![Go Reference](https://pkg.go.dev/badge/lesiw.io/proc.svg)](https://pkg.go.dev/lesiw.io/proc)

Package proc groups context-bound functions under shared supervision.

```go
g := proc.NewGroup(ctx)
g.Go(walk)
g.Go(chewGum)
if err := g.Wait(); err != nil {
	log.Fatal(err)
}
```

A process, or `Func`, is a func(context.Context) error that follows a few
specific rules.

- A `Func` must honor its `context.Context`, returning as soon
  as is feasible once the context is canceled;
- A `Func` must signal its completion by returning an error value,
  rather than exiting in a manner which would stop the entire program;
- A `Func` must not spawn goroutines that outlive its return, though
  it may itself spawn a new `Group` for concurrent work.

`lesiw.io/proc/procvet` provides static analyzers to help catch violations
of these rules, as well as common gotchas around context awareness, such
as blocking indefinitely on a channel send or receive.

A `Func` may be executed as a process via `Func.Exec`, which catches any
panic from the `Func` and returns its value and stacktrace as an `*Error`.
It also derives a new context on start which is canceled at the completion
of the process, regardless of whether the process succeeded (returned nil)
or failed.

`Group` is similar in operation to `golang.org/x/sync/errgroup`, but it
takes context-awareness a step further. Every process spawned by `Group.Go`,
and indeed the group as a whole, are explicitly tied to the lifetime of the
`context.Context` the `Group` was instantiated with by `NewGroup`. For this
reason, the zero value of `Group` is not usable — it must be constructed
with some `context.Context`.

`Group.Go` is safe for concurrent use; multiple goroutines may call it on
the same `Group` at the same time. It is also safe to call `Group.Go` from
inside a running `Func` for dynamic fan-out. `Group.Wait` must be called
exactly once — calling `Group.Go` after `Group.Wait` has returned is an
error, and will result in a panic.

## All for one

Scheduling Funcs with `Group.Go` is analogous to all-for-one supervision.
Under normal operation, `Group.Wait` waits for all processes to complete and
returns nil. If one of the processes returns a non-nil error value, the
group sends a cancellation signal to its context. In this circumstance,
`Group.Wait` still waits for all processes to complete their work, then
returns that first non-nil error value as its own error value.

This strategy is the right default for short-lived workers where one piece
failing means the whole effort should abort.

```go
g := proc.NewGroup(ctx)
for _, url := range urls {
	g.Go(func(ctx context.Context) error {
		if _, err := http.Get(url); err != nil {
			return fmt.Errorf("failed to fetch: %v", url)
		}
		return nil
	})
}
if err := g.Wait(); err != nil {
	log.Fatal(err)
}
```

## One for one

Keeping a `Func` long-lived is as simple as never returning an error value.
The only way the function should complete is upon context cancellation. A
simple one-for-one strategy is provided via `Watch`.

```go
g.Go(proc.Watch(serveHTTP))
```

`Watch` executes the `Func` in a loop until its context cancels. Each
iteration invokes the process via `Func.Exec`. A panic or error from one
iteration ends that execution and starts the next.

`Watch` runs for the lifetime of the ctx it is called with; when scheduled
via `Group.Go`, that is the group's context. `Watch` returns nil on
cancellation. An iteration already in progress when ctx cancels runs to its
own completion — the `Func` being watched must still observe context
cancellation.

To bound CPU use during a crash loop, `Watch` applies a decay-based backoff
between iterations: rapid failures delay the next execution, while a process
that stabilizes returns to full speed within minutes.

Recovered panics in a `Watch`-wrapped process are logged at Error level and
non-nil error returns at Warn level, both via the logger attached to ctx
(see Logging below).

`Watch` can be used to supervise several unrelated long-lived processes from
main. Combine a signal-aware context with `Watch` wrappers and block on
`Group.Wait`.

```go
func main() {
	ctx, cancel := signal.NotifyContext(
		context.Background(),
		os.Interrupt, syscall.SIGTERM,
	)
	defer cancel()

	g := proc.NewGroup(ctx)
	g.Go(proc.Watch(httpServer))
	g.Go(proc.Watch(metricsReporter))

	if err := g.Wait(); err != nil {
		log.Fatal(err)
	}
}
```

## Rest for one

When some processes depend on others — so that a failure in one should
terminate its dependents without disturbing its independents — model the
dependency by putting the dependent processes in a nested `Group`.

```go
g := proc.NewGroup(ctx)
g.Go(independent)
g.Go(func(ctx context.Context) error {
	inner := proc.NewGroup(ctx)
	inner.Go(upstream)
	inner.Go(downstream)
	return inner.Wait()
})
```

If upstream fails, the inner `Group`'s context cancels downstream. The outer
process — independent — keeps running. Wrap the inner closure in `Watch` if
automatic restarts of the dependent pair are desired.

## Let it error

proc recovers panics, but it does not encourage programming with panics. The
full and complete logging of the panic stack by `Watch` is designed to be
intentionally disruptive. Panics should be rare and loud.

Error values, on the other hand, are common, and should be viewed as a means
of avoiding defensive programming. Consider an error return in proc to be
conceptually equivalent to a crash in Erlang.

For a plain `Group.Go` process, "let it error" just means returning an error
— the `Group` sets its context's cancellation cause to that error, which
propagates to all siblings. A sibling doing ongoing work can select on
ctx.Done and react to the specific cause via `context.Cause` before
unwinding.

```go
g.Go(func(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			if errors.Is(context.Cause(ctx), errBudget) {
				flushPartial()
			}
			return context.Cause(ctx)
		case item := <-queue:
			process(item)
		}
	}
})
```

A `context.CancelCauseFunc` carries a structured reason across any scope
boundary. A process that detects an unrecoverable condition calls
cancel(err) on whatever context it wants to tear down — its own group,
a parent group, or an unrelated coordinator. Observers anywhere in the
tree read the reason via `context.Cause`.

```go
ctx, cancel := context.WithCancelCause(parent)
g := proc.NewGroup(ctx)
g.Go(proc.Watch(func(ctx context.Context) error {
	if err := checkLicense(ctx); err != nil {
		cancel(err) // tears down the group with a reason
		return nil
	}
	// ... normal work ...
	return nil
}))
```

Because a CancelCauseFunc is a plain Go value, it can be passed through
any boundary: one group can end another group's scope, a watchdog can
cancel a worker pool, or a coordinator can shut down several groups with
a single reason. This is how proc scales beyond a single group without
adding new primitives.

## Logging

proc routes its logging through whatever `*slog.Logger` is attached to the
`Group`'s context. Use `WithLogger` to attach one at construction time, and
`Logger` to read it from inside a `Func`, a wrapper, or any helper they
call:

```go
ctx := proc.WithLogger(ctx, myLogger)
g := proc.NewGroup(ctx)
g.Go(func(ctx context.Context) error {
	log := proc.Logger(ctx)
	log.Info("starting work")
	return doWork(ctx)
})
```

If no logger is attached, proc falls back to `slog.Default`.

The logger is an ambient property of the scope a `Group` supervises — like
the context cancellation it already carries. A `Func` and any wrapper above
share the same view of "the logger for this scope" without having to pass it
explicitly. This is the same channel proc uses for every other scope-bound
concern, and it lets free-function wrappers participate in per-`Group`
logging without holding a `Group` reference.

## Custom supervision

Strategies other than `Watch` are short wrappers you write yourself. The
signature is the same shape — a `Func` in, a `Func` out — so they compose
with `Group.Go` and with each other:

```go
func withRetries(
	n int, fn proc.Func,
) proc.Func {
	return func(ctx context.Context) error {
		for range n {
			if err := fn(ctx); err == nil {
				return nil
			}
		}
		return fmt.Errorf("gave up after %d attempts", n)
	}
}

g.Go(withRetries(5, connectToDatabase))
```

If a plain `Group.Go` process should end a daemon scope on its own exit,
wire the cancel explicitly at the call site:

```go
ctx, cancel := context.WithCancel(parent)
g := proc.NewGroup(ctx)
g.Go(func(ctx context.Context) error {
	defer cancel() // my exit ends the group
	return migrate(ctx)
})
g.Go(proc.Watch(serveHTTP))
```

Or use a caller-site loop that rebuilds the group on failure instead of
restarting individual workers:

```go
for ctx.Err() == nil {
	g := proc.NewGroup(ctx)
	g.Go(worker1)
	g.Go(worker2)
	if err := g.Wait(); err != nil {
		log.Printf("restarting: %v", err)
		continue
	}
	break
}
```

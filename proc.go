// Package proc groups context-bound functions under shared supervision.
//
//	g := proc.NewGroup(ctx)
//	g.Go(walk)
//	g.Go(chewGum)
//	if err := g.Wait(); err != nil {
//		log.Fatal(err)
//	}
//
// A process, or [Func], is a func(context.Context) error that follows a few
// specific rules.
//
//   - A [Func] must honor its [context.Context], returning as soon
//     as is feasible once the context is canceled;
//   - A [Func] must signal its completion by returning an error value,
//     rather than exiting in a manner which would stop the entire program;
//   - A [Func] must not spawn goroutines that outlive its return, though
//     it may itself spawn a new [Group] for concurrent work.
//
// [lesiw.io/proc/procvet] provides static analyzers to help catch violations
// of these rules, as well as common gotchas around context awareness, such
// as blocking indefinitely on a channel send or receive.
//
// A [Func] may be executed as a process via [Func.Exec], which catches any
// panic from the [Func] and returns its value and stacktrace as an [*Error].
// It also derives a new context on start which is canceled at the completion
// of the process, regardless of whether the process succeeded (returned nil)
// or failed.
//
// [Group] is similar in operation to [golang.org/x/sync/errgroup], but it
// takes context-awareness a step further. Every process spawned by [Group.Go],
// and indeed the group as a whole, are explicitly tied to the lifetime of the
// [context.Context] the [Group] was instantiated with by [NewGroup]. For this
// reason, the zero value of [Group] is not usable — it must be constructed
// with some [context.Context].
//
// [Group.Go] is safe for concurrent use; multiple goroutines may call it on
// the same [Group] at the same time. It is also safe to call [Group.Go] from
// inside a running [Func] for dynamic fan-out. [Group.Wait] must be called
// exactly once — calling [Group.Go] after [Group.Wait] has returned is an
// error, and will result in a panic.
//
// # All for one
//
// Scheduling Funcs with [Group.Go] is analogous to all-for-one supervision.
// Under normal operation, [Group.Wait] waits for all processes to complete and
// returns nil. If one of the processes returns a non-nil error value, the
// group sends a cancellation signal to its context. In this circumstance,
// [Group.Wait] still waits for all processes to complete their work, then
// returns that first non-nil error value as its own error value.
//
// This strategy is the right default for short-lived workers where one piece
// failing means the whole effort should abort.
//
//	g := proc.NewGroup(ctx)
//	for _, url := range urls {
//		g.Go(func(ctx context.Context) error {
//			if _, err := http.Get(url); err != nil {
//				return fmt.Errorf("failed to fetch: %v", url)
//			}
//			return nil
//		})
//	}
//	if err := g.Wait(); err != nil {
//		log.Fatal(err)
//	}
//
// # One for one
//
// Keeping a [Func] long-lived is as simple as never returning an error value.
// The only way the function should complete is upon context cancellation. A
// simple one-for-one strategy is provided via [Watch].
//
//	g.Go(proc.Watch(serveHTTP))
//
// [Watch] executes the [Func] in a loop until its context cancels. Each
// iteration invokes the process via [Func.Exec]. A panic or error from one
// iteration ends that execution and starts the next.
//
// [Watch] runs for the lifetime of the ctx it is called with; when scheduled
// via [Group.Go], that is the group's context. [Watch] returns nil on
// cancellation. An iteration already in progress when ctx cancels runs to its
// own completion — the [Func] being watched must still observe context
// cancellation.
//
// To bound CPU use during a crash loop, [Watch] applies a decay-based backoff
// between iterations: rapid failures delay the next execution, while a process
// that stabilizes returns to full speed within minutes.
//
// Recovered panics in a [Watch]-wrapped process are logged at Error level and
// non-nil error returns at Warn level, both via the logger attached to ctx
// (see [Logger]).
//
// [Watch] can be used to supervise several unrelated long-lived processes from
// main. Combine a signal-aware context with [Watch] wrappers and block on
// [Group.Wait].
//
//	func main() {
//		ctx, cancel := signal.NotifyContext(
//			context.Background(),
//			os.Interrupt, syscall.SIGTERM,
//		)
//		defer cancel()
//
//		g := proc.NewGroup(ctx)
//		g.Go(proc.Watch(httpServer))
//		g.Go(proc.Watch(metricsReporter))
//
//		if err := g.Wait(); err != nil {
//			log.Fatal(err)
//		}
//	}
//
// # Rest for one
//
// When some processes depend on others — so that a failure in one should
// terminate its dependents without disturbing its independents — model the
// dependency by putting the dependent processes in a nested [Group].
//
//	g := proc.NewGroup(ctx)
//	g.Go(independent)
//	g.Go(func(ctx context.Context) error {
//		inner := proc.NewGroup(ctx)
//		inner.Go(upstream)
//		inner.Go(downstream)
//		return inner.Wait()
//	})
//
// If upstream fails, the inner [Group]'s context cancels downstream. The outer
// process — independent — keeps running. Wrap the inner closure in [Watch] if
// automatic restarts of the dependent pair are desired.
//
// # Let it error
//
// proc recovers panics, but it does not encourage programming with panics. The
// full and complete logging of the panic stack by [Watch] is designed to be
// intentionally disruptive. Panics should be rare and loud.
//
// Error values, on the other hand, are common, and should be viewed as a means
// of avoiding defensive programming. Consider an error return in proc to be
// conceptually equivalent to a crash in Erlang.
//
// For a plain [Group.Go] process, "let it error" just means returning an error
// — the [Group] sets its context's cancellation cause to that error, which
// propagates to all siblings. A sibling doing ongoing work can select on
// ctx.Done and react to the specific cause via [context.Cause] before
// unwinding.
//
//	g.Go(func(ctx context.Context) error {
//		for {
//			select {
//			case <-ctx.Done():
//				if errors.Is(context.Cause(ctx), errBudget) {
//					flushPartial()
//				}
//				return context.Cause(ctx)
//			case item := <-queue:
//				process(item)
//			}
//		}
//	})
//
// A [context.CancelCauseFunc] carries a structured reason across any scope
// boundary. A process that detects an unrecoverable condition calls
// cancel(err) on whatever context it wants to tear down — its own group,
// a parent group, or an unrelated coordinator. Observers anywhere in the
// tree read the reason via [context.Cause].
//
//	ctx, cancel := context.WithCancelCause(parent)
//	g := proc.NewGroup(ctx)
//	g.Go(proc.Watch(func(ctx context.Context) error {
//		if err := checkLicense(ctx); err != nil {
//			cancel(err) // tears down the group with a reason
//			return nil
//		}
//		// ... normal work ...
//		return nil
//	}))
//
// Because a CancelCauseFunc is a plain Go value, it can be passed through
// any boundary: one group can end another group's scope, a watchdog can
// cancel a worker pool, or a coordinator can shut down several groups with
// a single reason. This is how proc scales beyond a single group without
// adding new primitives.
//
// # Logging
//
// proc routes its logging through whatever [*slog.Logger] is attached to the
// [Group]'s context. Use [WithLogger] to attach one at construction time, and
// [Logger] to read it from inside a [Func], a wrapper, or any helper they
// call:
//
//	ctx := proc.WithLogger(ctx, myLogger)
//	g := proc.NewGroup(ctx)
//	g.Go(func(ctx context.Context) error {
//		log := proc.Logger(ctx)
//		log.Info("starting work")
//		return doWork(ctx)
//	})
//
// If no logger is attached, proc falls back to [slog.Default].
//
// The logger is an ambient property of the scope a [Group] supervises — like
// the context cancellation it already carries. A [Func] and any wrapper above
// share the same view of "the logger for this scope" without having to pass it
// explicitly. This is the same channel proc uses for every other scope-bound
// concern, and it lets free-function wrappers participate in per-[Group]
// logging without holding a [Group] reference.
//
// # Custom supervision
//
// Strategies other than [Watch] are short wrappers you write yourself. The
// signature is the same shape — a [Func] in, a [Func] out — so they compose
// with [Group.Go] and with each other:
//
//	func withRetries(
//		n int, fn proc.Func,
//	) proc.Func {
//		return func(ctx context.Context) error {
//			for range n {
//				if err := fn(ctx); err == nil {
//					return nil
//				}
//			}
//			return fmt.Errorf("gave up after %d attempts", n)
//		}
//	}
//
//	g.Go(withRetries(5, connectToDatabase))
//
// If a plain [Group.Go] process should end a daemon scope on its own exit,
// wire the cancel explicitly at the call site:
//
//	ctx, cancel := context.WithCancel(parent)
//	g := proc.NewGroup(ctx)
//	g.Go(func(ctx context.Context) error {
//		defer cancel() // my exit ends the group
//		return migrate(ctx)
//	})
//	g.Go(proc.Watch(serveHTTP))
//
// Or use a caller-site loop that rebuilds the group on failure instead of
// restarting individual workers:
//
//	for ctx.Err() == nil {
//		g := proc.NewGroup(ctx)
//		g.Go(worker1)
//		g.Go(worker2)
//		if err := g.Wait(); err != nil {
//			log.Printf("restarting: %v", err)
//			continue
//		}
//		break
//	}
package proc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"runtime/debug"
	"time"

	"golang.org/x/sync/errgroup"
)

// Func is the unit of work supervised by proc: a function with a context-bound
// lifetime that behaves as a supervisable process. Any func(context.Context)
// error is structurally a [Func] and can be passed wherever a [Func] is
// expected without conversion, provided it upholds the expectations described
// in the package documentation.
//
// Func is a named function type rather than a plain func(context.Context)
// error so that the [Func.Exec] method can hang off it. [Group.Go] and [Watch]
// both invoke their Funcs through this method.
type Func func(context.Context) error

// Exec executes f as a supervised process on the current goroutine. It derives
// a child context from ctx and passes it to f; when f returns — cleanly, with
// an error, or with a caught panic — the child context is canceled, tearing
// down any scope f derived from it. Exec returns f's returned error unchanged,
// or a [*Error] wrapping any panic that escaped f along with a stack trace. On
// a clean completion Exec returns nil.
//
// Exec is the inline counterpart to [Group.Go]. Reach for [Group.Go] when you
// want f to run concurrently on a new goroutine; reach for Exec when you are a
// supervisor or wrapper staying on the current goroutine. Code that calls f
// inline for its result needs neither: panics propagate up the call stack
// naturally, and process execution is only needed where a panic would
// otherwise terminate a goroutine you do not own.
//
// [Group.Go] and [Watch] both delegate to Exec internally.
func (f Func) Exec(ctx context.Context) (err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			err = &Error{Value: r, Stack: debug.Stack()}
		}
	}()
	return f(ctx)
}

// Group is a collection of [Func] values whose lifetimes are bounded to a
// shared context. Construct one with [NewGroup]. The zero value is not usable:
// calls to [Group.Go] or [Group.Wait] on a Group with no context panic.
type Group struct {
	eg  *errgroup.Group
	ctx context.Context
}

// NewGroup returns a new [Group] derived from parent. The group's internal
// context cancels on the first non-nil error from a [Group.Go] [Func] or when
// [Group.Wait] returns, whichever comes first.
func NewGroup(parent context.Context) *Group {
	eg, ctx := errgroup.WithContext(parent)
	return &Group{eg: eg, ctx: ctx}
}

// Go schedules f on a new goroutine in the group. Panics from f are recovered
// and logged at Error level via the group's logger (see [Logger]) before being
// returned as [*Error] through [Group.Wait].
func (g *Group) Go(f Func) {
	if g.ctx == nil {
		panic("proc: Group has no context")
	}
	g.eg.Go(func() error {
		err := f.Exec(g.ctx)
		if err != nil && isPanic(err) {
			Logger(g.ctx).Error("proc: panic", "err", err)
		}
		return err
	})
}

// Wait blocks until every scheduled [Func] has returned and returns the first
// non-nil error.
//
// Wait does not take a context. When the group's context cancels, every [Func]
// observing its own context returns, and Wait unblocks naturally. A [Func]
// that ignores its context will block Wait until it returns of its own accord;
// bounding shutdown is the program's responsibility, not the [Group]'s.
func (g *Group) Wait() error {
	if g.ctx == nil {
		panic("proc: Group has no context")
	}
	return g.eg.Wait()
}

// loggerKey is the unexported context key under which proc stores a
// [*slog.Logger]. Each call to [WithLogger] produces a child context whose
// value at this key is the supplied logger.
type loggerKey struct{}

// WithLogger returns a copy of ctx carrying logger as the proc-scoped logger.
// Passing a nil logger returns ctx unchanged.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	if logger == nil {
		return ctx
	}
	return context.WithValue(ctx, loggerKey{}, logger)
}

// Logger returns the [*slog.Logger] attached to ctx via [WithLogger]. If no
// logger is attached, Logger returns [slog.Default]. The returned value is
// never nil.
func Logger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// Error is the error produced by [Func.Exec] when the executed function
// panics. It carries the original panic value and a stack trace captured at
// the recover site. [Group.Go] and [Watch] both delegate to [Func.Exec], so a
// panic inside a supervised [Func] surfaces as a [*Error].
//
// Callers can inspect the original value via [errors.As]:
//
//	var pe *proc.Error
//	if errors.As(err, &pe) {
//		log.Printf("panic value: %v", pe.Value)
//		log.Printf("stack:\n%s", pe.Stack)
//	}
type Error struct {
	// The value passed to panic.
	Value any

	// A stack trace captured via runtime/debug.Stack at the recover site.
	Stack []byte
}

func (e *Error) Error() string {
	return fmt.Sprintf("panic: %v\n%s", e.Value, e.Stack)
}

// Unwrap returns the panic value as an error if it implements error, or nil
// otherwise. This makes [Error] transparent to [errors.Is] and [errors.As]: a
// [Func] that panics with a sentinel error (or a [runtime.Error] like a
// nil-deref) can be inspected through the normal error chain without
// special-casing [Error].
func (e *Error) Unwrap() error {
	if err, ok := e.Value.(error); ok {
		return err
	}
	return nil
}

// Watch wraps f in a loop that reruns it until ctx cancels, providing
// one-for-one supervision. Each iteration is a separate [Func.Exec]: panics
// and errors end that iteration and start the next without propagating to
// [Group.Wait]. Watch returns nil on cancellation.
//
// A decay-based backoff bounds CPU use during a crash loop. Recovered panics
// are logged at Error level and non-nil error returns at Warn level, both via
// the ctx logger (see [Logger]).
func Watch(f Func) Func {
	return func(ctx context.Context) error {
		log := Logger(ctx)
		var b backoff
		for ctx.Err() == nil {
			if err := f.Exec(ctx); err != nil {
				if isPanic(err) {
					log.Error("proc: panic in watched func", "err", err)
				} else {
					log.Warn("proc: error in watched func", "err", err)
				}
			}
			b.tick()
			if !b.wait(ctx) {
				return nil
			}
		}
		return nil
	}
}

func isPanic(err error) bool {
	var e *Error
	return errors.As(err, &e)
}

// backoff implements the decay-based restart policy used by Watch. The counter
// halves every decayHalfLife of elapsed time and increments on every restart.
// Once it exceeds backoffThreshold, wait sleeps for backoffSleep plus jitter
// before the next attempt.
//
// The shape is borrowed from thejerf/suture v4's Spec: FailureDecay=30s,
// FailureThreshold=5, FailureBackoff=15s. Credit to the suture authors for the
// policy; see https://pkg.go.dev/github.com/thejerf/suture/v4#Spec.
//
// backoff is not safe for concurrent use; each Watch call gets its own.
type backoff struct {
	counter float64
	lastHit time.Time
}

const (
	decayHalfLife    = 30 * time.Second
	backoffThreshold = 5.0
	backoffSleep     = 15 * time.Second
	backoffJitter    = 7500 * time.Millisecond
)

func (b *backoff) tick() {
	b.decay()
	b.counter++
	b.lastHit = time.Now()
}

func (b *backoff) decay() {
	if b.lastHit.IsZero() {
		return
	}
	halvings := time.Since(b.lastHit).Seconds() / decayHalfLife.Seconds()
	b.counter *= math.Pow(0.5, halvings)
}

func (b *backoff) wait(ctx context.Context) bool {
	b.decay()
	if b.counter <= backoffThreshold {
		return ctx.Err() == nil
	}
	jitter := rand.N(backoffJitter)
	select {
	case <-ctx.Done():
		return false
	case <-time.After(backoffSleep + jitter):
		return ctx.Err() == nil
	}
}

package proc_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"lesiw.io/proc"
)

func walk(context.Context) error {
	fmt.Println("walking")
	return nil
}

func chewGum(context.Context) error {
	fmt.Println("chewing gum")
	return nil
}

// A Group runs Funcs under a shared context with panic recovery.
func ExampleGroup() {
	g := proc.NewGroup(context.Background())
	g.Go(walk)
	g.Go(chewGum)
	if err := g.Wait(); err != nil {
		fmt.Println("error:", err)
	}
	// Unordered output:
	// walking
	// chewing gum
}

// Custom retry policy as a function wrapper.
func ExampleGroup_budgetRetry() {
	withRetries := func(n int, fn proc.Func) proc.Func {
		return func(ctx context.Context) error {
			for range n {
				if err := fn(ctx); err == nil {
					return nil
				}
			}
			return fmt.Errorf("gave up after %d attempts", n)
		}
	}

	var attempts atomic.Int64
	connect := func(context.Context) error {
		if attempts.Add(1) < 3 {
			return fmt.Errorf("transient error")
		}
		fmt.Println("connected on attempt", attempts.Load())
		return nil
	}

	g := proc.NewGroup(context.Background())
	g.Go(withRetries(5, connect))
	if err := g.Wait(); err != nil {
		fmt.Println("error:", err)
	}
	// Output: connected on attempt 3
}

// Groups can be nested: a Func running inside one Group can construct its own
// child Group to supervise further work. The child's lifetime is bound to the
// parent Func's context, so child processes cancel when the parent scope ends.
func ExampleGroup_nested() {
	parent := proc.NewGroup(context.Background())
	parent.Go(func(ctx context.Context) error {
		child := proc.NewGroup(ctx)
		child.Go(func(context.Context) error {
			fmt.Println("child process")
			return nil
		})
		return child.Wait()
	})
	if err := parent.Wait(); err != nil {
		fmt.Println("error:", err)
	}
	// Output: child process
}

// Func.Exec runs a function as a supervised process on the current goroutine,
// catching any panic and returning it as an error. For concurrent use, prefer
// [proc.Group] — a single Group.Go call gives the same panic recovery without
// manual goroutine management.
func ExampleFunc_Exec() {
	fn := proc.Func(func(context.Context) error {
		panic("something went wrong")
	})
	if err := fn.Exec(context.Background()); err != nil {
		fmt.Println("recovered:", err.Error()[:27])
	}
	// Output: recovered: panic: something went wrong
}

// A caller-site loop rebuilding the group on failure is a custom supervision
// strategy.
func Example_callerSupervisor() {
	var attempts atomic.Int64
	for attempts.Load() < 2 {
		g := proc.NewGroup(context.Background())
		g.Go(func(context.Context) error {
			n := attempts.Add(1)
			if n < 2 {
				return fmt.Errorf("crashed")
			}
			fmt.Println("succeeded on attempt", n)
			return nil
		})
		if err := g.Wait(); err != nil {
			continue
		}
	}
	// Output: succeeded on attempt 2
}

func TestExecReturnsError(t *testing.T) {
	want := fmt.Errorf("boom")
	var f proc.Func = func(context.Context) error { return want }
	if got := f.Exec(t.Context()); !errors.Is(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExecCatchesPanic(t *testing.T) {
	var f proc.Func = func(context.Context) error { panic("oops") }
	err := f.Exec(t.Context())
	if err == nil || !strings.Contains(err.Error(), "panic: oops") {
		t.Errorf("got %v, want panic error", err)
	}
}

func TestExecUnwrapsToError(t *testing.T) {
	var f proc.Func = func(context.Context) error { panic("typed") }
	err := f.Exec(t.Context())
	var pe *proc.Error
	if !errors.As(err, &pe) {
		t.Fatalf("errors.As failed: %v", err)
	}
	if pe.Value != "typed" {
		t.Errorf("Value = %v, want %q", pe.Value, "typed")
	}
	if len(pe.Stack) == 0 {
		t.Error("Stack is empty")
	}
}

func TestErrorUnwrapsToInnerError(t *testing.T) {
	sentinel := fmt.Errorf("db down")
	var f proc.Func = func(context.Context) error { panic(sentinel) }
	err := f.Exec(t.Context())
	if !errors.Is(err, sentinel) {
		t.Errorf("errors.Is: want true for sentinel, got false (err=%v)", err)
	}
}

func TestErrorUnwrapNonError(t *testing.T) {
	var f proc.Func = func(context.Context) error {
		panic("just a string")
	}
	err := f.Exec(t.Context())
	var pe *proc.Error
	if !errors.As(err, &pe) {
		t.Fatalf("errors.As: want *Error")
	}
	if pe.Unwrap() != nil {
		t.Errorf("Unwrap: want nil for non-error panic, got %v", pe.Unwrap())
	}
}

func TestZeroGroupGoPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Go on zero-value Group did not panic")
		}
		msg := r.(string)
		if !strings.Contains(msg, "proc: Group has no context") {
			t.Errorf("panic = %q, want proc: Group has no context", msg)
		}
	}()
	var g proc.Group
	g.Go(func(context.Context) error { return nil })
}

func TestZeroGroupWaitPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Wait on zero-value Group did not panic")
		}
		msg := r.(string)
		if !strings.Contains(msg, "proc: Group has no context") {
			t.Errorf("panic = %q, want proc: Group has no context", msg)
		}
	}()
	var g proc.Group
	_ = g.Wait()
}

func TestGoErrorSurfaces(t *testing.T) {
	g := proc.NewGroup(t.Context())
	want := fmt.Errorf("ouch")
	g.Go(func(context.Context) error { return want })
	if err := g.Wait(); !errors.Is(err, want) {
		t.Errorf("Wait: got %v, want %v", err, want)
	}
}

func TestGoPanicBecomesError(t *testing.T) {
	g := proc.NewGroup(t.Context())
	g.Go(func(context.Context) error { panic("oops") })
	err := g.Wait()
	if err == nil || !strings.Contains(err.Error(), "panic: oops") {
		t.Fatalf("Wait: got %v, want panic error", err)
	}
	var pe *proc.Error
	if !errors.As(err, &pe) {
		t.Fatalf("errors.As: want *Error, got %T", err)
	}
}

func TestGoCancelsSiblings(t *testing.T) {
	g := proc.NewGroup(t.Context())
	want := fmt.Errorf("first")
	started := make(chan struct{})

	g.Go(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return nil
	})
	g.Go(func(context.Context) error {
		<-started
		return want
	})

	if err := g.Wait(); !errors.Is(err, want) {
		t.Errorf("Wait: got %v, want %v", err, want)
	}
}

func TestGoFirstErrorWins(t *testing.T) {
	g := proc.NewGroup(t.Context())
	first := fmt.Errorf("first")

	g.Go(func(ctx context.Context) error {
		return first
	})
	g.Go(func(ctx context.Context) error {
		<-ctx.Done()
		return fmt.Errorf("second")
	})

	if err := g.Wait(); !errors.Is(err, first) {
		t.Errorf("Wait: got %v, want %v", err, first)
	}
}

func TestWatchRunsUntilCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	g := proc.NewGroup(ctx)
	looped := make(chan struct{})
	var iters atomic.Int64

	g.Go(proc.Watch(func(ctx context.Context) error {
		if iters.Add(1) == 2 {
			close(looped)
			<-ctx.Done()
		}
		return nil
	}))

	<-looped
	cancel()
	if err := g.Wait(); err != nil {
		t.Errorf("Wait: %v", err)
	}
}

func TestWatchRecoversPanics(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	g := proc.NewGroup(parent)
	recovered := make(chan struct{})
	var iters atomic.Int64

	g.Go(proc.Watch(func(context.Context) error {
		n := iters.Add(1)
		if n < 3 {
			panic("crash")
		}
		close(recovered)
		return nil
	}))

	<-recovered
	if parent.Err() != nil {
		t.Error("parent ctx canceled: Watch should not cancel")
	}

	cancel()
	if err := g.Wait(); err != nil {
		t.Errorf("Wait: %v", err)
	}
}

func TestWatchExitsOnGoError(t *testing.T) {
	g := proc.NewGroup(t.Context())
	started := make(chan struct{})
	want := fmt.Errorf("go failed")
	var watchExited atomic.Bool

	g.Go(proc.Watch(func(ctx context.Context) error {
		started <- struct{}{}
		<-ctx.Done()
		watchExited.Store(true)
		return ctx.Err()
	}))

	g.Go(func(context.Context) error {
		<-started
		return want
	})

	if err := g.Wait(); !errors.Is(err, want) {
		t.Errorf("Wait: got %v, want %v", err, want)
	}
	if !watchExited.Load() {
		t.Error("Watch did not exit after Go error")
	}
}

func TestWatchCancelsIterationContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	g := proc.NewGroup(ctx)

	var first context.Context
	second := make(chan struct{})
	var once sync.Once

	g.Go(proc.Watch(func(ctx context.Context) error {
		if first == nil {
			first = ctx
			return fmt.Errorf("force restart")
		}
		if first.Err() == nil {
			t.Error(
				"first iteration ctx not canceled " +
					"when second iteration began",
			)
		}
		once.Do(func() { close(second) })
		<-ctx.Done()
		return nil
	}))

	<-second
	cancel()
	if err := g.Wait(); err != nil {
		t.Errorf("Wait: %v", err)
	}
}

func TestExplicitCancelEndsDaemon(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	g := proc.NewGroup(ctx)

	started := make(chan struct{})
	var daemonRan atomic.Bool

	g.Go(proc.Watch(func(ctx context.Context) error {
		daemonRan.Store(true)
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}))

	g.Go(func(context.Context) error {
		<-started
		defer cancel()
		return nil
	})

	if err := g.Wait(); err != nil {
		t.Errorf("Wait: %v", err)
	}
	if !daemonRan.Load() {
		t.Error("daemon never ran")
	}
}

func TestCleanCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	g := proc.NewGroup(ctx)

	g.Go(func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})

	cancel()
	if err := g.Wait(); err != nil {
		t.Errorf("Wait: %v", err)
	}
}

func TestLoggerDefault(t *testing.T) {
	got := proc.Logger(t.Context())
	if got == nil {
		t.Fatal("Logger: returned nil")
	}
	if got != slog.Default() {
		t.Errorf(
			"Logger on bare ctx: got %p, want slog.Default %p",
			got, slog.Default(),
		)
	}
}

func TestWithLoggerRoundTrip(t *testing.T) {
	want := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := proc.WithLogger(t.Context(), want)
	got := proc.Logger(ctx)
	if got != want {
		t.Errorf("Logger: got %p, want %p", got, want)
	}
}

func TestWithLoggerNilIsNoop(t *testing.T) {
	ctx := proc.WithLogger(t.Context(), nil)
	if got := proc.Logger(ctx); got != slog.Default() {
		t.Errorf("Logger after WithLogger(nil): want slog.Default")
	}
}

func TestLoggerFlowsThroughGroup(t *testing.T) {
	want := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := proc.WithLogger(t.Context(), want)
	g := proc.NewGroup(ctx)

	var got *slog.Logger
	g.Go(func(ctx context.Context) error {
		got = proc.Logger(ctx)
		return nil
	})
	if err := g.Wait(); err != nil {
		t.Errorf("Wait: %v", err)
	}
	if got != want {
		t.Errorf("process saw %p, want %p", got, want)
	}
}

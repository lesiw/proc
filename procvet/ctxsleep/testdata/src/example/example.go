package example

import (
	"context"
	"time"
)

// bad: ctx-aware function calling time.Sleep.
func badWorker(ctx context.Context) {
	time.Sleep(time.Second) // want "time.Sleep inside a ctx-aware body"
}

// bad: nested closure that takes ctx.
func badClosure() {
	fn := func(ctx context.Context) {
		time.Sleep(time.Second) // want "time.Sleep inside a ctx-aware body"
	}
	fn(context.Background())
}

// ok: non-ctx function, Sleep is fine.
func okNoCtx() {
	time.Sleep(time.Second)
}

// ok: ctx-aware function using the idiomatic select.
func okWithSelect(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
	}
}

// ok: nested closure doesn't take ctx; outer function
// takes ctx but doesn't call Sleep.
func okOuter(ctx context.Context) {
	_ = ctx
	fn := func() {
		time.Sleep(time.Second)
	}
	fn()
}

//ignore:linelen
package example

import (
	"context"
)

// bad: takes ctx, never uses it.
func badIgnoresCtx(ctx context.Context) error { // want `context.Context parameter "ctx" is never referenced`
	return nil
}

// bad: method with unused ctx.
type server struct{}

func (s *server) Serve(ctx context.Context) error { // want `context.Context parameter "ctx" is never referenced`
	return nil
}

// bad: function literal with unused ctx.
func badClosure() {
	fn := func(ctx context.Context) error { // want `context.Context parameter "ctx" is never referenced`
		return nil
	}
	_ = fn
}

// ok: explicit opt-out via underscore.
func okUnderscore(_ context.Context) error {
	return nil
}

// ok: ctx is actually referenced.
func okUsed(ctx context.Context) error {
	return ctx.Err()
}

// ok: passed through to another call.
func okPassed(ctx context.Context) error {
	return downstream(ctx)
}

func downstream(ctx context.Context) error {
	return ctx.Err()
}

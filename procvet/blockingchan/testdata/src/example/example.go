package example

import "context"

// bad: bare send in ctx-aware function.
func badSend(ctx context.Context) {
	ch := make(chan int)
	ch <- 1 // want "bare channel send in ctx-aware body"
}

// bad: bare receive (discard) in ctx-aware function.
func badRecvDiscard(ctx context.Context) {
	ch := make(chan int)
	<-ch // want "bare channel receive in ctx-aware body"
}

// bad: bare receive (assign) in ctx-aware function.
func badRecvAssign(ctx context.Context) {
	ch := make(chan int)
	v := <-ch // want "bare channel receive in ctx-aware body"
	_ = v
}

// ok: inside a select — user is already thinking
// about blocking.
func okSelect(ctx context.Context) {
	ch := make(chan int)
	select {
	case ch <- 1:
	case <-ctx.Done():
	}
}

// ok: receive inside a select.
func okSelectRecv(ctx context.Context) {
	ch := make(chan int)
	select {
	case v := <-ch:
		_ = v
	case <-ctx.Done():
	}
}

// bad: for-range over channel blocks until close.
func badRange(ctx context.Context) {
	ch := make(chan int)
	for v := range ch { // want "for-range over a channel blocks"
		_ = v
	}
}

// ok: non-ctx function, no obligation to honor
// cancellation.
func okNoCtx() {
	ch := make(chan int)
	ch <- 1
}

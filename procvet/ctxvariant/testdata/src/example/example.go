//ignore:linelen
package example

import (
	"context"

	"fakehttp"
)

func bad(ctx context.Context) {
	_, _ = fakehttp.NewRequest("GET", "/") // want "call to NewRequest has a ctx-aware variant"
	_ = fakehttp.Command("echo")           // want "call to Command has a ctx-aware variant"

	c := &fakehttp.Client{}
	req := &fakehttp.Request{}
	_ = c.Do(req) // want "call to Do has a ctx-aware variant"
}

func good(ctx context.Context) {
	// The ctx-aware variants are fine.
	_, _ = fakehttp.NewRequestWithContext(ctx, "GET", "/")
	_ = fakehttp.CommandContext(ctx, "echo")

	c := &fakehttp.Client{}
	req := &fakehttp.Request{}
	_ = c.DoContext(ctx, req)

	// Functions with no variant aren't flagged.
	_, _ = fakehttp.Get("/")
	_ = c.Close()
}

// noCtx has no context in scope: flagged but not fixed.
func noCtx() {
	_, _ = fakehttp.NewRequest("GET", "/") // want "call to NewRequest has a ctx-aware variant"
}

// twoCtx has two context parameters: ambiguous, flagged but not fixed.
func twoCtx(ctx1, ctx2 context.Context) {
	_, _ = fakehttp.NewRequest("GET", "/") // want "call to NewRequest has a ctx-aware variant"
	_ = ctx1
	_ = ctx2
}

package example

import (
	"context"
	"log"

	"lesiw.io/proc"
)

// ok: Wait is called directly.
func goodWait() {
	g := proc.NewGroup(context.Background())
	g.Go(func(ctx context.Context) error { return nil })
	_ = g.Wait()
}

// ok: Wait inside an if-init.
func goodWaitInIfInit() {
	g := proc.NewGroup(context.Background())
	g.Go(func(ctx context.Context) error { return nil })
	if err := g.Wait(); err != nil {
		log.Print(err)
	}
}

// ok: group is returned to the caller.
func goodReturned() *proc.Group {
	g := proc.NewGroup(context.Background())
	g.Go(func(ctx context.Context) error { return nil })
	return g
}

// ok: group is passed to another function.
func goodPassed() {
	g := proc.NewGroup(context.Background())
	g.Go(func(ctx context.Context) error { return nil })
	handOff(g)
}

// ok: group is stored in a composite literal returned to
// the caller.
func goodStoredInLiteral() *holder {
	g := proc.NewGroup(context.Background())
	g.Go(func(ctx context.Context) error { return nil })
	return &holder{g: g}
}

// ok: Wait is reached via a deferred closure.
func goodDeferredWait() {
	g := proc.NewGroup(context.Background())
	defer func() { _ = g.Wait() }()
	g.Go(func(ctx context.Context) error { return nil })
}

// ok: retry loop pattern. g is redeclared each iteration
// with its own Wait inside the loop body.
func goodRetryLoop(ctx context.Context) {
	for ctx.Err() == nil {
		g := proc.NewGroup(ctx)
		g.Go(func(ctx context.Context) error { return nil })
		if err := g.Wait(); err != nil {
			continue
		}
		break
	}
}

// ok: stored into a struct field via selector.
func goodStoredInField() {
	h := &holder{}
	h.g = proc.NewGroup(context.Background())
	h.g.Go(func(ctx context.Context) error { return nil })
}

// bad: Wait is never called.
func badNoWait() {
	g := proc.NewGroup(context.Background()) // want `proc.NewGroup result "g" is never Wait'd`
	g.Go(func(ctx context.Context) error { return nil })
}

// bad: Only Go is called, no Wait.
func badOnlyGo() {
	g := proc.NewGroup(context.Background()) // want `proc.NewGroup result "g" is never Wait'd`
	g.Go(func(ctx context.Context) error { return nil })
	g.Go(func(ctx context.Context) error { return nil })
}

// bad: NewGroup inside a function literal with no Wait.
func badInClosure() {
	_ = func() {
		g := proc.NewGroup(context.Background()) // want `proc.NewGroup result "g" is never Wait'd`
		g.Go(func(ctx context.Context) error { return nil })
	}
}

// bad: two groups in one function, only one is Waited.
func badTwoGroupsOneWaited() {
	g1 := proc.NewGroup(context.Background())
	g1.Go(func(ctx context.Context) error { return nil })
	_ = g1.Wait()

	g2 := proc.NewGroup(context.Background()) // want `proc.NewGroup result "g2" is never Wait'd`
	g2.Go(func(ctx context.Context) error { return nil })
}

type holder struct {
	g *proc.Group
}

func handOff(*proc.Group) {}

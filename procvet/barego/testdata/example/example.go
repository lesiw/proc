package example

import (
	"context"
	"log"

	"lesiw.io/proc"
)

// ok: go someFunc.Exec(ctx) executes as a supervised process.
func goodGoExec() {
	g := proc.NewGroup(context.Background())
	g.Go(func(ctx context.Context) error {
		var inner proc.Func = func(ctx context.Context) error {
			doWork(ctx)
			return nil
		}
		go inner.Exec(ctx)
		return nil
	})
	_ = g.Wait()
}

// ok: go with inline deferred recover.
func goodDeferredRecover() {
	g := proc.NewGroup(context.Background())
	g.Go(func(ctx context.Context) error {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("recovered: %v", r)
				}
			}()
			doWork(ctx)
		}()
		return nil
	})
	_ = g.Wait()
}

// bad: bare go inside a Group.Go worker.
func bareGoInWorker() {
	g := proc.NewGroup(context.Background())
	g.Go(func(ctx context.Context) error {
		go doWork(ctx) // want "bare go statement inside a proc.Func"
		return nil
	})
	_ = g.Wait()
}

// bad: bare go inside a Watch-wrapped worker.
func bareGoInWatch() {
	g := proc.NewGroup(context.Background())
	g.Go(proc.Watch(func(ctx context.Context) error {
		go doWork(ctx) // want "bare go statement inside a proc.Func"
		return nil
	}))
	_ = g.Wait()
}

// bad: bare go inside a function transitively called from a
// worker. The diagnostic reports the chain.
func bareGoTransitive() {
	g := proc.NewGroup(context.Background())
	g.Go(worker)
	_ = g.Wait()
}

func worker(ctx context.Context) error {
	helper(ctx)
	return nil
}

func helper(ctx context.Context) {
	go doWork(ctx) // want "bare go statement inside a proc.Func"
}

// bad: deferred function that re-panics isn't actually recovering.
func bareGoRepanic() {
	g := proc.NewGroup(context.Background())
	g.Go(func(ctx context.Context) error {
		go func() { // want "bare go statement inside a proc.Func"
			defer func() {
				if r := recover(); r != nil {
					panic(r)
				}
			}()
			doWork(ctx)
		}()
		return nil
	})
	_ = g.Wait()
}

// ok: bare go OUTSIDE any proc.Func is not flagged. barego only
// fires on code reachable from a Group.Go or Watch entry point.
func unrelatedBareGo() {
	go doWork(context.Background())
}

func doWork(context.Context) {}

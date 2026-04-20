package example

import (
	"context"
	"log"
	"os"
	"runtime"
	"syscall"
)

// bad: ctx-aware function calling os.Exit.
func badOsExit(ctx context.Context) {
	_ = ctx
	os.Exit(1) // want "os.Exit inside a ctx-aware body"
}

// bad: ctx-aware function calling runtime.Goexit.
func badGoexit(ctx context.Context) {
	_ = ctx
	runtime.Goexit() // want "runtime.Goexit inside a ctx-aware body"
}

// bad: ctx-aware function calling log.Fatal.
func badLogFatal(ctx context.Context) {
	_ = ctx
	log.Fatal("boom") // want "log.Fatal inside a ctx-aware body"
}

// bad: ctx-aware function calling log.Fatalf.
func badLogFatalf(ctx context.Context) {
	_ = ctx
	log.Fatalf("boom %d", 1) // want "log.Fatalf inside a ctx-aware body"
}

// bad: ctx-aware function calling syscall.Exit.
func badSyscallExit(ctx context.Context) {
	_ = ctx
	syscall.Exit(1) // want "syscall.Exit inside a ctx-aware body"
}

// ok: non-ctx function can use os.Exit freely.
// This is the typical main() shape.
func main2() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error { return nil }

// ok: ctx-aware function using panic — still caught
// by proc's protected call.
func okPanic(ctx context.Context) {
	_ = ctx
	if false {
		panic("recoverable")
	}
}

// ok: log.Panic calls panic, which IS recoverable.
func okLogPanic(ctx context.Context) {
	_ = ctx
	if false {
		log.Panic("recoverable")
	}
}

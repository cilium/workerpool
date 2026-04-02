# Workerpool

[![Go Reference](https://pkg.go.dev/badge/github.com/cilium/workerpool.svg)](https://pkg.go.dev/github.com/cilium/workerpool)
[![CI](https://github.com/cilium/workerpool/workflows/Tests/badge.svg)](https://github.com/cilium/workerpool/actions?query=workflow%3ATests)
[![Go Report Card](https://goreportcard.com/badge/github.com/cilium/workerpool)](https://goreportcard.com/report/github.com/cilium/workerpool)

Package workerpool implements a concurrency limiting worker pool. Worker
routines are spawned on demand as tasks are submitted; up to the configured
limit of concurrent workers.

When the limit of concurrently running workers is reached, submitting a task
blocks until a worker is able to pick it up. This behavior is intentional as it
prevents from accumulating tasks which could grow unbounded. Therefore, it is
the responsibility of the caller to queue up tasks if that's the intended
behavior.

One caveat is that while the number of concurrently running workers is limited,
task results are not and they accumulate until they are collected. Therefore,
if a large number of tasks can be expected, the workerpool should be
periodically drained (e.g. every 10k tasks). Alternatively,
`WithResultCallback` can be used to process results as they complete, avoiding
accumulation entirely.

This package is mostly useful when tasks are CPU bound and spawning too many
routines would be detrimental to performance. It features a straightforward API
and no external dependencies. See the sections below for usage examples.

## Example with Drain

```go
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/cilium/workerpool"
)

// IsPrime returns true if n is prime, false otherwise.
func IsPrime(n int64) bool {
	if n < 2 {
		return false
	}
	for p := int64(2); p*p <= n; p++ {
		if n%p == 0 {
			return false
		}
	}
	return true
}

func main() {
	wp := workerpool.New(runtime.NumCPU())
	// Defer Close to ensure cleanup on early return (e.g., errors during Submit).
	// Close sends cancellation to running tasks and waits for them to complete.
	// It's safe to call Close multiple times; subsequent calls return ErrClosed.
	defer func() { _ = wp.Close() }()

	for i, n := 0, int64(1_000_000_000_000_000_000); i < 100; i, n = i+1, n+1 {
		id := fmt.Sprintf("task #%d", i)
		// Use Submit to submit tasks for processing. Submit blocks when no
		// worker is available to pick up the task.
		err := wp.Submit(id, func(_ context.Context) error {
			fmt.Println("isprime", n)
			if IsPrime(n) {
				fmt.Println(n, "is prime!")
			}
			return nil
		})
		// Submit fails when the pool is closed (ErrClosed), being drained
		// (ErrDraining), or the parent context is done (context.Canceled).
		// Check for the error when appropriate.
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
	}

	// Drain prevents submitting new tasks and blocks until all submitted tasks
	// complete.
	tasks, err := wp.Drain()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}

	// Iterating over the results is useful if non-nil errors can be expected.
	for _, task := range tasks {
		// Err returns the error that the task returned after execution.
		if err := task.Err(); err != nil {
			fmt.Println("task", task, "failed:", err)
		}
	}

	// Close is called here explicitly to check for errors. The deferred Close
	// will also run but returns ErrClosed (which we can ignore on defer).
	if err := wp.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
```

## Example with result callback

Use `WithResultCallback` to process each result as it completes rather than
accumulating them for a later `Drain` call. The callback receives a `Result`,
which extends `Task` with a `Duration()` method reporting how long the task
took to execute. This is useful for logging, metrics, or long-running pools
where unbounded result accumulation is undesirable.

```go
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/cilium/workerpool"
)

func main() {
	wp := workerpool.New(runtime.NumCPU(), workerpool.WithResultCallback(func(r workerpool.Result) {
		if err := r.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "task %s failed after %s: %v\n", r, r.Duration(), err)
		} else {
			fmt.Printf("task %s completed in %s\n", r, r.Duration())
		}
	}))
	defer func() { _ = wp.Close() }()

	for i, n := 0, int64(1_000_000_000_000_000_000); i < 100; i, n = i+1, n+1 {
		id := fmt.Sprintf("task #%d", i)
		err := wp.Submit(id, func(_ context.Context) error {
			if IsPrime(n) {
				fmt.Println(n, "is prime!")
			}
			return nil
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
	}

	// Close waits for all in-flight tasks to complete before returning,
	// ensuring all callback invocations have finished.
	if err := wp.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
```

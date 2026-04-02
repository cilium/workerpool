// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package workerpool_test

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/cilium/workerpool"
)

// IsPrime returns true if n is prime, false otherwise.
func IsPrime(ctx context.Context, n int64) bool {
	if n < 2 {
		return false
	}
	for p := int64(2); p*p <= n; p++ {
		// Check for cancellation periodically (every 10000 iterations)
		if p%10000 == 0 {
			select {
			case <-ctx.Done():
				return false
			default:
			}
		}
		if n%p == 0 {
			return false
		}
	}
	return true
}

// Example demonstrates basic usage of a worker pool with Drain and Close.
func Example() {
	wp := workerpool.New(runtime.NumCPU())
	// Defer Close to ensure cleanup on early return (e.g., errors during Submit).
	// Close sends cancellation to running tasks and waits for them to complete.
	// It's safe to call Close multiple times; subsequent calls return [ErrClosed].
	defer func() { _ = wp.Close() }()

	for i, n := 0, int64(1_000_000_000_000_000_000); i < 100; i, n = i+1, n+1 {
		id := fmt.Sprintf("task #%d", i)
		// Use Submit to submit tasks for processing. Submit blocks when no
		// worker is available to pick up the task.
		err := wp.Submit(id, func(ctx context.Context) error {
			fmt.Println("isprime", n)
			if IsPrime(ctx, n) {
				fmt.Println(n, "is prime!")
			}
			return nil
		})
		// Submit fails when the pool is closed ([ErrClosed]), being drained
		// ([ErrDraining]), or the parent context is done ([context.Canceled]).
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
	// will also run but returns [ErrClosed] (which we can ignore on defer).
	if err := wp.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

// ExampleWithResultCallback demonstrates using a result callback to process
// task results immediately without accumulation.
func ExampleWithResultCallback() {
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
		err := wp.Submit(id, func(ctx context.Context) error {
			if IsPrime(ctx, n) {
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

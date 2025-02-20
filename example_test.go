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

func Example() {
	wp := workerpool.New(runtime.NumCPU())
	for i, n := 0, int64(1_000_000_000_000_000_000); n < 1_000_000_000_000_000_100; i, n = i+1, n+1 {
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
		// Submit fails when the pool is closed (ErrClosed) or being drained
		// (ErrDrained). Check for the error when appropriate.
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

	// Close should be called once the worker pool is no longer necessary.
	if err := wp.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

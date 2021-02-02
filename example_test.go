// Copyright 2021 Authors of Cilium
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package workerpool

import (
	"fmt"
	"os"
	"runtime"
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
	wp := New(runtime.NumCPU())
	for i, n := 0, int64(1_000_000_000_000_000_000); n < 1_000_000_000_000_000_100; i, n = i+1, n+1 {
		n := n // https://golang.org/doc/faq#closures_and_goroutines
		id := fmt.Sprintf("task #%d", i)
		// Use Submit to submit tasks for processing. Submit blocks when no
		// worker is available to pick up the task.
		err := wp.Submit(id, func() error {
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

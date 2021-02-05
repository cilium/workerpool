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
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestWorkerPool(t *testing.T) {
	n := runtime.NumCPU()
	wp := New(n)
	if c := wp.Cap(); c != n {
		t.Fatalf("workers channel capacity: got '%d', want '%d'", c, n)
	}
	if c := cap(wp.tasks); c != 0 {
		t.Fatalf("tasks channel capacity: got '%d', want an unbuffered channel", c)
	}

	numTasks := n + 2
	done := make(chan struct{})
	// working is used to ensure that n routines are dispatched at a given time
	working := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(numTasks - 1)
	for i := 0; i < numTasks-1; i++ {
		id := fmt.Sprintf("task #%2d", i)
		err := wp.Submit(id, func() error {
			defer wg.Done()
			working <- struct{}{}
			done <- struct{}{}
			return nil
		})
		if err != nil {
			t.Errorf("failed to submit task '%s': %v", id, err)
		}
	}

	// ensure n workers are busy
	for i := 0; i < n; i++ {
		<-working
	}

	// the n workers are busy so submitting a new task should block
	ready := make(chan struct{})
	sc := make(chan struct{})
	wg.Add(1)
	go func() {
		id := fmt.Sprintf("task #%2d", numTasks-1)
		ready <- struct{}{}
		wp.Submit(id, func() error {
			defer wg.Done()
			done <- struct{}{}
			return nil
		})
		sc <- struct{}{}
	}()

	<-ready
	select {
	case <-sc:
		t.Errorf("submit should be blocking")
	case <-time.After(100 * time.Millisecond):
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		ready <- struct{}{}
		results, err := wp.Drain()
		if err != nil {
			t.Errorf("draining failed: %v", err)
		}
		if len(results) != numTasks {
			t.Errorf("missing tasks results: got '%d', want '%d'", len(results), numTasks)
		}
		for i, r := range results {
			id := fmt.Sprintf("task #%2d", i)
			if s := r.String(); s != id {
				t.Errorf("String: got '%s', want '%s'", s, id)
			}
			if err := r.Err(); err != nil {
				t.Errorf("Err: got '%v', want no error", err)
			}
		}
	}()

	<-ready
	// un-block the worker routines
	for i := 0; i < numTasks-1; i++ {
		<-done
	}
	// The last task was blocked in wp.run() and not yet scheduled on a worker.
	// Now that some workers are free, the task should have been picked up.
	<-working
	<-done

	wg.Wait()

	if err := wp.Close(); err != nil {
		t.Errorf("close: got '%v', want no error", err)
	}

	if err := wp.Submit("", nil); err != ErrClosed {
		t.Errorf("submit: got '%v', want '%v'", err, ErrClosed)
	}

	results, err := wp.Drain()
	if err != ErrClosed {
		t.Errorf("drain: got '%v', want '%v'", err, ErrClosed)
	}
	if results != nil {
		t.Errorf("drain: got '%v', want '%v'", results, nil)
	}
}

func TestConcurrentDrain(t *testing.T) {
	n := runtime.NumCPU()
	wp := New(n)

	numTasks := n + 1
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(numTasks)
	for i := 0; i < numTasks; i++ {
		id := fmt.Sprintf("task #%2d", i)
		err := wp.Submit(id, func() error {
			defer wg.Done()
			done <- struct{}{}
			return nil
		})
		if err != nil {
			t.Errorf("failed to submit task '%s': %v", id, err)
		}
	}

	wg.Add(1)
	ready := make(chan struct{})
	go func() {
		defer wg.Done()
		ready <- struct{}{}
		results, err := wp.Drain()
		if err != nil {
			t.Errorf("draining failed: %v", err)
		}
		if len(results) != numTasks {
			t.Errorf("missing tasks results: got '%d', want '%d'", len(results), numTasks)
		}
		for i, r := range results {
			id := fmt.Sprintf("task #%2d", i)
			if s := r.String(); s != id {
				t.Errorf("String: got '%s', want '%s'", s, id)
			}
			if err := r.Err(); err != nil {
				t.Errorf("Err: got '%v', want no error", err)
			}
		}
	}()

	// make sure that drain is already called by the other routine
	<-ready
	time.Sleep(10 * time.Millisecond)

	if err := wp.Submit("", nil); err != ErrDraining {
		t.Errorf("submit: got '%v', want '%v'", err, ErrDraining)
	}

	results, err := wp.Drain()
	if err != ErrDraining {
		t.Errorf("drain: got '%v', want '%v'", err, ErrDraining)
	}
	if results != nil {
		t.Errorf("drain: got '%v', want '%v'", results, nil)
	}

	// un-block the worker routines to allow the test to complete
	for i := 0; i < numTasks; i++ {
		<-done
	}

	if err := wp.Close(); err != nil {
		t.Errorf("close: got '%v', want no error", err)
	}

	wg.Wait()
}

func TestWorkerPoolCap(t *testing.T) {
	one := New(1)
	defer one.Close()
	if c := one.Cap(); c != 1 {
		t.Errorf("got %d; want %d", c, 1)
	}

	n := runtime.NumCPU()
	ncpu := New(n)
	defer ncpu.Close()
	if c := ncpu.Cap(); c != n {
		t.Errorf("got %d; want %d", c, n)
	}

	fortyTwo := New(42)
	defer fortyTwo.Close()
	if c := fortyTwo.Cap(); c != 42 {
		t.Errorf("got %d; want %d", c, 42)
	}
}

func TestWorkerPoolManyClose(t *testing.T) {
	wp := New(runtime.NumCPU())

	// first call to Close() should not return an error.
	if err := wp.Close(); err != nil {
		t.Fatalf("unexpected error on Close(): %s", err)
	}

	// calling Close() more than once should always return an error.
	if err := wp.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("got %s; want %s", err, ErrClosed)
	}
	if err := wp.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("got %s; want %s", err, ErrClosed)
	}
}

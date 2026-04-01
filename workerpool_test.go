// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package workerpool_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/cilium/workerpool"
)

var errTask = errors.New("task error")

func TestWorkerPoolNewPanics(t *testing.T) {
	// helper expecting New(n) to panic.
	testWorkerPoolNewPanics := func(n int) {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("New(%d) should panic()", n)
			}
		}()
		_ = workerpool.New(n)
	}

	testWorkerPoolNewPanics(0)
	testWorkerPoolNewPanics(-1)
}

func TestWithResultCallbackNilPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("WithResultCallback(nil) should panic()")
		}
	}()
	workerpool.WithResultCallback(nil)
}

func TestWorkerPoolTasksCapacity(t *testing.T) {
	wp := workerpool.New(runtime.NumCPU())
	defer func() {
		if err := wp.Close(); err != nil {
			t.Errorf("close: got '%v', want no error", err)
		}
	}()

	if c := wp.TasksCap(); c != 0 {
		t.Errorf("tasks channel capacity is %d; want 0 (an unbuffered channel)", c)
	}
}

func TestWorkerPoolCap(t *testing.T) {
	one := workerpool.New(1)
	defer func() {
		if err := one.Close(); err != nil {
			t.Errorf("close: got '%v', want no error", err)
		}
	}()
	if c := one.Cap(); c != 1 {
		t.Errorf("got %d; want %d", c, 1)
	}

	n := runtime.NumCPU()
	ncpu := workerpool.New(n)
	defer func() {
		if err := ncpu.Close(); err != nil {
			t.Errorf("close: got '%v', want no error", err)
		}
	}()
	if c := ncpu.Cap(); c != n {
		t.Errorf("got %d; want %d", c, n)
	}

	fortyTwo := workerpool.New(42)
	defer func() {
		if err := fortyTwo.Close(); err != nil {
			t.Errorf("close: got '%v', want no error", err)
		}
	}()
	if c := fortyTwo.Cap(); c != 42 {
		t.Errorf("got %d; want %d", c, 42)
	}
}

func TestWorkerPoolLen(t *testing.T) {
	wp := workerpool.New(1)
	if l := wp.Len(); l != 0 {
		t.Errorf("got %d; want %d", l, 0)
	}

	submitted := make(chan struct{})
	err := wp.Submit("", func(ctx context.Context) error {
		close(submitted)
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil {
		t.Fatalf("failed to submit task: %v", err)
	}

	<-submitted
	if l := wp.Len(); l != 1 {
		t.Errorf("got %d; want %d", l, 1)
	}

	if err := wp.Close(); err != nil {
		t.Fatalf("close: got '%v', want no error", err)
	}
	if l := wp.Len(); l != 0 {
		t.Errorf("got %d; want %d", l, 0)
	}
}

// TestWorkerPoolConcurrentTasksCount ensure that there is at least, but no
// more than n workers running in the pool when more than n tasks are
// submitted.
func TestWorkerPoolConcurrentTasksCount(t *testing.T) {
	n := runtime.NumCPU()
	wp := workerpool.New(n)
	defer func() {
		if err := wp.Close(); err != nil {
			t.Errorf("close: got '%v', want no error", err)
		}
	}()

	// working is written to by each task as soon as possible.
	working := make(chan struct{})
	// NOTE: schedule one more task than we have workers, hence n+1.
	for i := range n + 1 {
		id := fmt.Sprintf("task #%2d", i)
		err := wp.Submit(id, func(ctx context.Context) error {
			select {
			case working <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
			<-ctx.Done()
			return nil
		})
		if err != nil {
			t.Fatalf("failed to submit task '%s': %v", id, err)
		}
	}

	// ensure that n workers are busy.
	for i := range n {
		select {
		case <-working:
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("got %d tasks running; want %d", i, n)
		}
	}

	// ensure that one task is not scheduled, as all workers should now be
	// waiting on the context.
	select {
	case <-working:
		t.Fatalf("got %d tasks running; want %d", n+1, n)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWorkerPool(t *testing.T) {
	n := runtime.NumCPU()
	wp := workerpool.New(n)

	numTasks := n + 2
	done := make(chan struct{})
	// working is used to ensure that n routines are dispatched at a given time
	working := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(numTasks - 1)
	for i := range numTasks - 1 {
		id := fmt.Sprintf("task #%2d", i)
		err := wp.Submit(id, func(_ context.Context) error {
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
	for range n {
		<-working
	}

	// the n workers are busy so submitting a new task should block
	ready := make(chan struct{})
	sc := make(chan struct{})
	wg.Add(1)
	go func() {
		id := fmt.Sprintf("task #%2d", numTasks-1)
		ready <- struct{}{}
		err := wp.Submit(id, func(_ context.Context) error {
			defer wg.Done()
			done <- struct{}{}
			return nil
		})
		if err != nil {
			t.Errorf("failed to submit task '%s': %v", id, err)
		}
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
	for range numTasks - 1 {
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
}

func TestConcurrentDrain(t *testing.T) {
	n := runtime.NumCPU()
	wp := workerpool.New(n)

	numTasks := n + 1
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(numTasks)
	for i := range numTasks {
		id := fmt.Sprintf("task #%2d", i)
		err := wp.Submit(id, func(_ context.Context) error {
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

	if err := wp.Submit("", nil); !errors.Is(err, workerpool.ErrDraining) {
		t.Errorf("submit: got '%v', want '%v'", err, workerpool.ErrDraining)
	}

	results, err := wp.Drain()
	if !errors.Is(err, workerpool.ErrDraining) {
		t.Errorf("drain: got '%v', want '%v'", err, workerpool.ErrDraining)
	}
	if results != nil {
		t.Errorf("drain: got '%v', want '%v'", results, nil)
	}

	// un-block the worker routines to allow the test to complete
	for range numTasks {
		<-done
	}

	wg.Wait()

	results, err = wp.Drain()
	if err != nil {
		t.Errorf("drain: got '%v', want '%v'", err, nil)
	}
	if len(results) != 0 {
		t.Errorf("drain: unexpectedly got '%d' results", len(results))
	}

	if err := wp.Close(); err != nil {
		t.Errorf("close: got '%v', want no error", err)
	}
}

func TestWorkerPoolDrainAfterClose(t *testing.T) {
	wp := workerpool.New(runtime.NumCPU())
	if err := wp.Close(); err != nil {
		t.Fatalf("close: got '%v', want no error", err)
	}
	tasks, err := wp.Drain()
	if !errors.Is(err, workerpool.ErrClosed) {
		t.Errorf("got %v; want %v", err, workerpool.ErrClosed)
	}
	if tasks != nil {
		t.Errorf("got %v as tasks; want %v", tasks, nil)
	}
}

func TestWorkerPoolDrainAfterCloseWithCallback(t *testing.T) {
	wp := workerpool.New(runtime.NumCPU(), workerpool.WithResultCallback(func(workerpool.Result) {}))
	if err := wp.Close(); err != nil {
		t.Fatalf("close: got '%v', want no error", err)
	}
	// ErrClosed must take precedence over ErrCallbackSet.
	tasks, err := wp.Drain()
	if !errors.Is(err, workerpool.ErrClosed) {
		t.Errorf("got %v; want %v", err, workerpool.ErrClosed)
	}
	if tasks != nil {
		t.Errorf("got %v as tasks; want %v", tasks, nil)
	}
}

func TestWorkerPoolSubmitNil(t *testing.T) {
	wp := workerpool.New(runtime.NumCPU())
	defer func() {
		if err := wp.Close(); err != nil {
			t.Errorf("close: got '%v', want no error", err)
		}
	}()
	id := "nothing"
	if err := wp.Submit(id, nil); err != nil {
		t.Fatalf("got %v; want no error", err)
	}
	tasks, err := wp.Drain()
	if err != nil {
		t.Errorf("got %v; want no error", err)
	}
	if n := len(tasks); n != 1 {
		t.Errorf("got %v tasks; want 1", n)
	}
	r := tasks[0]
	if s := r.String(); s != id {
		t.Errorf("String: got '%s', want '%s'", s, id)
	}
	if err := r.Err(); err != nil {
		t.Errorf("Err: got '%v', want no error", err)
	}

}

func TestWorkerPoolSubmitNilWithCallback(t *testing.T) {
	id := "nothing"
	var got workerpool.Result
	wp := workerpool.New(runtime.NumCPU(), workerpool.WithResultCallback(func(r workerpool.Result) {
		got = r
	}))
	if err := wp.Submit(id, nil); err != nil {
		t.Fatalf("got %v; want no error", err)
	}
	if err := wp.Close(); err != nil {
		t.Fatalf("close: got '%v', want no error", err)
	}
	if got == nil {
		t.Fatal("callback was not invoked")
	}
	if s := got.String(); s != id {
		t.Errorf("String: got '%s', want '%s'", s, id)
	}
	if err := got.Err(); err != nil {
		t.Errorf("Err: got '%v', want no error", err)
	}
	if got.Duration() < 0 {
		t.Errorf("Duration: got %v, want >= 0", got.Duration())
	}
}

func TestWorkerPoolSubmitAfterClose(t *testing.T) {
	wp := workerpool.New(runtime.NumCPU())
	if err := wp.Close(); err != nil {
		t.Fatalf("close: got '%v', want no error", err)
	}
	if err := wp.Submit("dummy", nil); !errors.Is(err, workerpool.ErrClosed) {
		t.Fatalf("got %v; want %v", err, workerpool.ErrClosed)
	}
}

func TestWorkerPoolManyClose(t *testing.T) {
	wp := workerpool.New(runtime.NumCPU())

	// first call to Close() should not return an error.
	if err := wp.Close(); err != nil {
		t.Fatalf("unexpected error on Close(): %s", err)
	}

	// calling Close() more than once should always return an error.
	if err := wp.Close(); !errors.Is(err, workerpool.ErrClosed) {
		t.Fatalf("got %v; want %v", err, workerpool.ErrClosed)
	}
	if err := wp.Close(); !errors.Is(err, workerpool.ErrClosed) {
		t.Fatalf("got %v; want %v", err, workerpool.ErrClosed)
	}
}

func TestWorkerPoolClose(t *testing.T) {
	n := runtime.NumCPU()
	wp := workerpool.New(n)

	// working is written to by each task as soon as possible.
	working := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		id := fmt.Sprintf("task #%2d", i)
		err := wp.Submit(id, func(ctx context.Context) error {
			working <- struct{}{}
			<-ctx.Done()
			wg.Done()
			return ctx.Err()
		})
		if err != nil {
			t.Errorf("failed to submit task '%s': %v", id, err)
		}
	}

	// ensure n workers are busy
	for range n {
		<-working
	}

	if err := wp.Close(); err != nil {
		t.Errorf("close: got '%v', want no error", err)
	}
	wg.Wait() // all routines should have returned
}

func TestWorkerPoolNewWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	n := runtime.NumCPU()
	wp := workerpool.NewWithContext(ctx, n)

	// working is written to by each task as soon as possible.
	working := make(chan struct{})
	var wg sync.WaitGroup
	// Create n tasks waiting on the context to be cancelled.
	wg.Add(n)
	for i := range n {
		id := fmt.Sprintf("task #%2d", i)
		err := wp.Submit(id, func(ctx context.Context) error {
			working <- struct{}{}
			<-ctx.Done()
			wg.Done()
			return ctx.Err()
		})
		if err != nil {
			t.Errorf("failed to submit task '%s': %v", id, err)
		}
	}

	// ensure n workers are busy
	for range n {
		<-working
	}

	// cancel the parent context, which should complete all tasks.
	cancel()
	wg.Wait()

	// Submitting a task once the parent context has been cancelled should
	// return context.Canceled and not submit the task. Call Submit twice to
	// ensure the mutex is released on this path (a missing unlock would
	// deadlock the second call).
	for range 2 {
		err := wp.Submit("last", nil)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("submit after parent cancel: got %v, want %v", err, context.Canceled)
		}
	}

	// Drain should return only the n tasks that completed, not the rejected ones.
	results, err := wp.Drain()
	if err != nil {
		t.Errorf("drain: got '%v', want no error", err)
	}
	if len(results) != n {
		t.Errorf("drain: got %d results, want %d", len(results), n)
	}

	if err := wp.Close(); err != nil {
		t.Errorf("close: got '%v', want no error", err)
	}
}

func TestWorkerPoolNewWithCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before creating the pool

	wp := workerpool.NewWithContext(ctx, runtime.NumCPU())
	defer func() {
		if err := wp.Close(); err != nil {
			t.Errorf("close: got '%v', want no error", err)
		}
	}()

	// Submit should return context.Canceled immediately. Call twice to verify
	// the mutex is released on this path.
	for range 2 {
		err := wp.Submit("task", nil)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("submit with cancelled context: got %v, want %v", err, context.Canceled)
		}
	}

	// No tasks should have been queued.
	results, err := wp.Drain()
	if err != nil {
		t.Errorf("drain: got '%v', want no error", err)
	}
	if len(results) != 0 {
		t.Errorf("drain: got %d results, want 0", len(results))
	}
}

func TestWorkerPoolWithResultCallback(t *testing.T) {
	n := runtime.NumCPU()

	var mu sync.Mutex
	var got []workerpool.Result

	wp := workerpool.New(n, workerpool.WithResultCallback(func(r workerpool.Result) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, r)
	}))

	numTasks := n + 2
	wantErr := errTask
	for i := range numTasks {
		id := fmt.Sprintf("task #%2d", i)
		var f func(context.Context) error
		if i == 0 {
			f = func(_ context.Context) error { return wantErr }
		} else {
			f = func(_ context.Context) error { return nil }
		}
		if err := wp.Submit(id, f); err != nil {
			t.Fatalf("failed to submit task '%s': %v", id, err)
		}
	}

	// Drain must return ErrCallbackSet.
	tasks, err := wp.Drain()
	if !errors.Is(err, workerpool.ErrCallbackSet) {
		t.Errorf("drain: got %v, want %v", err, workerpool.ErrCallbackSet)
	}
	if tasks != nil {
		t.Errorf("drain: got %v, want nil", tasks)
	}

	// Close waits for all in-flight tasks, so after it returns all callbacks
	// have been invoked.
	if err := wp.Close(); err != nil {
		t.Fatalf("close: got '%v', want no error", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(got) != numTasks {
		t.Fatalf("callback: got %d results, want %d", len(got), numTasks)
	}
	for _, r := range got {
		if r.Duration() < 0 {
			t.Errorf("%s: Duration: got %v, want >= 0", r, r.Duration())
		}
		if r.String() == "task # 0" {
			if !errors.Is(r.Err(), wantErr) {
				t.Errorf("%s: Err: got %v, want %v", r, r.Err(), wantErr)
			}
		} else {
			if r.Err() != nil {
				t.Errorf("%s: Err: got %v, want nil", r, r.Err())
			}
		}
	}
}

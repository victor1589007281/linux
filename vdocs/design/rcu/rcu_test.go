package rcu

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBasicRCU(t *testing.T) {
	r := New()
	r.Start()
	defer r.Stop()

	// Test read lock/unlock
	r.ReadLock()
	r.ReadUnlock()

	stats := r.Stats()
	if stats.ReadLocks != 1 || stats.ReadUnlocks != 1 {
		t.Errorf("Expected 1 lock and 1 unlock, got %d/%d", stats.ReadLocks, stats.ReadUnlocks)
	}
}

func TestNestedReadLocks(t *testing.T) {
	r := New()
	r.Start()
	defer r.Stop()

	// Nested locks
	r.ReadLock()
	r.ReadLock()
	r.ReadLock()
	r.ReadUnlock()
	r.ReadUnlock()
	r.ReadUnlock()

	stats := r.Stats()
	if stats.ReadLocks != 3 || stats.ReadUnlocks != 3 {
		t.Errorf("Expected 3 locks and 3 unlocks, got %d/%d", stats.ReadLocks, stats.ReadUnlocks)
	}
}

func TestSynchronize(t *testing.T) {
	r := New(WithGracePeriod(time.Millisecond * 5))
	r.Start()
	defer r.Stop()

	// Simple synchronize
	start := time.Now()
	r.Synchronize()
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("Synchronize took too long: %v", elapsed)
	}
}

func TestCallRCU(t *testing.T) {
	r := New(WithGracePeriod(time.Millisecond * 5))
	r.Start()
	defer r.Stop()

	var called int32
	done := make(chan struct{})

	head := &RCUHead{}
	r.CallRCU(head, func(h *RCUHead) {
		atomic.StoreInt32(&called, 1)
		close(done)
	})

	select {
	case <-done:
		// OK
	case <-time.After(time.Second):
		t.Error("Callback was not invoked within timeout")
	}

	if atomic.LoadInt32(&called) != 1 {
		t.Error("Callback was not called")
	}
}

func TestConcurrentReaders(t *testing.T) {
	r := New(WithGracePeriod(time.Millisecond * 5))
	r.Start()
	defer r.Stop()

	var wg sync.WaitGroup
	numReaders := 10
	iterations := 100

	// Shared data
	data := &struct{ value int }{value: 42}
	var dataPtr atomic.Pointer[struct{ value int }]
	dataPtr.Store(data)

	// Start readers
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				r.ReadLock()
				p := dataPtr.Load()
				if p != nil {
					_ = p.value
				}
				r.ReadUnlock()
			}
		}()
	}

	wg.Wait()

	stats := r.Stats()
	expectedOps := uint64(numReaders * iterations)
	if stats.ReadLocks != expectedOps || stats.ReadUnlocks != expectedOps {
		t.Errorf("Expected %d locks/unlocks, got %d/%d", expectedOps, stats.ReadLocks, stats.ReadUnlocks)
	}
}

func TestConcurrentReadersAndWriter(t *testing.T) {
	r := New(WithGracePeriod(time.Millisecond * 2))
	r.Start()
	defer r.Stop()

	type Data struct {
		value int
	}

	var dataPtr atomic.Pointer[Data]
	dataPtr.Store(&Data{value: 0})

	var wg sync.WaitGroup
	numReaders := 5
	numWrites := 10

	// Start readers
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.ReadLock()
				p := dataPtr.Load()
				if p != nil {
					_ = p.value
				}
				r.ReadUnlock()
				time.Sleep(time.Microsecond * 100)
			}
		}()
	}

	// Writer
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < numWrites; i++ {
			// Copy-update
			old := dataPtr.Load()
			newData := &Data{value: old.value + 1}
			dataPtr.Store(newData)
			
			// Wait for grace period
			r.Synchronize()
			
			time.Sleep(time.Millisecond * 10)
		}
	}()

	wg.Wait()

	finalData := dataPtr.Load()
	if finalData.value != numWrites {
		t.Errorf("Expected final value %d, got %d", numWrites, finalData.value)
	}
}

func TestMultipleSynchronize(t *testing.T) {
	r := New(WithGracePeriod(time.Millisecond * 5))
	r.Start()
	defer r.Stop()

	// Multiple synchronizes should all complete
	for i := 0; i < 5; i++ {
		r.Synchronize()
	}

	stats := r.Stats()
	if stats.GracePeriods < 1 {
		t.Error("Expected at least one grace period")
	}
}

func TestDoHelper(t *testing.T) {
	r := New()
	r.Start()
	defer r.Stop()

	var executed bool
	r.Do(func() {
		executed = true
	})

	if !executed {
		t.Error("Do helper did not execute function")
	}

	stats := r.Stats()
	if stats.ReadLocks != 1 || stats.ReadUnlocks != 1 {
		t.Error("Do helper did not properly lock/unlock")
	}
}

func TestRCUPointer(t *testing.T) {
	type Data struct {
		value int
	}

	ptr := NewRCUPointer(&Data{value: 42})

	// Load
	val := ptr.Load()
	if val.value != 42 {
		t.Errorf("Expected 42, got %d", val.value)
	}

	// Store
	ptr.Store(&Data{value: 100})
	val = ptr.Load()
	if val.value != 100 {
		t.Errorf("Expected 100, got %d", val.value)
	}

	// CompareAndSwap
	old := ptr.Load()
	newData := &Data{value: 200}
	if !ptr.CompareAndSwap(old, newData) {
		t.Error("CAS should succeed")
	}
	if ptr.Load().value != 200 {
		t.Error("CAS did not update value")
	}
}

func TestRCUList(t *testing.T) {
	r := New()
	r.Start()
	defer r.Stop()

	list := NewRCUList[int](r)

	// Prepend elements
	list.Prepend(3)
	list.Prepend(2)
	list.Prepend(1)

	// Check length
	if list.Len() != 3 {
		t.Errorf("Expected length 3, got %d", list.Len())
	}

	// ForEach
	var elements []int
	list.ForEach(func(v int) bool {
		elements = append(elements, v)
		return true
	})

	if len(elements) != 3 {
		t.Errorf("Expected 3 elements, got %d", len(elements))
	}

	// Find
	val, found := list.Find(func(v int) bool { return v == 2 })
	if !found || *val != 2 {
		t.Error("Failed to find element 2")
	}

	// Remove
	removed := list.Remove(func(v int) bool { return v == 2 })
	if !removed {
		t.Error("Failed to remove element 2")
	}
	if list.Len() != 2 {
		t.Errorf("Expected length 2 after remove, got %d", list.Len())
	}
}

func TestCallbackList(t *testing.T) {
	cl := NewCallbackList()

	// Enqueue callbacks
	for i := 0; i < 10; i++ {
		cl.Enqueue(&RCUHead{})
	}

	if cl.Len() != 10 {
		t.Errorf("Expected 10 callbacks, got %d", cl.Len())
	}

	// Advance (simulates GP completion)
	cl.Advance(1)
	cl.Advance(2)
	cl.Advance(3)

	// Get ready callbacks
	ready := cl.GetReady()
	if len(ready) != 10 {
		t.Errorf("Expected 10 ready callbacks, got %d", len(ready))
	}
}

func TestGPSequence(t *testing.T) {
	seq := NewGPSequence()

	initial := seq.Get()
	if initial != 0 {
		t.Errorf("Initial sequence should be 0, got %d", initial)
	}

	// Advance
	seq.Advance()
	if seq.Counter() != 1 {
		t.Errorf("Counter should be 1, got %d", seq.Counter())
	}

	// Check started
	if !seq.Started(initial) {
		t.Error("GP should have started")
	}

	// Advance again for completion
	seq.Advance()
	if !seq.Completed(initial) {
		t.Error("GP should be completed")
	}
}

// Benchmark tests

func BenchmarkReadLockUnlock(b *testing.B) {
	r := New()
	r.Start()
	defer r.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.ReadLock()
		r.ReadUnlock()
	}
}

func BenchmarkParallelReadLockUnlock(b *testing.B) {
	r := New()
	r.Start()
	defer r.Stop()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			r.ReadLock()
			r.ReadUnlock()
		}
	})
}

func BenchmarkSynchronize(b *testing.B) {
	r := New(WithGracePeriod(time.Microsecond * 100))
	r.Start()
	defer r.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Synchronize()
	}
}

func BenchmarkCallRCU(b *testing.B) {
	r := New(WithGracePeriod(time.Microsecond * 100))
	r.Start()
	defer r.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		head := &RCUHead{}
		r.CallRCU(head, func(h *RCUHead) {})
	}

	// Wait for all callbacks to complete
	r.Synchronize()
}


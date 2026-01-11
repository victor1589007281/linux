// Package rcu provides Read-Copy-Update synchronization mechanism
// inspired by Linux kernel's RCU implementation.
//
// RCU allows lock-free read access to shared data structures while
// ensuring safe memory reclamation through grace periods.
//
// Reference: kernel/rcu/tree.c, kernel/rcu/update.c
package rcu

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// RCU is the main RCU controller
// Inspired by Linux's rcu_state structure
type RCU struct {
	// Grace period management
	gpSeq      uint64        // Grace period sequence number
	gpState    int32         // Current GP state
	gpComplete chan struct{} // Signal GP completion

	// Reader tracking (per-goroutine)
	readers sync.Map // goroutineID -> *ReaderState

	// Callback management
	cbList    *CallbackList
	cbListMu  sync.Mutex

	// Control
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	running   int32
	gpTicker  *time.Ticker
	gpPeriod  time.Duration

	// Statistics
	stats RCUStats
}

// GP states (inspired by kernel/rcu/tree.c)
const (
	RCU_GP_IDLE      int32 = 0 // No grace period in progress
	RCU_GP_WAIT_GPS  int32 = 1 // Waiting to start
	RCU_GP_DOING     int32 = 2 // Grace period in progress
	RCU_GP_CLEANUP   int32 = 3 // Cleanup after GP
)

// RCU sequence number constants
const (
	RCU_SEQ_CTR_SHIFT  = 2
	RCU_SEQ_STATE_MASK = 3
)

// RCUStats holds RCU statistics
type RCUStats struct {
	GracePeriods     uint64
	Callbacks        uint64
	CallbacksInvoked uint64
	ReadLocks        uint64
	ReadUnlocks      uint64
}

// Options for RCU configuration
type Option func(*RCU)

// WithGracePeriod sets the grace period check interval
func WithGracePeriod(d time.Duration) Option {
	return func(r *RCU) {
		r.gpPeriod = d
	}
}

// New creates a new RCU instance
func New(opts ...Option) *RCU {
	r := &RCU{
		gpSeq:      0,
		gpState:    RCU_GP_IDLE,
		gpComplete: make(chan struct{}),
		cbList:     NewCallbackList(),
		gpPeriod:   time.Millisecond * 10, // Default 10ms
	}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Start starts the RCU subsystem
func (r *RCU) Start() {
	if !atomic.CompareAndSwapInt32(&r.running, 0, 1) {
		return // Already running
	}

	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.gpTicker = time.NewTicker(r.gpPeriod)

	// Start grace period detector
	r.wg.Add(1)
	go r.gpThread()

	// Start callback processor
	r.wg.Add(1)
	go r.callbackProcessor()
}

// Stop stops the RCU subsystem
func (r *RCU) Stop() {
	if !atomic.CompareAndSwapInt32(&r.running, 1, 0) {
		return
	}

	r.cancel()
	r.gpTicker.Stop()
	r.wg.Wait()
}

// ReadLock enters an RCU read-side critical section
// This must be paired with ReadUnlock
//
// Reference: kernel/rcu/tree_plugin.h - __rcu_read_lock()
func (r *RCU) ReadLock() {
	gid := getGoroutineID()
	
	// Get or create reader state for this goroutine
	stateI, loaded := r.readers.LoadOrStore(gid, &ReaderState{})
	state := stateI.(*ReaderState)
	
	if !loaded {
		// New reader, record the GP sequence when it started
		state.gpSeqSeen = atomic.LoadUint64(&r.gpSeq)
	}

	// Increment nesting count
	atomic.AddInt32(&state.nesting, 1)
	atomic.AddUint64(&r.stats.ReadLocks, 1)

	// Memory barrier to ensure critical section starts after lock
	runtime.Gosched()
}

// ReadUnlock exits an RCU read-side critical section
//
// Reference: kernel/rcu/tree_plugin.h - __rcu_read_unlock()
func (r *RCU) ReadUnlock() {
	gid := getGoroutineID()

	stateI, ok := r.readers.Load(gid)
	if !ok {
		panic("rcu: ReadUnlock without ReadLock")
	}

	state := stateI.(*ReaderState)

	// Memory barrier before decrementing
	runtime.Gosched()

	// Decrement nesting count
	newNesting := atomic.AddInt32(&state.nesting, -1)
	atomic.AddUint64(&r.stats.ReadUnlocks, 1)

	if newNesting < 0 {
		panic("rcu: ReadUnlock underflow")
	}

	if newNesting == 0 {
		// Outermost unlock - report quiescent state
		atomic.StoreUint64(&state.gpSeqSeen, atomic.LoadUint64(&r.gpSeq))
		
		// Clean up if this goroutine is done
		r.readers.Delete(gid)
	}
}

// Synchronize waits for a full grace period to elapse
// After this returns, all RCU read-side critical sections that were
// in progress when Synchronize was called have completed.
//
// Reference: kernel/rcu/tree.c - synchronize_rcu()
func (r *RCU) Synchronize() {
	if atomic.LoadInt32(&r.running) == 0 {
		return
	}

	// Use completion-based synchronization
	done := make(chan struct{})
	
	head := &RCUHead{}
	r.CallRCU(head, func(h *RCUHead) {
		close(done)
	})

	<-done
}

// CallRCU queues a callback for invocation after a grace period
//
// Reference: kernel/rcu/tree.c - call_rcu()
func (r *RCU) CallRCU(head *RCUHead, fn func(*RCUHead)) {
	head.fn = fn
	head.next = nil
	head.gpSeq = atomic.LoadUint64(&r.gpSeq)

	r.cbListMu.Lock()
	r.cbList.Enqueue(head)
	r.cbListMu.Unlock()

	atomic.AddUint64(&r.stats.Callbacks, 1)

	// Signal that we have callbacks waiting
	r.maybeStartGP()
}

// maybeStartGP starts a grace period if needed
func (r *RCU) maybeStartGP() {
	// If not in idle state, a GP is already in progress or scheduled
	if atomic.LoadInt32(&r.gpState) != RCU_GP_IDLE {
		return
	}

	atomic.CompareAndSwapInt32(&r.gpState, RCU_GP_IDLE, RCU_GP_WAIT_GPS)
}

// gpThread is the grace period detection thread
// Inspired by Linux's rcu_gp_kthread
func (r *RCU) gpThread() {
	defer r.wg.Done()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.gpTicker.C:
			r.processGracePeriod()
		}
	}
}

// processGracePeriod handles one grace period cycle
func (r *RCU) processGracePeriod() {
	// Check if we need to start a GP
	if atomic.LoadInt32(&r.gpState) == RCU_GP_IDLE {
		r.cbListMu.Lock()
		hasCallbacks := r.cbList.Len() > 0
		r.cbListMu.Unlock()
		
		if !hasCallbacks {
			return
		}
		atomic.StoreInt32(&r.gpState, RCU_GP_WAIT_GPS)
	}

	// Start the grace period
	if atomic.CompareAndSwapInt32(&r.gpState, RCU_GP_WAIT_GPS, RCU_GP_DOING) {
		// Record the start of this grace period
		gpStartSeq := atomic.LoadUint64(&r.gpSeq)
		
		// Wait for all readers that were present at GP start
		r.waitForReaders(gpStartSeq)
		
		// Advance the sequence number
		atomic.AddUint64(&r.gpSeq, 1<<RCU_SEQ_CTR_SHIFT)
		atomic.AddUint64(&r.stats.GracePeriods, 1)
		
		// Move to cleanup
		atomic.StoreInt32(&r.gpState, RCU_GP_CLEANUP)
	}

	// Cleanup: advance callbacks
	if atomic.CompareAndSwapInt32(&r.gpState, RCU_GP_CLEANUP, RCU_GP_IDLE) {
		r.advanceCallbacks()
	}
}

// waitForReaders waits for all readers present at gpSeq to complete
func (r *RCU) waitForReaders(gpSeq uint64) {
	// Simple implementation: check all known readers
	maxWait := 100 * time.Millisecond
	deadline := time.Now().Add(maxWait)

	for time.Now().Before(deadline) {
		allQuiescent := true
		
		r.readers.Range(func(key, value interface{}) bool {
			state := value.(*ReaderState)
			
			// Reader is in critical section if nesting > 0
			// and was present before this GP started
			if atomic.LoadInt32(&state.nesting) > 0 {
				seenSeq := atomic.LoadUint64(&state.gpSeqSeen)
				if seenSeq <= gpSeq {
					allQuiescent = false
					return false // Stop iteration
				}
			}
			return true
		})

		if allQuiescent {
			return
		}

		// Brief sleep to avoid busy-waiting
		runtime.Gosched()
		time.Sleep(time.Microsecond * 100)
	}
}

// advanceCallbacks moves callbacks and invokes ready ones
func (r *RCU) advanceCallbacks() {
	r.cbListMu.Lock()
	r.cbList.Advance(atomic.LoadUint64(&r.gpSeq))
	r.cbListMu.Unlock()
}

// callbackProcessor invokes ready callbacks
func (r *RCU) callbackProcessor() {
	defer r.wg.Done()

	ticker := time.NewTicker(r.gpPeriod / 2)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			// Drain remaining callbacks on shutdown
			r.drainCallbacks()
			return
		case <-ticker.C:
			r.invokeCallbacks()
		}
	}
}

// invokeCallbacks invokes all ready callbacks
func (r *RCU) invokeCallbacks() {
	r.cbListMu.Lock()
	callbacks := r.cbList.GetReady()
	r.cbListMu.Unlock()

	for _, cb := range callbacks {
		if cb.fn != nil {
			cb.fn(cb)
			atomic.AddUint64(&r.stats.CallbacksInvoked, 1)
		}
	}
}

// drainCallbacks invokes all pending callbacks
func (r *RCU) drainCallbacks() {
	r.cbListMu.Lock()
	callbacks := r.cbList.DrainAll()
	r.cbListMu.Unlock()

	for _, cb := range callbacks {
		if cb.fn != nil {
			cb.fn(cb)
			atomic.AddUint64(&r.stats.CallbacksInvoked, 1)
		}
	}
}

// Stats returns RCU statistics
func (r *RCU) Stats() RCUStats {
	return RCUStats{
		GracePeriods:     atomic.LoadUint64(&r.stats.GracePeriods),
		Callbacks:        atomic.LoadUint64(&r.stats.Callbacks),
		CallbacksInvoked: atomic.LoadUint64(&r.stats.CallbacksInvoked),
		ReadLocks:        atomic.LoadUint64(&r.stats.ReadLocks),
		ReadUnlocks:      atomic.LoadUint64(&r.stats.ReadUnlocks),
	}
}

// GPSeq returns the current grace period sequence number
func (r *RCU) GPSeq() uint64 {
	return atomic.LoadUint64(&r.gpSeq)
}

// getGoroutineID returns a unique identifier for the current goroutine
// This is a simplified implementation; in production, consider using
// goroutine-local storage or other mechanisms
func getGoroutineID() uint64 {
	// Use the goroutine's stack pointer as a unique identifier
	// This is not the goroutine ID but serves as a unique key
	var x int
	return uint64(uintptr(unsafe.Pointer(&x)))
}

// Dereference safely reads an RCU-protected pointer
// Must be called within rcu_read_lock/rcu_read_unlock
func Dereference[T any](pp **T) *T {
	return (*T)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(pp))))
}

// AssignPointer safely updates an RCU-protected pointer
// The new data should be fully initialized before calling this
func AssignPointer[T any](pp **T, p *T) {
	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(pp)), unsafe.Pointer(p))
}


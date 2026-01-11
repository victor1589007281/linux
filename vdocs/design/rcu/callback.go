package rcu

import (
	"sync"
)

// RCUHead is the callback structure that must be embedded in structures
// that need RCU-based deferred freeing.
//
// Reference: include/linux/types.h - struct rcu_head
type RCUHead struct {
	next  *RCUHead
	fn    func(*RCUHead)
	gpSeq uint64 // Grace period sequence when callback was registered
}

// CallbackList manages RCU callbacks in a segmented list
// Inspired by Linux's rcu_segcblist
//
// Reference: kernel/rcu/rcu_segcblist.c
type CallbackList struct {
	mu sync.Mutex

	// Segmented callback lists
	// Each segment holds callbacks for different grace period states
	done      []*RCUHead // Callbacks ready to be invoked
	wait      []*RCUHead // Waiting for current GP to complete
	nextReady []*RCUHead // Will be ready after next GP
	next      []*RCUHead // Newly added callbacks

	// The GP sequence at which each segment becomes ready
	waitGP      uint64
	nextReadyGP uint64
}

// Callback segments (inspired by RCU_SEGCBLIST_*)
const (
	RCU_DONE_TAIL       = 0 // Callbacks ready to invoke
	RCU_WAIT_TAIL       = 1 // Waiting for current GP
	RCU_NEXT_READY_TAIL = 2 // Ready after next GP
	RCU_NEXT_TAIL       = 3 // Newly arrived
	RCU_CBLIST_NSEGS    = 4
)

// NewCallbackList creates a new callback list
func NewCallbackList() *CallbackList {
	return &CallbackList{
		done:      make([]*RCUHead, 0),
		wait:      make([]*RCUHead, 0),
		nextReady: make([]*RCUHead, 0),
		next:      make([]*RCUHead, 0),
	}
}

// Enqueue adds a callback to the list
// New callbacks go to the "next" segment
func (cl *CallbackList) Enqueue(head *RCUHead) {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	cl.next = append(cl.next, head)
}

// Advance moves callbacks to the next segment based on GP completion
// Called when a grace period completes
func (cl *CallbackList) Advance(currentGP uint64) {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	// Move wait -> done (these were waiting for a GP that's now complete)
	cl.done = append(cl.done, cl.wait...)
	cl.wait = cl.wait[:0]

	// Move nextReady -> wait
	cl.wait = append(cl.wait, cl.nextReady...)
	cl.waitGP = cl.nextReadyGP
	cl.nextReady = cl.nextReady[:0]

	// Move next -> nextReady
	cl.nextReady = append(cl.nextReady, cl.next...)
	cl.nextReadyGP = currentGP
	cl.next = cl.next[:0]
}

// GetReady returns all callbacks ready to be invoked
func (cl *CallbackList) GetReady() []*RCUHead {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	result := cl.done
	cl.done = make([]*RCUHead, 0)
	return result
}

// DrainAll returns all callbacks (for shutdown)
func (cl *CallbackList) DrainAll() []*RCUHead {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	result := make([]*RCUHead, 0, len(cl.done)+len(cl.wait)+len(cl.nextReady)+len(cl.next))
	result = append(result, cl.done...)
	result = append(result, cl.wait...)
	result = append(result, cl.nextReady...)
	result = append(result, cl.next...)

	cl.done = cl.done[:0]
	cl.wait = cl.wait[:0]
	cl.nextReady = cl.nextReady[:0]
	cl.next = cl.next[:0]

	return result
}

// Len returns the total number of callbacks
func (cl *CallbackList) Len() int {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return len(cl.done) + len(cl.wait) + len(cl.nextReady) + len(cl.next)
}

// LenDone returns the number of callbacks ready to invoke
func (cl *CallbackList) LenDone() int {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return len(cl.done)
}

// LenWait returns the number of callbacks waiting for GP
func (cl *CallbackList) LenWait() int {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return len(cl.wait)
}

// Stats returns callback list statistics
type CallbackListStats struct {
	Done      int
	Wait      int
	NextReady int
	Next      int
	Total     int
}

// Stats returns statistics about the callback list
func (cl *CallbackList) Stats() CallbackListStats {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	return CallbackListStats{
		Done:      len(cl.done),
		Wait:      len(cl.wait),
		NextReady: len(cl.nextReady),
		Next:      len(cl.next),
		Total:     len(cl.done) + len(cl.wait) + len(cl.nextReady) + len(cl.next),
	}
}

// RCUSynchronize is used for synchronize_rcu implementation
// Reference: kernel/rcu/update.c - struct rcu_synchronize
type RCUSynchronize struct {
	Head       RCUHead
	Completion chan struct{}
}

// NewRCUSynchronize creates a new synchronize structure
func NewRCUSynchronize() *RCUSynchronize {
	return &RCUSynchronize{
		Completion: make(chan struct{}),
	}
}

// WakemeAfterRCU is the callback function for synchronize_rcu
// Reference: kernel/rcu/update.c - wakeme_after_rcu()
func WakemeAfterRCU(head *RCUHead) {
	// Get the containing RCUSynchronize structure
	// In Go, we use the closure to access the completion channel
}

// CallRCUFunc is a function type for call_rcu callbacks
type CallRCUFunc func(*RCUHead)

// CallbackInvoker handles callback invocation
type CallbackInvoker struct {
	workers int
	queue   chan *RCUHead
	wg      sync.WaitGroup
	stopped int32
}

// NewCallbackInvoker creates a new callback invoker
func NewCallbackInvoker(workers int) *CallbackInvoker {
	return &CallbackInvoker{
		workers: workers,
		queue:   make(chan *RCUHead, 1000),
	}
}

// Start starts the callback invoker workers
func (ci *CallbackInvoker) Start() {
	for i := 0; i < ci.workers; i++ {
		ci.wg.Add(1)
		go ci.worker()
	}
}

// Stop stops the callback invoker
func (ci *CallbackInvoker) Stop() {
	close(ci.queue)
	ci.wg.Wait()
}

// Submit submits a callback for invocation
func (ci *CallbackInvoker) Submit(head *RCUHead) {
	ci.queue <- head
}

// worker is the callback worker goroutine
func (ci *CallbackInvoker) worker() {
	defer ci.wg.Done()

	for head := range ci.queue {
		if head.fn != nil {
			head.fn(head)
		}
	}
}


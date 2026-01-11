package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// AsyncScheduler implements io_uring style asynchronous scheduling
// This is inspired by Linux kernel's io_uring mechanism.
//
// Key concepts from Linux kernel (io_uring/):
// 1. Submission Queue (SQ) - ring buffer for submitting requests
// 2. Completion Queue (CQ) - ring buffer for completion events
// 3. Batch submission for efficiency
// 4. Support for SQPOLL mode (kernel thread polls SQ)
// 5. Zero-copy operations where possible
//
// Reference:
// - io_uring/io_uring.c: io_submit_sqe(), io_ring_ctx_alloc()
// - io_uring/sqpoll.c: io_sq_thread()
type AsyncScheduler struct {
	*BaseScheduler
	sq           *SubmissionQueue
	cq           *CompletionQueue
	mu           sync.RWMutex
	running      bool
	sqpollMode   bool // Whether to use SQPOLL mode
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	sqpollStop   chan struct{}
	completedCnt uint64
	submittedCnt uint64
}

// SubmissionQueueEntry represents an entry in the submission queue
// Inspired by Linux's io_uring_sqe structure
type SubmissionQueueEntry struct {
	Task     Task
	UserData uint64      // User-provided identifier
	Flags    uint32      // Submission flags
	Result   chan Result // Channel to receive result
}

// CompletionQueueEntry represents an entry in the completion queue
// Inspired by Linux's io_uring_cqe structure
type CompletionQueueEntry struct {
	UserData uint64    // Copied from SQE
	Result   int32     // Result code
	Flags    uint32    // Completion flags
	Error    error     // Error if any
	Task     Task      // Reference to the task
}

// Result holds the result of an async operation
type Result struct {
	Value int32
	Error error
}

// SubmissionQueue implements a ring buffer for submissions
// Inspired by io_uring's sq_ring structure
type SubmissionQueue struct {
	entries  []*SubmissionQueueEntry
	head     uint32
	tail     uint32
	mask     uint32
	size     uint32
	mu       sync.Mutex
	notEmpty chan struct{}
}

// CompletionQueue implements a ring buffer for completions
// Inspired by io_uring's cq_ring structure
type CompletionQueue struct {
	entries  []*CompletionQueueEntry
	head     uint32
	tail     uint32
	mask     uint32
	size     uint32
	mu       sync.Mutex
	notEmpty chan struct{}
}

// SQ/CQ flags (inspired by io_uring)
const (
	IOSQE_FIXED_FILE   = 1 << 0 // Use fixed file descriptor
	IOSQE_IO_DRAIN     = 1 << 1 // Wait for all previous requests
	IOSQE_IO_LINK      = 1 << 2 // Link with next request
	IOSQE_IO_HARDLINK  = 1 << 3 // Hard link with next request
	IOSQE_ASYNC        = 1 << 4 // Force async execution
	IOSQE_BUFFER_SELECT = 1 << 5 // Select buffer from pool
)

// Queue sizes (power of 2 for efficient modulo with bitwise AND)
const (
	DefaultSQSize = 4096
	DefaultCQSize = 8192 // CQ is typically 2x SQ size
)

// NewSubmissionQueue creates a new submission queue
func NewSubmissionQueue(size uint32) *SubmissionQueue {
	// Ensure size is power of 2
	size = nextPowerOf2(size)
	return &SubmissionQueue{
		entries:  make([]*SubmissionQueueEntry, size),
		head:     0,
		tail:     0,
		mask:     size - 1,
		size:     size,
		notEmpty: make(chan struct{}, 1),
	}
}

// NewCompletionQueue creates a new completion queue
func NewCompletionQueue(size uint32) *CompletionQueue {
	size = nextPowerOf2(size)
	return &CompletionQueue{
		entries:  make([]*CompletionQueueEntry, size),
		head:     0,
		tail:     0,
		mask:     size - 1,
		size:     size,
		notEmpty: make(chan struct{}, 1),
	}
}

// nextPowerOf2 returns the next power of 2 >= n
func nextPowerOf2(n uint32) uint32 {
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n++
	return n
}

// Push adds an entry to the submission queue
func (sq *SubmissionQueue) Push(entry *SubmissionQueueEntry) error {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	// Check if queue is full
	if sq.tail-sq.head >= sq.size {
		return ErrQueueFull
	}

	index := sq.tail & sq.mask
	sq.entries[index] = entry
	sq.tail++

	// Signal that queue is not empty
	select {
	case sq.notEmpty <- struct{}{}:
	default:
	}

	return nil
}

// Pop removes and returns an entry from the submission queue
func (sq *SubmissionQueue) Pop() (*SubmissionQueueEntry, error) {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	if sq.head == sq.tail {
		return nil, ErrQueueEmpty
	}

	index := sq.head & sq.mask
	entry := sq.entries[index]
	sq.entries[index] = nil
	sq.head++

	return entry, nil
}

// Len returns the number of entries in the queue
func (sq *SubmissionQueue) Len() int {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return int(sq.tail - sq.head)
}

// Push adds an entry to the completion queue
func (cq *CompletionQueue) Push(entry *CompletionQueueEntry) error {
	cq.mu.Lock()
	defer cq.mu.Unlock()

	if cq.tail-cq.head >= cq.size {
		return ErrQueueFull
	}

	index := cq.tail & cq.mask
	cq.entries[index] = entry
	cq.tail++

	select {
	case cq.notEmpty <- struct{}{}:
	default:
	}

	return nil
}

// Pop removes and returns an entry from the completion queue
func (cq *CompletionQueue) Pop() (*CompletionQueueEntry, error) {
	cq.mu.Lock()
	defer cq.mu.Unlock()

	if cq.head == cq.tail {
		return nil, ErrQueueEmpty
	}

	index := cq.head & cq.mask
	entry := cq.entries[index]
	cq.entries[index] = nil
	cq.head++

	return entry, nil
}

// Len returns the number of entries in the queue
func (cq *CompletionQueue) Len() int {
	cq.mu.Lock()
	defer cq.mu.Unlock()
	return int(cq.tail - cq.head)
}

// Wait blocks until at least one completion is available
func (cq *CompletionQueue) Wait(ctx context.Context) (*CompletionQueueEntry, error) {
	for {
		entry, err := cq.Pop()
		if err == nil {
			return entry, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-cq.notEmpty:
			// Try again
		}
	}
}

// AsyncSchedulerOption is a functional option for AsyncScheduler
type AsyncSchedulerOption func(*AsyncScheduler)

// WithSQPollMode enables SQPOLL mode
func WithSQPollMode(enabled bool) AsyncSchedulerOption {
	return func(s *AsyncScheduler) {
		s.sqpollMode = enabled
	}
}

// NewAsyncScheduler creates a new async scheduler
func NewAsyncScheduler(opts ...SchedulerOption) *AsyncScheduler {
	return &AsyncScheduler{
		BaseScheduler: NewBaseScheduler("async", opts...),
		sq:            NewSubmissionQueue(DefaultSQSize),
		cq:            NewCompletionQueue(DefaultCQSize),
		sqpollStop:    make(chan struct{}),
	}
}

// Name returns the scheduler name
func (s *AsyncScheduler) Name() string {
	return "async"
}

// Submit submits a task to the scheduler
// Returns a channel to receive the result
func (s *AsyncScheduler) Submit(task Task, userData uint64) (<-chan Result, error) {
	resultChan := make(chan Result, 1)

	entry := &SubmissionQueueEntry{
		Task:     task,
		UserData: userData,
		Result:   resultChan,
	}

	err := s.sq.Push(entry)
	if err != nil {
		close(resultChan)
		return nil, err
	}

	atomic.AddUint64(&s.submittedCnt, 1)
	return resultChan, nil
}

// SubmitBatch submits multiple tasks at once
// This is more efficient than submitting one by one
// Inspired by io_uring's batch submission
func (s *AsyncScheduler) SubmitBatch(tasks []Task) ([]<-chan Result, error) {
	results := make([]<-chan Result, len(tasks))

	for i, task := range tasks {
		resultChan, err := s.Submit(task, uint64(i))
		if err != nil {
			// Clean up already submitted
			return results[:i], err
		}
		results[i] = resultChan
	}

	return results, nil
}

// Enqueue implements the Scheduler interface
func (s *AsyncScheduler) Enqueue(task Task) error {
	_, err := s.Submit(task, 0)
	return err
}

// Dequeue implements the Scheduler interface
func (s *AsyncScheduler) Dequeue() (Task, error) {
	entry, err := s.sq.Pop()
	if err != nil {
		return nil, err
	}
	return entry.Task, nil
}

// Pick implements the Scheduler interface
func (s *AsyncScheduler) Pick() (Task, error) {
	s.sq.mu.Lock()
	defer s.sq.mu.Unlock()

	if s.sq.head == s.sq.tail {
		return nil, ErrQueueEmpty
	}

	index := s.sq.head & s.sq.mask
	return s.sq.entries[index].Task, nil
}

// Len returns the number of pending tasks
func (s *AsyncScheduler) Len() int {
	return s.sq.Len()
}

// Start starts the scheduler
func (s *AsyncScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.sqpollStop = make(chan struct{})
	s.mu.Unlock()

	// Start worker goroutines
	for i := 0; i < s.config.WorkerCount; i++ {
		s.wg.Add(1)
		go s.worker(i)
	}

	// Start SQPOLL thread if enabled
	if s.sqpollMode {
		s.wg.Add(1)
		go s.sqpollThread()
	}

	return nil
}

// Stop stops the scheduler
func (s *AsyncScheduler) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.cancel()
	close(s.sqpollStop)
	s.mu.Unlock()

	s.wg.Wait()
	return nil
}

// worker processes tasks from the submission queue
func (s *AsyncScheduler) worker(id int) {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			s.processNextSubmission()
		}
	}
}

// processNextSubmission processes one submission
func (s *AsyncScheduler) processNextSubmission() {
	entry, err := s.sq.Pop()
	if err == ErrQueueEmpty {
		// Wait for new submissions
		select {
		case <-s.sq.notEmpty:
		case <-s.ctx.Done():
			return
		case <-time.After(s.config.TickInterval):
		}
		return
	}
	if err != nil {
		return
	}

	// Execute task
	s.notifyTaskStart(entry.Task)
	execErr := entry.Task.Execute(s.ctx)
	s.notifyTaskComplete(entry.Task, execErr)

	// Create completion entry
	cqe := &CompletionQueueEntry{
		UserData: entry.UserData,
		Error:    execErr,
		Task:     entry.Task,
	}

	if execErr == nil {
		cqe.Result = 0
	} else {
		cqe.Result = -1
	}

	// Push to completion queue
	s.cq.Push(cqe)

	// Send result to the channel
	if entry.Result != nil {
		result := Result{Error: execErr}
		if execErr == nil {
			result.Value = 0
		} else {
			result.Value = -1
		}
		select {
		case entry.Result <- result:
		default:
		}
		close(entry.Result)
	}

	atomic.AddUint64(&s.completedCnt, 1)
}

// sqpollThread implements the SQPOLL kernel thread behavior
// This thread continuously polls the submission queue
//
// Reference: io_uring/sqpoll.c - io_sq_thread()
func (s *AsyncScheduler) sqpollThread() {
	defer s.wg.Done()

	for {
		select {
		case <-s.sqpollStop:
			return
		case <-s.ctx.Done():
			return
		default:
			// Continuously poll for new submissions
			if s.sq.Len() > 0 {
				s.processNextSubmission()
			} else {
				// Brief sleep to avoid busy-waiting
				time.Sleep(time.Microsecond * 100)
			}
		}
	}
}

// WaitCompletion waits for a completion event
func (s *AsyncScheduler) WaitCompletion(ctx context.Context) (*CompletionQueueEntry, error) {
	return s.cq.Wait(ctx)
}

// PeekCompletions returns up to n completion events without blocking
func (s *AsyncScheduler) PeekCompletions(n int) []*CompletionQueueEntry {
	var results []*CompletionQueueEntry

	for i := 0; i < n; i++ {
		entry, err := s.cq.Pop()
		if err != nil {
			break
		}
		results = append(results, entry)
	}

	return results
}

// GetSubmissionQueue returns the submission queue
func (s *AsyncScheduler) GetSubmissionQueue() *SubmissionQueue {
	return s.sq
}

// GetCompletionQueue returns the completion queue
func (s *AsyncScheduler) GetCompletionQueue() *CompletionQueue {
	return s.cq
}

// Stats returns scheduler statistics
func (s *AsyncScheduler) Stats() (submitted, completed uint64) {
	return atomic.LoadUint64(&s.submittedCnt), atomic.LoadUint64(&s.completedCnt)
}

// Linked submission support (inspired by IOSQE_IO_LINK)

// SubmitLinked submits a chain of linked tasks
// If any task fails, subsequent tasks are cancelled
func (s *AsyncScheduler) SubmitLinked(tasks []Task) (<-chan Result, error) {
	if len(tasks) == 0 {
		return nil, errors.New("empty task list")
	}

	resultChan := make(chan Result, 1)

	go func() {
		defer close(resultChan)

		for _, task := range tasks {
			err := task.Execute(s.ctx)
			if err != nil {
				resultChan <- Result{Value: -1, Error: err}
				return
			}
		}

		resultChan <- Result{Value: 0, Error: nil}
	}()

	return resultChan, nil
}


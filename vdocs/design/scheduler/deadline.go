package scheduler

import (
	"container/heap"
	"container/list"
	"context"
	"sync"
	"time"
)

// DeadlineScheduler implements deadline-based scheduling
// This is inspired by Linux kernel's mq-deadline IO scheduler.
//
// Key concepts from Linux kernel (block/mq-deadline.c):
// 1. Maintains both FIFO list (by arrival time) and sorted list (by deadline)
// 2. Expired requests (past deadline) are prioritized
// 3. Separate queues for different priority classes (RT, BE, IDLE)
// 4. Batching to improve throughput
//
// Reference: struct deadline_data in block/mq-deadline.c
type DeadlineScheduler struct {
	*BaseScheduler
	// FIFO list ordered by arrival time
	fifoQueue *list.List
	// Deadline-sorted heap
	deadlineQueue *deadlineHeap
	// Track expired tasks
	expiredCount int
	// Configuration
	fifoExpire   time.Duration // Max time before a task is considered expired
	batchSize    int           // Number of sequential tasks to batch
	starvedLimit int           // Max times high-priority can starve low-priority
	starvedCount int
	mu           sync.RWMutex
	running      bool
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
}

// deadlineEntity represents a task with deadline metadata
type deadlineEntity struct {
	task       Task
	deadline   time.Time
	arrivedAt  time.Time
	fifoElem   *list.Element
	heapIndex  int
}

// deadlineHeap implements a min-heap sorted by deadline
type deadlineHeap struct {
	entities []*deadlineEntity
}

func (h *deadlineHeap) Len() int { return len(h.entities) }

func (h *deadlineHeap) Less(i, j int) bool {
	return h.entities[i].deadline.Before(h.entities[j].deadline)
}

func (h *deadlineHeap) Swap(i, j int) {
	h.entities[i], h.entities[j] = h.entities[j], h.entities[i]
	h.entities[i].heapIndex = i
	h.entities[j].heapIndex = j
}

func (h *deadlineHeap) Push(x interface{}) {
	n := len(h.entities)
	entity := x.(*deadlineEntity)
	entity.heapIndex = n
	h.entities = append(h.entities, entity)
}

func (h *deadlineHeap) Pop() interface{} {
	old := h.entities
	n := len(old)
	entity := old[n-1]
	old[n-1] = nil
	entity.heapIndex = -1
	h.entities = old[0 : n-1]
	return entity
}

// Default deadline scheduler configuration
const (
	DefaultFIFOExpire   = 500 * time.Millisecond // Max time before task is expired
	DefaultBatchSize    = 16                     // Number of tasks to batch
	DefaultStarvedLimit = 2                      // Max starvation count
)

// NewDeadlineScheduler creates a new deadline scheduler
func NewDeadlineScheduler(opts ...SchedulerOption) *DeadlineScheduler {
	return &DeadlineScheduler{
		BaseScheduler: NewBaseScheduler("deadline", opts...),
		fifoQueue:     list.New(),
		deadlineQueue: &deadlineHeap{entities: make([]*deadlineEntity, 0)},
		fifoExpire:    DefaultFIFOExpire,
		batchSize:     DefaultBatchSize,
		starvedLimit:  DefaultStarvedLimit,
	}
}

// Name returns the scheduler name
func (s *DeadlineScheduler) Name() string {
	return "deadline"
}

// Enqueue adds a task to both FIFO and deadline queues
// Reference: dd_insert_request() in block/mq-deadline.c
func (s *DeadlineScheduler) Enqueue(task Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running && s.ctx != nil {
		return ErrSchedulerStopped
	}

	now := time.Now()
	deadline := task.Deadline()
	if deadline.IsZero() {
		// If no deadline specified, use FIFO expire as default
		deadline = now.Add(s.fifoExpire)
	}

	entity := &deadlineEntity{
		task:      task,
		deadline:  deadline,
		arrivedAt: now,
	}

	// Add to FIFO queue
	entity.fifoElem = s.fifoQueue.PushBack(entity)

	// Add to deadline heap
	heap.Push(s.deadlineQueue, entity)

	return nil
}

// Dequeue removes and returns the next task to run
// This implements the core deadline scheduling logic
//
// Reference: dd_dispatch_request() in block/mq-deadline.c
func (s *DeadlineScheduler) Dequeue() (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fifoQueue.Len() == 0 {
		return nil, ErrQueueEmpty
	}

	var entity *deadlineEntity

	// Check for expired tasks first (past FIFO expire time)
	if s.checkFIFOExpired() {
		entity = s.dispatchFIFO()
	} else {
		// Otherwise, dispatch by deadline
		entity = s.dispatchDeadline()
	}

	if entity == nil {
		return nil, ErrQueueEmpty
	}

	// Remove from both queues
	s.removeEntity(entity)

	return entity.task, nil
}

// checkFIFOExpired checks if the oldest task has expired
// Reference: deadline_check_fifo() in block/mq-deadline.c
func (s *DeadlineScheduler) checkFIFOExpired() bool {
	if s.fifoQueue.Len() == 0 {
		return false
	}

	front := s.fifoQueue.Front()
	entity := front.Value.(*deadlineEntity)

	return time.Since(entity.arrivedAt) > s.fifoExpire
}

// dispatchFIFO returns the oldest task (FIFO order)
func (s *DeadlineScheduler) dispatchFIFO() *deadlineEntity {
	if s.fifoQueue.Len() == 0 {
		return nil
	}

	front := s.fifoQueue.Front()
	return front.Value.(*deadlineEntity)
}

// dispatchDeadline returns the task with earliest deadline
func (s *DeadlineScheduler) dispatchDeadline() *deadlineEntity {
	if s.deadlineQueue.Len() == 0 {
		return nil
	}

	return s.deadlineQueue.entities[0]
}

// removeEntity removes an entity from both queues
func (s *DeadlineScheduler) removeEntity(entity *deadlineEntity) {
	// Remove from FIFO queue
	if entity.fifoElem != nil {
		s.fifoQueue.Remove(entity.fifoElem)
		entity.fifoElem = nil
	}

	// Remove from deadline heap
	if entity.heapIndex >= 0 && entity.heapIndex < s.deadlineQueue.Len() {
		heap.Remove(s.deadlineQueue, entity.heapIndex)
	}
}

// Pick returns the next task without removing it
func (s *DeadlineScheduler) Pick() (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.fifoQueue.Len() == 0 {
		return nil, ErrQueueEmpty
	}

	var entity *deadlineEntity

	if s.checkFIFOExpired() {
		entity = s.dispatchFIFO()
	} else {
		entity = s.dispatchDeadline()
	}

	if entity == nil {
		return nil, ErrQueueEmpty
	}

	return entity.task, nil
}

// Len returns the number of tasks in the queue
func (s *DeadlineScheduler) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.fifoQueue.Len()
}

// Start starts the scheduler
func (s *DeadlineScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.mu.Unlock()

	// Start worker goroutines
	for i := 0; i < s.config.WorkerCount; i++ {
		s.wg.Add(1)
		go s.worker(i)
	}

	return nil
}

// Stop stops the scheduler
func (s *DeadlineScheduler) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.cancel()
	s.mu.Unlock()

	s.wg.Wait()
	return nil
}

// worker processes tasks from the queue
func (s *DeadlineScheduler) worker(id int) {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			s.processNextTask()
		}
	}
}

// processNextTask picks and executes the next task
func (s *DeadlineScheduler) processNextTask() {
	task, err := s.Dequeue()
	if err == ErrQueueEmpty {
		time.Sleep(s.config.TickInterval)
		return
	}
	if err != nil {
		return
	}

	s.notifyTaskStart(task)
	execErr := task.Execute(s.ctx)
	s.notifyTaskComplete(task, execErr)
}

// SetFIFOExpire sets the FIFO expire time
func (s *DeadlineScheduler) SetFIFOExpire(expire time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fifoExpire = expire
}

// SetBatchSize sets the batch size
func (s *DeadlineScheduler) SetBatchSize(size int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batchSize = size
}

// GetExpiredCount returns the number of expired tasks
func (s *DeadlineScheduler) GetExpiredCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for elem := s.fifoQueue.Front(); elem != nil; elem = elem.Next() {
		entity := elem.Value.(*deadlineEntity)
		if time.Since(entity.arrivedAt) > s.fifoExpire {
			count++
		}
	}
	return count
}

// GetNearestDeadline returns the nearest deadline in the queue
func (s *DeadlineScheduler) GetNearestDeadline() (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.deadlineQueue.Len() == 0 {
		return time.Time{}, false
	}

	return s.deadlineQueue.entities[0].deadline, true
}


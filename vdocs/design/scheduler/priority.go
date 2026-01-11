package scheduler

import (
	"container/list"
	"context"
	"sync"
	"time"
)

// PriorityScheduler implements a multi-level priority queue scheduler
// This is inspired by Linux kernel's real-time scheduler with priority arrays.
//
// Key concepts from Linux kernel (kernel/sched/rt.c):
// 1. Multiple priority levels (MAX_RT_PRIO = 100 in Linux)
// 2. Higher priority tasks always run before lower priority tasks
// 3. Within same priority, tasks are scheduled in FIFO order
// 4. Bitmap tracks which priority levels have runnable tasks
//
// Reference: struct rt_prio_array in kernel/sched/sched.h
type PriorityScheduler struct {
	*BaseScheduler
	queues    []*list.List // One queue per priority level
	bitmap    []bool       // Tracks which queues have tasks
	numLevels int          // Number of priority levels
	mu        sync.RWMutex
	running   bool
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// Default number of priority levels
const DefaultPriorityLevels = 100

// PrioritySchedulerOption is a functional option for PriorityScheduler
type PrioritySchedulerOption func(*PriorityScheduler)

// WithPriorityLevels sets the number of priority levels
func WithPriorityLevels(levels int) PrioritySchedulerOption {
	return func(s *PriorityScheduler) {
		s.numLevels = levels
	}
}

// NewPriorityScheduler creates a new priority scheduler
func NewPriorityScheduler(opts ...SchedulerOption) *PriorityScheduler {
	config := DefaultSchedulerConfig()
	for _, opt := range opts {
		opt(config)
	}

	numLevels := DefaultPriorityLevels
	queues := make([]*list.List, numLevels)
	bitmap := make([]bool, numLevels)

	for i := 0; i < numLevels; i++ {
		queues[i] = list.New()
	}

	return &PriorityScheduler{
		BaseScheduler: NewBaseScheduler("priority", opts...),
		queues:        queues,
		bitmap:        bitmap,
		numLevels:     numLevels,
	}
}

// Name returns the scheduler name
func (s *PriorityScheduler) Name() string {
	return "priority"
}

// Enqueue adds a task to the appropriate priority queue
// Tasks are added to the end of their priority level queue (FIFO within priority)
func (s *PriorityScheduler) Enqueue(task Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running && s.ctx != nil {
		return ErrSchedulerStopped
	}

	priority := task.Priority()
	if priority < 0 {
		priority = 0
	}
	if priority >= s.numLevels {
		priority = s.numLevels - 1
	}

	wrapper := NewTaskWrapper(task)
	s.queues[priority].PushBack(wrapper)
	s.bitmap[priority] = true

	return nil
}

// Dequeue removes and returns the highest priority task
// Scans bitmap to find first non-empty queue (O(1) with bitmap optimization)
func (s *PriorityScheduler) Dequeue() (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find first non-empty queue (highest priority = lowest index)
	for i := 0; i < s.numLevels; i++ {
		if s.bitmap[i] && s.queues[i].Len() > 0 {
			elem := s.queues[i].Front()
			wrapper := s.queues[i].Remove(elem).(*TaskWrapper)

			// Update bitmap if queue becomes empty
			if s.queues[i].Len() == 0 {
				s.bitmap[i] = false
			}

			return wrapper.Task, nil
		}
	}

	return nil, ErrQueueEmpty
}

// Pick returns the highest priority task without removing it
func (s *PriorityScheduler) Pick() (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := 0; i < s.numLevels; i++ {
		if s.bitmap[i] && s.queues[i].Len() > 0 {
			wrapper := s.queues[i].Front().Value.(*TaskWrapper)
			return wrapper.Task, nil
		}
	}

	return nil, ErrQueueEmpty
}

// Len returns the total number of tasks across all queues
func (s *PriorityScheduler) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := 0
	for i := 0; i < s.numLevels; i++ {
		total += s.queues[i].Len()
	}
	return total
}

// LenAtPriority returns the number of tasks at a specific priority level
func (s *PriorityScheduler) LenAtPriority(priority int) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if priority < 0 || priority >= s.numLevels {
		return 0
	}
	return s.queues[priority].Len()
}

// Start starts the scheduler
func (s *PriorityScheduler) Start(ctx context.Context) error {
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
func (s *PriorityScheduler) Stop() error {
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
func (s *PriorityScheduler) worker(id int) {
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

// processNextTask picks and executes the next highest priority task
func (s *PriorityScheduler) processNextTask() {
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

// BoostPriority temporarily boosts a task's priority
// This can be used to prevent priority inversion
func (s *PriorityScheduler) BoostPriority(taskID string, newPriority int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find and remove task from current queue
	var foundWrapper *TaskWrapper
	var foundPriority int = -1

	for i := 0; i < s.numLevels; i++ {
		for elem := s.queues[i].Front(); elem != nil; elem = elem.Next() {
			wrapper := elem.Value.(*TaskWrapper)
			if wrapper.Task.ID() == taskID {
				foundWrapper = wrapper
				foundPriority = i
				s.queues[i].Remove(elem)
				if s.queues[i].Len() == 0 {
					s.bitmap[i] = false
				}
				break
			}
		}
		if foundWrapper != nil {
			break
		}
	}

	if foundWrapper == nil {
		return ErrTaskNotFound
	}

	// Add to new priority queue
	if newPriority < 0 {
		newPriority = 0
	}
	if newPriority >= s.numLevels {
		newPriority = s.numLevels - 1
	}

	// Only boost (move to higher priority = lower index)
	if newPriority > foundPriority {
		newPriority = foundPriority
	}

	s.queues[newPriority].PushFront(foundWrapper)
	s.bitmap[newPriority] = true

	return nil
}

// GetHighestPriority returns the priority level of the highest priority task
func (s *PriorityScheduler) GetHighestPriority() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := 0; i < s.numLevels; i++ {
		if s.bitmap[i] && s.queues[i].Len() > 0 {
			return i
		}
	}

	return -1 // No tasks
}


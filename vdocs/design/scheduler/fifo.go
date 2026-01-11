package scheduler

import (
	"context"
	"sync"
)

// FIFOScheduler implements a First-In-First-Out scheduler
// This is the simplest scheduling algorithm where tasks are processed
// in the order they arrive, similar to a basic queue.
//
// Reference: Linux kernel's simple FIFO implementation for real-time tasks
// See: kernel/sched/rt.c - SCHED_FIFO policy
type FIFOScheduler struct {
	*BaseScheduler
	queue   *RunQueue
	mu      sync.RWMutex
	running bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewFIFOScheduler creates a new FIFO scheduler
func NewFIFOScheduler(opts ...SchedulerOption) *FIFOScheduler {
	return &FIFOScheduler{
		BaseScheduler: NewBaseScheduler("fifo", opts...),
		queue:         NewRunQueue(DefaultSchedulerConfig().MaxQueueSize),
	}
}

// Name returns the scheduler name
func (s *FIFOScheduler) Name() string {
	return "fifo"
}

// Enqueue adds a task to the end of the queue
// Time complexity: O(1)
func (s *FIFOScheduler) Enqueue(task Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running && s.ctx != nil {
		return ErrSchedulerStopped
	}

	return s.queue.Push(task)
}

// Dequeue removes and returns the first task from the queue
// Time complexity: O(1)
func (s *FIFOScheduler) Dequeue() (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.queue.Pop()
}

// Pick returns the first task without removing it
func (s *FIFOScheduler) Pick() (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.queue.Peek()
}

// Len returns the number of tasks in the queue
func (s *FIFOScheduler) Len() int {
	return s.queue.Len()
}

// Start starts the scheduler with worker goroutines
func (s *FIFOScheduler) Start(ctx context.Context) error {
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
func (s *FIFOScheduler) Stop() error {
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

// worker is a goroutine that processes tasks
func (s *FIFOScheduler) worker(id int) {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			task, err := s.Dequeue()
			if err == ErrQueueEmpty {
				continue
			}
			if err != nil {
				continue
			}

			s.notifyTaskStart(task)
			execErr := task.Execute(s.ctx)
			s.notifyTaskComplete(task, execErr)
		}
	}
}


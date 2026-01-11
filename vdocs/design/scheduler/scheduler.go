package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Common errors
var (
	ErrSchedulerStopped = errors.New("scheduler is stopped")
	ErrQueueEmpty       = errors.New("queue is empty")
	ErrQueueFull        = errors.New("queue is full")
	ErrTaskNotFound     = errors.New("task not found")
)

// Scheduler defines the interface for all schedulers
// This interface is inspired by Linux kernel's sched_class structure
type Scheduler interface {
	// Name returns the name of the scheduler
	Name() string

	// Enqueue adds a task to the scheduler's run queue
	// Inspired by sched_class.enqueue_task
	Enqueue(task Task) error

	// Dequeue removes and returns the next task to run
	// Inspired by sched_class.pick_next_task
	Dequeue() (Task, error)

	// Pick returns the next task without removing it
	// Inspired by sched_class.pick_task
	Pick() (Task, error)

	// Len returns the number of tasks in the queue
	Len() int

	// Start starts the scheduler
	Start(ctx context.Context) error

	// Stop stops the scheduler
	Stop() error
}

// SchedulerConfig holds common configuration for schedulers
type SchedulerConfig struct {
	// MaxQueueSize is the maximum number of tasks in the queue
	MaxQueueSize int

	// WorkerCount is the number of worker goroutines
	WorkerCount int

	// TimeSlice is the default time slice for round-robin scheduling
	TimeSlice time.Duration

	// TickInterval is the interval for scheduler tick
	TickInterval time.Duration
}

// DefaultSchedulerConfig returns the default scheduler configuration
func DefaultSchedulerConfig() *SchedulerConfig {
	return &SchedulerConfig{
		MaxQueueSize: 10000,
		WorkerCount:  4,
		TimeSlice:    100 * time.Millisecond,
		TickInterval: 10 * time.Millisecond,
	}
}

// SchedulerOption is a functional option for configuring schedulers
type SchedulerOption func(*SchedulerConfig)

// WithMaxQueueSize sets the maximum queue size
func WithMaxQueueSize(size int) SchedulerOption {
	return func(c *SchedulerConfig) {
		c.MaxQueueSize = size
	}
}

// WithWorkerCount sets the number of workers
func WithWorkerCount(count int) SchedulerOption {
	return func(c *SchedulerConfig) {
		c.WorkerCount = count
	}
}

// WithSchedulerTimeSlice sets the default time slice
func WithSchedulerTimeSlice(slice time.Duration) SchedulerOption {
	return func(c *SchedulerConfig) {
		c.TimeSlice = slice
	}
}

// WithTickInterval sets the tick interval
func WithTickInterval(interval time.Duration) SchedulerOption {
	return func(c *SchedulerConfig) {
		c.TickInterval = interval
	}
}

// RunQueue represents a run queue holding tasks
// Inspired by Linux kernel's rq (run queue) structure
type RunQueue struct {
	mu        sync.RWMutex
	tasks     []Task
	maxSize   int
	nrRunning int
}

// NewRunQueue creates a new run queue
func NewRunQueue(maxSize int) *RunQueue {
	return &RunQueue{
		tasks:   make([]Task, 0, maxSize),
		maxSize: maxSize,
	}
}

// Push adds a task to the run queue
func (rq *RunQueue) Push(task Task) error {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	if len(rq.tasks) >= rq.maxSize {
		return ErrQueueFull
	}

	rq.tasks = append(rq.tasks, task)
	rq.nrRunning++
	return nil
}

// Pop removes and returns the first task
func (rq *RunQueue) Pop() (Task, error) {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	if len(rq.tasks) == 0 {
		return nil, ErrQueueEmpty
	}

	task := rq.tasks[0]
	rq.tasks = rq.tasks[1:]
	rq.nrRunning--
	return task, nil
}

// Peek returns the first task without removing it
func (rq *RunQueue) Peek() (Task, error) {
	rq.mu.RLock()
	defer rq.mu.RUnlock()

	if len(rq.tasks) == 0 {
		return nil, ErrQueueEmpty
	}

	return rq.tasks[0], nil
}

// Len returns the number of tasks
func (rq *RunQueue) Len() int {
	rq.mu.RLock()
	defer rq.mu.RUnlock()
	return len(rq.tasks)
}

// Remove removes a specific task from the queue
func (rq *RunQueue) Remove(taskID string) error {
	rq.mu.Lock()
	defer rq.mu.Unlock()

	for i, t := range rq.tasks {
		if t.ID() == taskID {
			rq.tasks = append(rq.tasks[:i], rq.tasks[i+1:]...)
			rq.nrRunning--
			return nil
		}
	}
	return ErrTaskNotFound
}

// BaseScheduler provides common functionality for all schedulers
type BaseScheduler struct {
	name    string
	config  *SchedulerConfig
	running bool
	mu      sync.RWMutex
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// Callbacks
	onTaskComplete func(task Task, err error)
	onTaskStart    func(task Task)
}

// NewBaseScheduler creates a new BaseScheduler
func NewBaseScheduler(name string, opts ...SchedulerOption) *BaseScheduler {
	config := DefaultSchedulerConfig()
	for _, opt := range opts {
		opt(config)
	}

	return &BaseScheduler{
		name:   name,
		config: config,
	}
}

// Name returns the scheduler name
func (s *BaseScheduler) Name() string {
	return s.name
}

// IsRunning returns whether the scheduler is running
func (s *BaseScheduler) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// SetOnTaskComplete sets the callback for task completion
func (s *BaseScheduler) SetOnTaskComplete(fn func(task Task, err error)) {
	s.onTaskComplete = fn
}

// SetOnTaskStart sets the callback for task start
func (s *BaseScheduler) SetOnTaskStart(fn func(task Task)) {
	s.onTaskStart = fn
}

// notifyTaskStart notifies task start
func (s *BaseScheduler) notifyTaskStart(task Task) {
	if s.onTaskStart != nil {
		s.onTaskStart(task)
	}
}

// notifyTaskComplete notifies task completion
func (s *BaseScheduler) notifyTaskComplete(task Task, err error) {
	if s.onTaskComplete != nil {
		s.onTaskComplete(task, err)
	}
}

// SchedulerStats holds scheduler statistics
// Inspired by Linux's scheduler statistics
type SchedulerStats struct {
	TotalTasks      uint64
	CompletedTasks  uint64
	FailedTasks     uint64
	TotalRuntime    time.Duration
	AvgWaitTime     time.Duration
	AvgExecuteTime  time.Duration
	ContextSwitches uint64
}


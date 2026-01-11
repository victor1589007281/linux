// Package scheduler provides abstract resource scheduling implementations
// inspired by Linux kernel CPU/IO schedulers and io_uring mechanism.
package scheduler

import (
	"context"
	"sync/atomic"
	"time"
)

// Task represents an abstract schedulable unit.
// This interface is inspired by Linux kernel's task_struct and sched_entity.
type Task interface {
	// ID returns the unique identifier of the task
	ID() string

	// Priority returns the priority of the task (lower value = higher priority)
	// Inspired by Linux's task priority system (nice values)
	Priority() int

	// Deadline returns the deadline by which the task should complete
	// Inspired by SCHED_DEADLINE and mq-deadline IO scheduler
	Deadline() time.Time

	// Weight returns the weight for fair scheduling (higher = more CPU time)
	// Inspired by CFS scheduler's load weight
	Weight() uint64

	// Execute runs the task with the given context
	Execute(ctx context.Context) error

	// GetVRuntime returns the virtual runtime (for CFS-like scheduling)
	GetVRuntime() uint64

	// SetVRuntime sets the virtual runtime
	SetVRuntime(vruntime uint64)

	// GetTimeSlice returns remaining time slice (for RR scheduling)
	GetTimeSlice() time.Duration

	// SetTimeSlice sets the time slice
	SetTimeSlice(slice time.Duration)
}

// TaskState represents the state of a task
// Inspired by Linux's task states (TASK_RUNNING, TASK_INTERRUPTIBLE, etc.)
type TaskState int

const (
	TaskStateReady TaskState = iota
	TaskStateRunning
	TaskStateBlocked
	TaskStateCompleted
)

// SimpleTask is a basic implementation of Task interface
type SimpleTask struct {
	id        string
	priority  int
	deadline  time.Time
	weight    uint64
	vruntime  uint64
	timeSlice time.Duration
	state     TaskState
	executor  func(ctx context.Context) error
}

// TaskOption is a functional option for configuring SimpleTask
type TaskOption func(*SimpleTask)

// WithPriority sets the task priority
func WithPriority(priority int) TaskOption {
	return func(t *SimpleTask) {
		t.priority = priority
	}
}

// WithDeadline sets the task deadline
func WithDeadline(deadline time.Time) TaskOption {
	return func(t *SimpleTask) {
		t.deadline = deadline
	}
}

// WithWeight sets the task weight for fair scheduling
func WithWeight(weight uint64) TaskOption {
	return func(t *SimpleTask) {
		t.weight = weight
	}
}

// WithTimeSlice sets the initial time slice
func WithTimeSlice(slice time.Duration) TaskOption {
	return func(t *SimpleTask) {
		t.timeSlice = slice
	}
}

// NewSimpleTask creates a new SimpleTask with the given options
func NewSimpleTask(id string, executor func(ctx context.Context) error, opts ...TaskOption) *SimpleTask {
	t := &SimpleTask{
		id:        id,
		priority:  0,               // Default priority (normal)
		deadline:  time.Time{},     // No deadline by default
		weight:    1024,            // Default weight (NICE_0_LOAD in Linux)
		vruntime:  0,
		timeSlice: 100 * time.Millisecond, // Default time slice
		state:     TaskStateReady,
		executor:  executor,
	}

	for _, opt := range opts {
		opt(t)
	}

	return t
}

func (t *SimpleTask) ID() string {
	return t.id
}

func (t *SimpleTask) Priority() int {
	return t.priority
}

func (t *SimpleTask) Deadline() time.Time {
	return t.deadline
}

func (t *SimpleTask) Weight() uint64 {
	return t.weight
}

func (t *SimpleTask) Execute(ctx context.Context) error {
	if t.executor == nil {
		return nil
	}
	return t.executor(ctx)
}

func (t *SimpleTask) GetVRuntime() uint64 {
	return atomic.LoadUint64(&t.vruntime)
}

func (t *SimpleTask) SetVRuntime(vruntime uint64) {
	atomic.StoreUint64(&t.vruntime, vruntime)
}

func (t *SimpleTask) GetTimeSlice() time.Duration {
	return t.timeSlice
}

func (t *SimpleTask) SetTimeSlice(slice time.Duration) {
	t.timeSlice = slice
}

func (t *SimpleTask) GetState() TaskState {
	return t.state
}

func (t *SimpleTask) SetState(state TaskState) {
	t.state = state
}

// TaskWrapper wraps a task with additional scheduling metadata
// Inspired by Linux's sched_entity structure
type TaskWrapper struct {
	Task       Task
	EnqueuedAt time.Time // When the task was enqueued
	StartedAt  time.Time // When the task started executing
	VRuntime   uint64    // Virtual runtime for CFS
	TimeSlice  time.Duration // Remaining time slice for RR
}

// NewTaskWrapper creates a new TaskWrapper
func NewTaskWrapper(task Task) *TaskWrapper {
	return &TaskWrapper{
		Task:       task,
		EnqueuedAt: time.Now(),
		VRuntime:   task.GetVRuntime(),
		TimeSlice:  task.GetTimeSlice(),
	}
}


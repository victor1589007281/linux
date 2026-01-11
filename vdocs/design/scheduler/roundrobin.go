package scheduler

import (
	"container/list"
	"context"
	"sync"
	"time"
)

// RoundRobinScheduler implements Round-Robin scheduling with time slicing
// This is inspired by Linux kernel's SCHED_RR real-time scheduling policy.
//
// Key concepts from Linux kernel (kernel/sched/rt.c):
// 1. Each task gets a fixed time slice (sched_rr_timeslice)
// 2. When time slice expires, task is moved to the end of the queue
// 3. Tasks with equal priority are scheduled in round-robin fashion
//
// Reference: task_tick_rt() in kernel/sched/rt.c:
//   if (--p->rt.time_slice)
//       return;
//   p->rt.time_slice = sched_rr_timeslice;
//   requeue_task_rt(rq, p, 0);
//   resched_curr(rq);
type RoundRobinScheduler struct {
	*BaseScheduler
	queue       *list.List
	current     *TaskWrapper
	timeSlice   time.Duration
	mu          sync.RWMutex
	running     bool
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	tickerStop  chan struct{}
}

// NewRoundRobinScheduler creates a new Round-Robin scheduler
func NewRoundRobinScheduler(opts ...SchedulerOption) *RoundRobinScheduler {
	config := DefaultSchedulerConfig()
	for _, opt := range opts {
		opt(config)
	}

	return &RoundRobinScheduler{
		BaseScheduler: NewBaseScheduler("roundrobin", opts...),
		queue:         list.New(),
		timeSlice:     config.TimeSlice,
		tickerStop:    make(chan struct{}),
	}
}

// Name returns the scheduler name
func (s *RoundRobinScheduler) Name() string {
	return "roundrobin"
}

// Enqueue adds a task to the round-robin queue
// The task is wrapped with scheduling metadata including initial time slice
func (s *RoundRobinScheduler) Enqueue(task Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running && s.ctx != nil {
		return ErrSchedulerStopped
	}

	wrapper := NewTaskWrapper(task)
	wrapper.TimeSlice = s.timeSlice
	s.queue.PushBack(wrapper)
	return nil
}

// Dequeue removes and returns the next task to run
func (s *RoundRobinScheduler) Dequeue() (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.queue.Len() == 0 {
		return nil, ErrQueueEmpty
	}

	elem := s.queue.Front()
	wrapper := s.queue.Remove(elem).(*TaskWrapper)
	return wrapper.Task, nil
}

// Pick returns the next task without removing it
func (s *RoundRobinScheduler) Pick() (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.queue.Len() == 0 {
		return nil, ErrQueueEmpty
	}

	wrapper := s.queue.Front().Value.(*TaskWrapper)
	return wrapper.Task, nil
}

// Len returns the number of tasks in the queue
func (s *RoundRobinScheduler) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.queue.Len()
}

// Start starts the scheduler
func (s *RoundRobinScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.tickerStop = make(chan struct{})
	s.mu.Unlock()

	// Start the tick goroutine (scheduler tick)
	s.wg.Add(1)
	go s.tick()

	// Start worker goroutines
	for i := 0; i < s.config.WorkerCount; i++ {
		s.wg.Add(1)
		go s.worker(i)
	}

	return nil
}

// Stop stops the scheduler
func (s *RoundRobinScheduler) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.cancel()
	close(s.tickerStop)
	s.mu.Unlock()

	s.wg.Wait()
	return nil
}

// tick implements the scheduler tick functionality
// This is inspired by Linux kernel's sched_tick() function
// which is called by the timer interrupt at HZ frequency.
//
// Reference: kernel/sched/core.c - sched_tick()
func (s *RoundRobinScheduler) tick() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.config.TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.tickerStop:
			return
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.handleTick()
		}
	}
}

// handleTick processes a single tick event
// This decrements the current task's time slice and reschedules if needed
func (s *RoundRobinScheduler) handleTick() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.current == nil {
		return
	}

	// Decrement time slice
	s.current.TimeSlice -= s.config.TickInterval

	// Check if time slice expired
	if s.current.TimeSlice <= 0 {
		// Reset time slice and requeue
		s.current.TimeSlice = s.timeSlice
		s.queue.PushBack(s.current)
		s.current = nil
	}
}

// worker processes tasks from the queue
func (s *RoundRobinScheduler) worker(id int) {
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
func (s *RoundRobinScheduler) processNextTask() {
	s.mu.Lock()

	if s.queue.Len() == 0 {
		s.mu.Unlock()
		time.Sleep(s.config.TickInterval)
		return
	}

	// Pick next task
	elem := s.queue.Front()
	wrapper := s.queue.Remove(elem).(*TaskWrapper)
	s.current = wrapper
	wrapper.StartedAt = time.Now()
	s.mu.Unlock()

	// Execute task with timeout based on time slice
	ctx, cancel := context.WithTimeout(s.ctx, s.timeSlice)
	defer cancel()

	s.notifyTaskStart(wrapper.Task)
	err := wrapper.Task.Execute(ctx)
	s.notifyTaskComplete(wrapper.Task, err)

	s.mu.Lock()
	s.current = nil
	s.mu.Unlock()
}

// Requeue puts the current task back to the queue with reset time slice
// This is called when a task voluntarily yields or when preempted
func (s *RoundRobinScheduler) Requeue(task Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	wrapper := NewTaskWrapper(task)
	wrapper.TimeSlice = s.timeSlice
	s.queue.PushBack(wrapper)
	return nil
}

// GetTimeSlice returns the configured time slice
func (s *RoundRobinScheduler) GetTimeSlice() time.Duration {
	return s.timeSlice
}

// SetTimeSlice sets the time slice for new tasks
func (s *RoundRobinScheduler) SetTimeSlice(slice time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.timeSlice = slice
}


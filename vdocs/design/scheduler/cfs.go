package scheduler

import (
	"container/heap"
	"context"
	"sync"
	"time"
)

// CFSScheduler implements Completely Fair Scheduler
// This is inspired by Linux kernel's CFS (Completely Fair Scheduler)
// which uses virtual runtime (vruntime) to achieve fair CPU allocation.
//
// Key concepts from Linux kernel (kernel/sched/fair.c):
// 1. Each task has a virtual runtime (vruntime) that tracks CPU usage
// 2. Tasks with lower vruntime get scheduled first (more "deserving")
// 3. vruntime increases slower for higher-weight tasks
// 4. Uses a red-black tree for O(log n) operations
//
// vruntime calculation (from calc_delta_fair):
//   vruntime += delta_exec * NICE_0_LOAD / weight
//
// Reference: kernel/sched/fair.c:
//   static void update_curr(struct cfs_rq *cfs_rq)
//   {
//       curr->vruntime += calc_delta_fair(delta_exec, curr);
//   }
type CFSScheduler struct {
	*BaseScheduler
	rq          *cfsRunQueue
	minVruntime uint64      // Tracks minimum vruntime in the tree
	mu          sync.RWMutex
	running     bool
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	tickerStop  chan struct{}
}

// NICE_0_LOAD is the default weight for nice 0 tasks
// This value is from Linux kernel
const NICE_0_LOAD = 1024

// cfsRunQueue implements a min-heap based on vruntime
// In Linux, this would be a red-black tree, but heap provides similar O(log n) performance
type cfsRunQueue struct {
	tasks []*cfsEntity
}

// cfsEntity represents a schedulable entity in CFS
// Inspired by Linux's sched_entity structure
type cfsEntity struct {
	task     Task
	vruntime uint64
	weight   uint64
	index    int
}

// Heap interface implementation for cfsRunQueue
func (rq *cfsRunQueue) Len() int { return len(rq.tasks) }

func (rq *cfsRunQueue) Less(i, j int) bool {
	// Lower vruntime = higher priority (gets scheduled first)
	return rq.tasks[i].vruntime < rq.tasks[j].vruntime
}

func (rq *cfsRunQueue) Swap(i, j int) {
	rq.tasks[i], rq.tasks[j] = rq.tasks[j], rq.tasks[i]
	rq.tasks[i].index = i
	rq.tasks[j].index = j
}

func (rq *cfsRunQueue) Push(x interface{}) {
	n := len(rq.tasks)
	entity := x.(*cfsEntity)
	entity.index = n
	rq.tasks = append(rq.tasks, entity)
}

func (rq *cfsRunQueue) Pop() interface{} {
	old := rq.tasks
	n := len(old)
	entity := old[n-1]
	old[n-1] = nil
	entity.index = -1
	rq.tasks = old[0 : n-1]
	return entity
}

// NewCFSScheduler creates a new CFS scheduler
func NewCFSScheduler(opts ...SchedulerOption) *CFSScheduler {
	return &CFSScheduler{
		BaseScheduler: NewBaseScheduler("cfs", opts...),
		rq:            &cfsRunQueue{tasks: make([]*cfsEntity, 0)},
		minVruntime:   0,
		tickerStop:    make(chan struct{}),
	}
}

// Name returns the scheduler name
func (s *CFSScheduler) Name() string {
	return "cfs"
}

// Enqueue adds a task to the CFS run queue
// New tasks are placed based on their vruntime relative to min_vruntime
func (s *CFSScheduler) Enqueue(task Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running && s.ctx != nil {
		return ErrSchedulerStopped
	}

	// Get task weight (or use default)
	weight := task.Weight()
	if weight == 0 {
		weight = NICE_0_LOAD
	}

	// Initialize vruntime for new tasks
	// New tasks start at min_vruntime to prevent starvation
	vruntime := task.GetVRuntime()
	if vruntime == 0 {
		vruntime = s.minVruntime
	}

	entity := &cfsEntity{
		task:     task,
		vruntime: vruntime,
		weight:   weight,
	}

	heap.Push(s.rq, entity)
	return nil
}

// Dequeue removes and returns the task with the smallest vruntime
// Time complexity: O(log n)
func (s *CFSScheduler) Dequeue() (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.rq.Len() == 0 {
		return nil, ErrQueueEmpty
	}

	entity := heap.Pop(s.rq).(*cfsEntity)
	return entity.task, nil
}

// Pick returns the task with smallest vruntime without removing it
func (s *CFSScheduler) Pick() (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.rq.Len() == 0 {
		return nil, ErrQueueEmpty
	}

	return s.rq.tasks[0].task, nil
}

// Len returns the number of tasks in the queue
func (s *CFSScheduler) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rq.Len()
}

// Start starts the scheduler
func (s *CFSScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.tickerStop = make(chan struct{})
	s.mu.Unlock()

	// Start worker goroutines
	for i := 0; i < s.config.WorkerCount; i++ {
		s.wg.Add(1)
		go s.worker(i)
	}

	return nil
}

// Stop stops the scheduler
func (s *CFSScheduler) Stop() error {
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

// worker processes tasks from the queue
func (s *CFSScheduler) worker(id int) {
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
func (s *CFSScheduler) processNextTask() {
	s.mu.Lock()

	if s.rq.Len() == 0 {
		s.mu.Unlock()
		time.Sleep(s.config.TickInterval)
		return
	}

	// Pick task with smallest vruntime
	entity := heap.Pop(s.rq).(*cfsEntity)
	s.mu.Unlock()

	// Execute task and measure runtime
	startTime := time.Now()

	ctx, cancel := context.WithTimeout(s.ctx, s.config.TimeSlice)
	defer cancel()

	s.notifyTaskStart(entity.task)
	err := entity.task.Execute(ctx)
	s.notifyTaskComplete(entity.task, err)

	// Update vruntime based on actual execution time
	deltaExec := uint64(time.Since(startTime).Nanoseconds())
	s.updateVruntime(entity, deltaExec)

	// If task is not complete and needs more CPU time, requeue it
	// (In a real implementation, you'd check task state here)
}

// updateVruntime updates the virtual runtime of an entity
// This implements the core CFS fairness mechanism
//
// Formula: vruntime += delta_exec * NICE_0_LOAD / weight
// Higher weight = slower vruntime growth = more CPU time
func (s *CFSScheduler) updateVruntime(entity *cfsEntity, deltaExec uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Calculate vruntime delta
	// delta_vruntime = delta_exec * NICE_0_LOAD / weight
	deltaVruntime := deltaExec * NICE_0_LOAD / entity.weight

	entity.vruntime += deltaVruntime
	entity.task.SetVRuntime(entity.vruntime)

	// Update min_vruntime
	// min_vruntime only moves forward, never backward
	if entity.vruntime > s.minVruntime && s.rq.Len() > 0 {
		s.minVruntime = s.rq.tasks[0].vruntime
	}
}

// GetMinVruntime returns the current minimum vruntime
func (s *CFSScheduler) GetMinVruntime() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.minVruntime
}

// calcDeltaFair calculates the fair delta for vruntime
// This is the Go equivalent of Linux's calc_delta_fair()
func calcDeltaFair(delta uint64, weight uint64) uint64 {
	if weight == NICE_0_LOAD {
		return delta
	}
	return delta * NICE_0_LOAD / weight
}

// EntityLag calculates the lag of an entity
// Lag represents how much behind or ahead the entity is
// compared to the ideal fair share
//
// Reference: kernel/sched/fair.c - entity_lag()
func (s *CFSScheduler) EntityLag(entity *cfsEntity) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	avgVruntime := s.avgVruntime()
	return int64(avgVruntime) - int64(entity.vruntime)
}

// avgVruntime calculates the average vruntime of all entities
// Reference: kernel/sched/fair.c - avg_vruntime()
func (s *CFSScheduler) avgVruntime() uint64 {
	if s.rq.Len() == 0 {
		return s.minVruntime
	}

	var totalWeight uint64
	var weightedSum uint64

	for _, e := range s.rq.tasks {
		totalWeight += e.weight
		weightedSum += (e.vruntime - s.minVruntime) * e.weight
	}

	if totalWeight == 0 {
		return s.minVruntime
	}

	return s.minVruntime + weightedSum/totalWeight
}

// IsEntityEligible checks if an entity is eligible to run
// An entity is eligible if its vruntime <= average vruntime
//
// Reference: kernel/sched/fair.c - entity_eligible()
func (s *CFSScheduler) IsEntityEligible(entity *cfsEntity) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return entity.vruntime <= s.avgVruntime()
}


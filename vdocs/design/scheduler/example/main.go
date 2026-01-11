// Package main demonstrates the usage of various schedulers
package main

import (
	"context"
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"

	scheduler "github.com/example/scheduler"
)

func main() {
	fmt.Println("=== Abstract Resource Scheduler Demo ===")
	fmt.Println()

	// Demo 1: FIFO Scheduler
	demoFIFO()

	// Demo 2: Round-Robin Scheduler
	demoRoundRobin()

	// Demo 3: CFS Scheduler
	demoCFS()

	// Demo 4: Priority Scheduler
	demoPriority()

	// Demo 5: Deadline Scheduler
	demoDeadline()

	// Demo 6: Async (io_uring style) Scheduler
	demoAsync()
}

func demoFIFO() {
	fmt.Println("--- Demo 1: FIFO Scheduler ---")

	sched := scheduler.NewFIFOScheduler()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var completed int32

	// Add tasks
	for i := 0; i < 5; i++ {
		taskID := fmt.Sprintf("fifo-task-%d", i)
		idx := i
		task := scheduler.NewSimpleTask(taskID, func(ctx context.Context) error {
			fmt.Printf("  [FIFO] Executing task %d\n", idx)
			atomic.AddInt32(&completed, 1)
			time.Sleep(50 * time.Millisecond)
			return nil
		})
		sched.Enqueue(task)
	}

	sched.Start(ctx)
	time.Sleep(500 * time.Millisecond)
	sched.Stop()

	fmt.Printf("  Completed: %d tasks\n\n", atomic.LoadInt32(&completed))
}

func demoRoundRobin() {
	fmt.Println("--- Demo 2: Round-Robin Scheduler ---")

	sched := scheduler.NewRoundRobinScheduler(
		scheduler.WithSchedulerTimeSlice(100 * time.Millisecond),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Add long-running tasks
	for i := 0; i < 3; i++ {
		taskID := fmt.Sprintf("rr-task-%d", i)
		idx := i
		task := scheduler.NewSimpleTask(taskID, func(ctx context.Context) error {
			// Simulate work that takes multiple time slices
			for j := 0; j < 3; j++ {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					fmt.Printf("  [RR] Task %d - iteration %d\n", idx, j)
					time.Sleep(30 * time.Millisecond)
				}
			}
			return nil
		})
		sched.Enqueue(task)
	}

	fmt.Printf("  Time slice: %v\n", sched.GetTimeSlice())

	sched.Start(ctx)
	time.Sleep(500 * time.Millisecond)
	sched.Stop()

	fmt.Println()
}

func demoCFS() {
	fmt.Println("--- Demo 3: CFS (Completely Fair Scheduler) ---")

	sched := scheduler.NewCFSScheduler()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Add tasks with different weights
	weights := []struct {
		name   string
		weight uint64
	}{
		{"low-priority", 512},   // Gets less CPU time
		{"normal", 1024},        // Default weight
		{"high-priority", 2048}, // Gets more CPU time
	}

	for _, w := range weights {
		name := w.name
		weight := w.weight
		task := scheduler.NewSimpleTask(name, func(ctx context.Context) error {
			fmt.Printf("  [CFS] Executing %s (weight=%d)\n", name, weight)
			time.Sleep(50 * time.Millisecond)
			return nil
		}, scheduler.WithWeight(weight))
		sched.Enqueue(task)
	}

	fmt.Printf("  Min vruntime: %d\n", sched.GetMinVruntime())

	sched.Start(ctx)
	time.Sleep(300 * time.Millisecond)
	sched.Stop()

	fmt.Println()
}

func demoPriority() {
	fmt.Println("--- Demo 4: Priority Scheduler ---")

	sched := scheduler.NewPriorityScheduler()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Add tasks with different priorities (lower = higher priority)
	priorities := []int{50, 10, 90, 5, 75}
	for _, p := range priorities {
		priority := p
		taskID := fmt.Sprintf("priority-%d", priority)
		task := scheduler.NewSimpleTask(taskID, func(ctx context.Context) error {
			fmt.Printf("  [Priority] Executing task with priority %d\n", priority)
			time.Sleep(30 * time.Millisecond)
			return nil
		}, scheduler.WithPriority(priority))
		sched.Enqueue(task)
	}

	fmt.Printf("  Highest priority in queue: %d\n", sched.GetHighestPriority())

	sched.Start(ctx)
	time.Sleep(500 * time.Millisecond)
	sched.Stop()

	fmt.Println()
}

func demoDeadline() {
	fmt.Println("--- Demo 5: Deadline Scheduler ---")

	sched := scheduler.NewDeadlineScheduler()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now()

	// Add tasks with different deadlines
	deadlines := []time.Duration{
		500 * time.Millisecond,
		100 * time.Millisecond, // Earliest - should run first
		300 * time.Millisecond,
	}

	for i, d := range deadlines {
		deadline := now.Add(d)
		taskID := fmt.Sprintf("deadline-task-%d", i)
		dl := d
		task := scheduler.NewSimpleTask(taskID, func(ctx context.Context) error {
			fmt.Printf("  [Deadline] Executing task with deadline in %v\n", dl)
			time.Sleep(30 * time.Millisecond)
			return nil
		}, scheduler.WithDeadline(deadline))
		sched.Enqueue(task)
	}

	if nearest, ok := sched.GetNearestDeadline(); ok {
		fmt.Printf("  Nearest deadline: %v from now\n", nearest.Sub(now))
	}

	sched.Start(ctx)
	time.Sleep(600 * time.Millisecond)
	sched.Stop()

	fmt.Println()
}

func demoAsync() {
	fmt.Println("--- Demo 6: Async Scheduler (io_uring style) ---")

	sched := scheduler.NewAsyncScheduler()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sched.Start(ctx)

	// Submit multiple tasks asynchronously
	fmt.Println("  Submitting tasks...")
	results := make([]<-chan scheduler.Result, 5)

	for i := 0; i < 5; i++ {
		taskID := fmt.Sprintf("async-task-%d", i)
		idx := i
		sleepTime := time.Duration(rand.Intn(100)) * time.Millisecond

		task := scheduler.NewSimpleTask(taskID, func(ctx context.Context) error {
			fmt.Printf("  [Async] Executing task %d (sleep %v)\n", idx, sleepTime)
			time.Sleep(sleepTime)
			return nil
		})

		ch, err := sched.Submit(task, uint64(i))
		if err != nil {
			fmt.Printf("  Error submitting task %d: %v\n", i, err)
			continue
		}
		results[i] = ch
	}

	// Wait for all results
	fmt.Println("  Waiting for completions...")
	for i, ch := range results {
		if ch == nil {
			continue
		}
		select {
		case result := <-ch:
			if result.Error != nil {
				fmt.Printf("  Task %d failed: %v\n", i, result.Error)
			} else {
				fmt.Printf("  Task %d completed successfully\n", i)
			}
		case <-time.After(2 * time.Second):
			fmt.Printf("  Task %d timed out\n", i)
		}
	}

	submitted, completed := sched.Stats()
	fmt.Printf("  Stats: submitted=%d, completed=%d\n", submitted, completed)

	sched.Stop()
	fmt.Println()
}


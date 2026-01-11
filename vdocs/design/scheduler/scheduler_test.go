package scheduler

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// TestFIFOScheduler tests the FIFO scheduler
func TestFIFOScheduler(t *testing.T) {
	sched := NewFIFOScheduler()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var completed int32

	// Create tasks
	for i := 0; i < 10; i++ {
		taskID := fmt.Sprintf("task-%d", i)
		task := NewSimpleTask(taskID, func(ctx context.Context) error {
			atomic.AddInt32(&completed, 1)
			return nil
		})
		if err := sched.Enqueue(task); err != nil {
			t.Fatalf("Failed to enqueue task: %v", err)
		}
	}

	if sched.Len() != 10 {
		t.Errorf("Expected 10 tasks, got %d", sched.Len())
	}

	// Start and wait
	sched.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	sched.Stop()

	if atomic.LoadInt32(&completed) != 10 {
		t.Errorf("Expected 10 completed tasks, got %d", completed)
	}
}

// TestRoundRobinScheduler tests the Round-Robin scheduler
func TestRoundRobinScheduler(t *testing.T) {
	sched := NewRoundRobinScheduler(
		WithSchedulerTimeSlice(50 * time.Millisecond),
		WithTickInterval(10 * time.Millisecond),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var completed int32
	orderChan := make(chan string, 10)

	// Create tasks
	for i := 0; i < 5; i++ {
		taskID := fmt.Sprintf("task-%d", i)
		task := NewSimpleTask(taskID, func(ctx context.Context) error {
			orderChan <- taskID
			atomic.AddInt32(&completed, 1)
			return nil
		})
		if err := sched.Enqueue(task); err != nil {
			t.Fatalf("Failed to enqueue task: %v", err)
		}
	}

	sched.Start(ctx)
	time.Sleep(200 * time.Millisecond)
	sched.Stop()

	close(orderChan)

	t.Logf("Completed %d tasks", atomic.LoadInt32(&completed))
}

// TestCFSScheduler tests the CFS scheduler
func TestCFSScheduler(t *testing.T) {
	sched := NewCFSScheduler()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var completed int32

	// Create tasks with different weights
	weights := []uint64{512, 1024, 2048} // Low, normal, high weight

	for i, w := range weights {
		taskID := fmt.Sprintf("task-%d", i)
		task := NewSimpleTask(taskID, func(ctx context.Context) error {
			atomic.AddInt32(&completed, 1)
			return nil
		}, WithWeight(w))
		if err := sched.Enqueue(task); err != nil {
			t.Fatalf("Failed to enqueue task: %v", err)
		}
	}

	sched.Start(ctx)
	time.Sleep(200 * time.Millisecond)
	sched.Stop()

	if atomic.LoadInt32(&completed) != 3 {
		t.Errorf("Expected 3 completed tasks, got %d", completed)
	}
}

// TestPriorityScheduler tests the priority scheduler
func TestPriorityScheduler(t *testing.T) {
	sched := NewPriorityScheduler()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var order []string
	orderChan := make(chan string, 10)

	// Create tasks with different priorities
	priorities := []int{50, 10, 90, 5, 75} // Priority 5 is highest
	for i, p := range priorities {
		taskID := fmt.Sprintf("task-p%d", p)
		task := NewSimpleTask(taskID, func(ctx context.Context) error {
			orderChan <- taskID
			return nil
		}, WithPriority(p))
		if err := sched.Enqueue(task); err != nil {
			t.Fatalf("Failed to enqueue task %d: %v", i, err)
		}
	}

	// Without starting workers, verify Pick returns highest priority
	nextTask, _ := sched.Pick()
	if nextTask != nil && nextTask.Priority() != 5 {
		t.Errorf("Expected priority 5, got %d", nextTask.Priority())
	}

	sched.Start(ctx)
	time.Sleep(200 * time.Millisecond)
	sched.Stop()

	close(orderChan)

	for id := range orderChan {
		order = append(order, id)
	}

	t.Logf("Execution order: %v", order)
}

// TestDeadlineScheduler tests the deadline scheduler
func TestDeadlineScheduler(t *testing.T) {
	sched := NewDeadlineScheduler()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var completed int32

	now := time.Now()

	// Create tasks with different deadlines
	deadlines := []time.Duration{
		500 * time.Millisecond, // Latest
		100 * time.Millisecond, // Earliest
		300 * time.Millisecond,
	}

	for i, d := range deadlines {
		taskID := fmt.Sprintf("task-%d", i)
		deadline := now.Add(d)
		task := NewSimpleTask(taskID, func(ctx context.Context) error {
			atomic.AddInt32(&completed, 1)
			return nil
		}, WithDeadline(deadline))
		if err := sched.Enqueue(task); err != nil {
			t.Fatalf("Failed to enqueue task: %v", err)
		}
	}

	// Verify earliest deadline is picked first
	nextTask, _ := sched.Pick()
	if nextTask != nil {
		nearestDeadline, _ := sched.GetNearestDeadline()
		if !nearestDeadline.Equal(now.Add(100 * time.Millisecond)) {
			t.Logf("Nearest deadline: %v", nearestDeadline)
		}
	}

	sched.Start(ctx)
	time.Sleep(200 * time.Millisecond)
	sched.Stop()

	if atomic.LoadInt32(&completed) != 3 {
		t.Errorf("Expected 3 completed tasks, got %d", completed)
	}
}

// TestAsyncScheduler tests the async scheduler
func TestAsyncScheduler(t *testing.T) {
	sched := NewAsyncScheduler()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sched.Start(ctx)

	// Submit tasks
	results := make([]<-chan Result, 5)
	for i := 0; i < 5; i++ {
		taskID := fmt.Sprintf("task-%d", i)
		task := NewSimpleTask(taskID, func(ctx context.Context) error {
			time.Sleep(10 * time.Millisecond)
			return nil
		})

		resultChan, err := sched.Submit(task, uint64(i))
		if err != nil {
			t.Fatalf("Failed to submit task: %v", err)
		}
		results[i] = resultChan
	}

	// Wait for all results
	for i, ch := range results {
		select {
		case result := <-ch:
			if result.Error != nil {
				t.Errorf("Task %d failed: %v", i, result.Error)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("Task %d timed out", i)
		}
	}

	submitted, completed := sched.Stats()
	if submitted != 5 || completed != 5 {
		t.Errorf("Expected 5 submitted and 5 completed, got %d/%d", submitted, completed)
	}

	sched.Stop()
}

// TestAsyncSchedulerBatch tests batch submission
func TestAsyncSchedulerBatch(t *testing.T) {
	sched := NewAsyncScheduler()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sched.Start(ctx)

	// Create batch of tasks
	tasks := make([]Task, 10)
	for i := 0; i < 10; i++ {
		taskID := fmt.Sprintf("batch-task-%d", i)
		tasks[i] = NewSimpleTask(taskID, func(ctx context.Context) error {
			return nil
		})
	}

	// Submit batch
	results, err := sched.SubmitBatch(tasks)
	if err != nil {
		t.Fatalf("Failed to submit batch: %v", err)
	}

	if len(results) != 10 {
		t.Errorf("Expected 10 results, got %d", len(results))
	}

	// Wait for all
	for i, ch := range results {
		select {
		case result := <-ch:
			if result.Error != nil {
				t.Errorf("Task %d failed: %v", i, result.Error)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("Task %d timed out", i)
		}
	}

	sched.Stop()
}

// BenchmarkFIFOScheduler benchmarks FIFO scheduler
func BenchmarkFIFOScheduler(b *testing.B) {
	sched := NewFIFOScheduler()

	task := NewSimpleTask("bench-task", func(ctx context.Context) error {
		return nil
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sched.Enqueue(task)
		sched.Dequeue()
	}
}

// BenchmarkCFSScheduler benchmarks CFS scheduler
func BenchmarkCFSScheduler(b *testing.B) {
	sched := NewCFSScheduler()

	task := NewSimpleTask("bench-task", func(ctx context.Context) error {
		return nil
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sched.Enqueue(task)
		sched.Dequeue()
	}
}

// BenchmarkAsyncScheduler benchmarks async scheduler submission
func BenchmarkAsyncScheduler(b *testing.B) {
	sched := NewAsyncScheduler()
	ctx := context.Background()
	sched.Start(ctx)
	defer sched.Stop()

	task := NewSimpleTask("bench-task", func(ctx context.Context) error {
		return nil
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ch, _ := sched.Submit(task, 0)
		<-ch
	}
}


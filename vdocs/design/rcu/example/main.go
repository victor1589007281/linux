// Package main demonstrates the usage of RCU (Read-Copy-Update)
package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	rcu "github.com/example/rcu"
)

// User represents a user record
type User struct {
	ID    int
	Name  string
	Email string
}

func main() {
	fmt.Println("=== RCU (Read-Copy-Update) Demo ===")
	fmt.Println()

	// Demo 1: Basic RCU usage
	demoBasicRCU()

	// Demo 2: Concurrent readers with updates
	demoConcurrentAccess()

	// Demo 3: RCU-protected list
	demoRCUList()

	// Demo 4: Performance comparison
	demoPerformance()
}

func demoBasicRCU() {
	fmt.Println("--- Demo 1: Basic RCU Usage ---")

	r := rcu.New()
	r.Start()
	defer r.Stop()

	// Create initial data
	user := &User{ID: 1, Name: "Alice", Email: "alice@example.com"}
	var userPtr atomic.Pointer[User]
	userPtr.Store(user)

	// Reader: read data within critical section
	fmt.Println("  Reader reading data...")
	r.ReadLock()
	current := userPtr.Load()
	fmt.Printf("    User: %+v\n", *current)
	r.ReadUnlock()

	// Writer: update data using copy-update pattern
	fmt.Println("\n  Writer updating data...")
	oldUser := userPtr.Load()
	newUser := &User{
		ID:    oldUser.ID,
		Name:  "Alice Smith",
		Email: "alice.smith@example.com",
	}
	userPtr.Store(newUser)
	fmt.Printf("    Updated user: %+v\n", *newUser)

	// Wait for grace period (safe to free oldUser after this)
	r.Synchronize()
	fmt.Println("    Grace period completed, old data can be freed")

	// Reader: read updated data
	r.ReadLock()
	current = userPtr.Load()
	fmt.Printf("    Current user: %+v\n", *current)
	r.ReadUnlock()

	stats := r.Stats()
	fmt.Printf("\n  RCU Stats: GPs=%d, Callbacks=%d, ReadLocks=%d\n",
		stats.GracePeriods, stats.Callbacks, stats.ReadLocks)
	fmt.Println()
}

func demoConcurrentAccess() {
	fmt.Println("--- Demo 2: Concurrent Readers with Updates ---")

	r := rcu.New(rcu.WithGracePeriod(time.Millisecond * 5))
	r.Start()
	defer r.Stop()

	// Shared counter
	type Counter struct {
		Value int64
	}
	var counterPtr atomic.Pointer[Counter]
	counterPtr.Store(&Counter{Value: 0})

	var wg sync.WaitGroup
	numReaders := 5
	numUpdates := 10

	// Start readers
	fmt.Printf("  Starting %d readers...\n", numReaders)
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				r.ReadLock()
				c := counterPtr.Load()
				if c != nil && j == 10 {
					fmt.Printf("    Reader %d sees value: %d\n", id, c.Value)
				}
				r.ReadUnlock()
				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	// Start writer
	fmt.Printf("  Starting writer with %d updates...\n", numUpdates)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < numUpdates; i++ {
			// Copy-Update pattern
			old := counterPtr.Load()
			newCounter := &Counter{Value: old.Value + 1}
			counterPtr.Store(newCounter)

			// Wait for grace period
			r.Synchronize()

			time.Sleep(time.Millisecond * 5)
		}
	}()

	wg.Wait()

	finalCounter := counterPtr.Load()
	fmt.Printf("  Final counter value: %d\n", finalCounter.Value)

	stats := r.Stats()
	fmt.Printf("  RCU Stats: GPs=%d, ReadLocks=%d\n", stats.GracePeriods, stats.ReadLocks)
	fmt.Println()
}

func demoRCUList() {
	fmt.Println("--- Demo 3: RCU-Protected List ---")

	r := rcu.New()
	r.Start()
	defer r.Stop()

	list := rcu.NewRCUList[string](r)

	// Add elements
	fmt.Println("  Adding elements to list...")
	list.Prepend("Charlie")
	list.Prepend("Bob")
	list.Prepend("Alice")

	fmt.Printf("  List length: %d\n", list.Len())

	// Iterate
	fmt.Println("  List contents:")
	list.ForEach(func(name string) bool {
		fmt.Printf("    - %s\n", name)
		return true
	})

	// Find
	if val, found := list.Find(func(s string) bool { return s == "Bob" }); found {
		fmt.Printf("  Found: %s\n", *val)
	}

	// Remove
	fmt.Println("\n  Removing 'Bob'...")
	list.Remove(func(s string) bool { return s == "Bob" })

	fmt.Printf("  List length after remove: %d\n", list.Len())
	fmt.Println("  List contents after remove:")
	list.ForEach(func(name string) bool {
		fmt.Printf("    - %s\n", name)
		return true
	})
	fmt.Println()
}

func demoPerformance() {
	fmt.Println("--- Demo 4: Performance Comparison ---")

	iterations := 100000

	// RCU
	r := rcu.New(rcu.WithGracePeriod(time.Millisecond))
	r.Start()
	defer r.Stop()

	start := time.Now()
	for i := 0; i < iterations; i++ {
		r.ReadLock()
		r.ReadUnlock()
	}
	rcuTime := time.Since(start)

	// sync.RWMutex
	var rwmu sync.RWMutex
	start = time.Now()
	for i := 0; i < iterations; i++ {
		rwmu.RLock()
		rwmu.RUnlock()
	}
	rwmuTime := time.Since(start)

	fmt.Printf("  %d read lock/unlock operations:\n", iterations)
	fmt.Printf("    RCU:     %v (%.2f ns/op)\n", rcuTime, float64(rcuTime.Nanoseconds())/float64(iterations))
	fmt.Printf("    RWMutex: %v (%.2f ns/op)\n", rwmuTime, float64(rwmuTime.Nanoseconds())/float64(iterations))
	fmt.Printf("    RCU is %.2fx of RWMutex time\n", float64(rcuTime)/float64(rwmuTime))
	fmt.Println()

	// Parallel read performance
	fmt.Println("  Parallel read performance (8 goroutines):")
	
	var wg sync.WaitGroup
	parallelIterations := iterations / 8

	// RCU parallel
	start = time.Now()
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < parallelIterations; i++ {
				r.ReadLock()
				r.ReadUnlock()
			}
		}()
	}
	wg.Wait()
	rcuParallelTime := time.Since(start)

	// RWMutex parallel
	start = time.Now()
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < parallelIterations; i++ {
				rwmu.RLock()
				rwmu.RUnlock()
			}
		}()
	}
	wg.Wait()
	rwmuParallelTime := time.Since(start)

	fmt.Printf("    RCU:     %v\n", rcuParallelTime)
	fmt.Printf("    RWMutex: %v\n", rwmuParallelTime)
	fmt.Printf("    RCU is %.2fx of RWMutex time (parallel)\n", float64(rcuParallelTime)/float64(rwmuParallelTime))

	fmt.Println("\n  Note: RCU shines in read-heavy workloads with")
	fmt.Println("  many concurrent readers and infrequent updates.")
}


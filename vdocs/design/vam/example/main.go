// Package main demonstrates the usage of Virtual Address Management
package main

import (
	"fmt"

	vam "github.com/example/vam"
)

func main() {
	fmt.Println("=== Virtual Address Management Demo ===")
	fmt.Println()

	// Demo 1: Basic allocation with lazy allocation
	demoLazyAllocation()

	// Demo 2: Overcommit
	demoOvercommit()

	// Demo 3: Demand paging (fault handling)
	demoDemandPaging()

	// Demo 4: Different overcommit policies
	demoOvercommitPolicies()
}

func demoLazyAllocation() {
	fmt.Println("--- Demo 1: Lazy Allocation ---")

	// Create VAS with 1MB physical backend
	vas := vam.NewVirtualAddressSpace(
		vam.WithBackend(vam.NewMemoryBackend(1*vam.MB)),
	)

	// Allocate 512KB virtual space
	fmt.Println("  Allocating 512KB virtual space...")
	addr, err := vas.Allocate(512 * vam.KB)
	if err != nil {
		fmt.Printf("  Error: %v\n", err)
		return
	}
	fmt.Printf("  Allocated at virtual address: 0x%x\n", addr)

	// Check stats - physical should be 0
	stats := vas.Stats()
	fmt.Printf("  Virtual: %d bytes, Physical: %d bytes\n",
		stats.TotalVirtual, stats.TotalPhysical)
	fmt.Println("  (No physical resources allocated yet - lazy allocation!)")

	// Access the memory - this triggers a page fault
	fmt.Println("\n  Accessing memory...")
	_, err = vas.Access(addr)
	if err != nil {
		fmt.Printf("  Error accessing: %v\n", err)
		return
	}

	// Check stats again
	stats = vas.Stats()
	fmt.Printf("  Virtual: %d bytes, Physical: %d bytes\n",
		stats.TotalVirtual, stats.TotalPhysical)
	fmt.Printf("  Minor faults: %d (demand paging triggered!)\n", stats.MinorFaults)

	// Clean up
	vas.Free(addr)
	fmt.Println()
}

func demoOvercommit() {
	fmt.Println("--- Demo 2: Overcommit (200%) ---")

	// Create VAS with 1MB physical but 200% overcommit ratio
	vas := vam.NewVirtualAddressSpace(
		vam.WithBackend(vam.NewMemoryBackend(1*vam.MB)),
		vam.WithOvercommitPolicy(vam.OvercommitGuess),
		vam.WithOvercommitRatio(200), // 200% = can commit 2MB with 1MB physical
	)

	// Allocate 1.5MB virtual space (more than physical!)
	fmt.Println("  Physical backend: 1MB")
	fmt.Println("  Overcommit ratio: 200%")
	fmt.Println("  Attempting to allocate 1.5MB virtual space...")

	addr1, err := vas.Allocate(1 * vam.MB)
	if err != nil {
		fmt.Printf("  Error allocating 1MB: %v\n", err)
		return
	}
	fmt.Printf("  Allocated 1MB at: 0x%x\n", addr1)

	addr2, err := vas.Allocate(512 * vam.KB)
	if err != nil {
		fmt.Printf("  Error allocating 512KB: %v\n", err)
		return
	}
	fmt.Printf("  Allocated 512KB at: 0x%x\n", addr2)

	// Show overcommit stats
	ocStats := vas.GetOvercommitStats()
	fmt.Printf("\n  Overcommit Statistics:\n")
	fmt.Printf("    Policy: %s\n", ocStats.Policy)
	fmt.Printf("    Committed: %d bytes\n", ocStats.Committed)
	fmt.Printf("    Limit: %d bytes\n", ocStats.Limit)
	fmt.Printf("    Usage: %.2f%%\n", ocStats.UsagePercent)

	fmt.Println("  (Allocated 1.5MB virtual with only 1MB physical - overcommit works!)")

	vas.Free(addr1)
	vas.Free(addr2)
	fmt.Println()
}

func demoDemandPaging() {
	fmt.Println("--- Demo 3: Demand Paging ---")

	vas := vam.NewVirtualAddressSpace(
		vam.WithBackend(vam.NewMemoryBackend(1*vam.MB)),
	)

	// Allocate multiple regions
	fmt.Println("  Allocating 4 regions of 4KB each...")
	addrs := make([]vam.VirtualAddress, 4)
	for i := 0; i < 4; i++ {
		addr, _ := vas.Allocate(4 * vam.KB)
		addrs[i] = addr
		fmt.Printf("  Region %d at: 0x%x\n", i, addr)
	}

	stats := vas.Stats()
	fmt.Printf("\n  Before access: Physical = %d bytes, Faults = %d\n",
		stats.TotalPhysical, stats.MinorFaults)

	// Access only some regions
	fmt.Println("\n  Accessing regions 0 and 2 only...")
	vas.Access(addrs[0])
	vas.Access(addrs[2])

	stats = vas.Stats()
	fmt.Printf("  After access: Physical = %d bytes, Faults = %d\n",
		stats.TotalPhysical, stats.MinorFaults)
	fmt.Println("  (Only accessed pages are physically allocated!)")

	// Write data
	fmt.Println("\n  Writing data to region 0...")
	err := vas.Write(addrs[0], []byte("Hello, Demand Paging!"))
	if err != nil {
		fmt.Printf("  Error writing: %v\n", err)
	} else {
		fmt.Println("  Write successful!")
	}

	// Clean up
	for _, addr := range addrs {
		vas.Free(addr)
	}
	fmt.Println()
}

func demoOvercommitPolicies() {
	fmt.Println("--- Demo 4: Overcommit Policies ---")

	policies := []struct {
		name   string
		policy vam.OvercommitPolicy
	}{
		{"OVERCOMMIT_ALWAYS", vam.OvercommitAlways},
		{"OVERCOMMIT_GUESS", vam.OvercommitGuess},
		{"OVERCOMMIT_NEVER", vam.OvercommitNever},
	}

	for _, p := range policies {
		fmt.Printf("\n  Testing %s:\n", p.name)

		vas := vam.NewVirtualAddressSpace(
			vam.WithBackend(vam.NewMemoryBackend(100*vam.KB)),
			vam.WithOvercommitPolicy(p.policy),
		)

		// Try to allocate more than physical
		_, err1 := vas.Allocate(50 * vam.KB)
		_, err2 := vas.Allocate(50 * vam.KB)
		_, err3 := vas.Allocate(50 * vam.KB) // This might fail for OVERCOMMIT_NEVER

		fmt.Printf("    50KB #1: %v\n", statusStr(err1))
		fmt.Printf("    50KB #2: %v\n", statusStr(err2))
		fmt.Printf("    50KB #3: %v\n", statusStr(err3))
	}

	fmt.Println()
}

func statusStr(err error) string {
	if err == nil {
		return "OK"
	}
	return fmt.Sprintf("FAILED (%v)", err)
}


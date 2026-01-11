package vam

import (
	"testing"
)

// TestMemoryBackend tests the memory backend
func TestMemoryBackend(t *testing.T) {
	backend := NewMemoryBackend(1 * MB)

	// Test allocation
	addr1, err := backend.Allocate(4 * KB)
	if err != nil {
		t.Fatalf("Failed to allocate: %v", err)
	}

	addr2, err := backend.Allocate(8 * KB)
	if err != nil {
		t.Fatalf("Failed to allocate: %v", err)
	}

	if addr1 == addr2 {
		t.Error("Allocated same address twice")
	}

	// Test write and read
	testData := []byte("Hello, VAM!")
	err = backend.Write(addr1, 0, testData)
	if err != nil {
		t.Fatalf("Failed to write: %v", err)
	}

	data, err := backend.Read(addr1, 0, uint64(len(testData)))
	if err != nil {
		t.Fatalf("Failed to read: %v", err)
	}

	if string(data) != string(testData) {
		t.Errorf("Data mismatch: got %s, want %s", data, testData)
	}

	// Test free
	err = backend.Free(addr1, 4*KB)
	if err != nil {
		t.Fatalf("Failed to free: %v", err)
	}

	// Test stats
	stats := backend.Stats()
	if stats.TotalCapacity != 1*MB {
		t.Errorf("Wrong total capacity: got %d, want %d", stats.TotalCapacity, 1*MB)
	}
}

// TestVirtualAddressSpace tests the virtual address space
func TestVirtualAddressSpace(t *testing.T) {
	vas := NewVirtualAddressSpace(
		WithBackend(NewMemoryBackend(1*MB)),
		WithOvercommitPolicy(OvercommitGuess),
		WithOvercommitRatio(200),
	)

	// Test allocation
	addr1, err := vas.Allocate(4 * KB)
	if err != nil {
		t.Fatalf("Failed to allocate: %v", err)
	}

	if addr1 == InvalidAddress {
		t.Error("Got invalid address")
	}

	// At this point, no physical resource should be allocated
	stats := vas.Stats()
	if stats.TotalVirtual != 4*KB {
		t.Errorf("Wrong virtual total: got %d, want %d", stats.TotalVirtual, 4*KB)
	}
	if stats.TotalPhysical != 0 {
		t.Errorf("Physical should be 0 before access, got %d", stats.TotalPhysical)
	}

	// Access to trigger fault
	_, err = vas.Access(addr1)
	if err != nil {
		t.Fatalf("Failed to access: %v", err)
	}

	// Now physical should be allocated
	stats = vas.Stats()
	if stats.TotalPhysical == 0 {
		t.Error("Physical should be non-zero after access")
	}
	if stats.MinorFaults != 1 {
		t.Errorf("Expected 1 minor fault, got %d", stats.MinorFaults)
	}

	// Test write
	testData := []byte("Test data")
	err = vas.Write(addr1, testData)
	if err != nil {
		t.Fatalf("Failed to write: %v", err)
	}

	// Test free
	err = vas.Free(addr1)
	if err != nil {
		t.Fatalf("Failed to free: %v", err)
	}

	stats = vas.Stats()
	if stats.TotalVirtual != 0 {
		t.Errorf("Virtual should be 0 after free, got %d", stats.TotalVirtual)
	}
}

// TestOvercommit tests the overcommit functionality
func TestOvercommit(t *testing.T) {
	// Create a VAS with 1MB physical, but allow 200% overcommit
	vas := NewVirtualAddressSpace(
		WithBackend(NewMemoryBackend(1*MB)),
		WithOvercommitPolicy(OvercommitGuess),
		WithOvercommitRatio(200),
	)

	// Should be able to allocate more than physical capacity
	addr1, err := vas.Allocate(1 * MB)
	if err != nil {
		t.Fatalf("Failed to allocate 1MB: %v", err)
	}

	addr2, err := vas.Allocate(512 * KB)
	if err != nil {
		t.Fatalf("Failed to allocate 512KB with overcommit: %v", err)
	}

	if addr1 == addr2 {
		t.Error("Got same address for different allocations")
	}

	// Get overcommit stats
	ocStats := vas.GetOvercommitStats()
	t.Logf("Overcommit stats: committed=%d, limit=%d, usage=%.2f%%",
		ocStats.Committed, ocStats.Limit, ocStats.UsagePercent)
}

// TestOvercommitNever tests strict overcommit policy
func TestOvercommitNever(t *testing.T) {
	vas := NewVirtualAddressSpace(
		WithBackend(NewMemoryBackend(1*MB)),
		WithOvercommitPolicy(OvercommitNever),
	)

	// First allocation should succeed
	_, err := vas.Allocate(512 * KB)
	if err != nil {
		t.Fatalf("First allocation failed: %v", err)
	}

	// Second allocation should also succeed
	_, err = vas.Allocate(256 * KB)
	if err != nil {
		t.Fatalf("Second allocation failed: %v", err)
	}

	// This should fail (exceeds limit)
	_, err = vas.Allocate(512 * KB)
	if err == nil {
		t.Error("Expected allocation to fail with OVERCOMMIT_NEVER")
	}
}

// TestLazyAllocation tests that physical resources are allocated lazily
func TestLazyAllocation(t *testing.T) {
	backend := NewMemoryBackend(1 * MB)
	vas := NewVirtualAddressSpace(
		WithBackend(backend),
	)

	// Allocate virtual space
	_, err := vas.Allocate(100 * KB)
	if err != nil {
		t.Fatalf("Failed to allocate: %v", err)
	}

	// Check backend - should have no allocations yet
	backendStats := backend.Stats()
	if backendStats.UsedCapacity != 0 {
		t.Errorf("Backend should have 0 used capacity, got %d", backendStats.UsedCapacity)
	}
}

// TestRegionTree tests the region tree
func TestRegionTree(t *testing.T) {
	tree := NewRegionTree()

	// Insert regions
	r1 := NewVirtualRegion(0x1000, 0x2000, RegionDefault, nil)
	r2 := NewVirtualRegion(0x3000, 0x4000, RegionDefault, nil)
	r3 := NewVirtualRegion(0x5000, 0x6000, RegionDefault, nil)

	tree.Insert(r1)
	tree.Insert(r2)
	tree.Insert(r3)

	// Test find
	found := tree.Find(0x1500)
	if found != r1 {
		t.Error("Failed to find region r1")
	}

	found = tree.Find(0x3500)
	if found != r2 {
		t.Error("Failed to find region r2")
	}

	// Test not found
	found = tree.Find(0x2500)
	if found != nil {
		t.Error("Should not find region in gap")
	}

	// Test gap finding
	gap := tree.FindGap(0x1000, 0)
	if gap < 0x2000 || gap >= 0x3000 {
		t.Errorf("Gap should be between 0x2000 and 0x3000, got %x", gap)
	}

	// Test overlapping
	overlapping := tree.FindOverlapping(0x1500, 0x3500)
	if len(overlapping) != 2 {
		t.Errorf("Expected 2 overlapping regions, got %d", len(overlapping))
	}
}

// TestLRUList tests the LRU list
func TestLRUList(t *testing.T) {
	lru := NewLRUList(10)

	// Add entries
	for i := 0; i < 5; i++ {
		lru.Add(&LRUEntry{
			VAddr: VirtualAddress(i * PageSize),
			PAddr: PhysicalAddress(i * PageSize),
		})
	}

	if lru.Len() != 5 {
		t.Errorf("Expected 5 entries, got %d", lru.Len())
	}

	// Access an entry to promote it
	lru.Access(VirtualAddress(0))

	// Evict - should get the oldest unaccessed entry
	evicted := lru.Evict()
	if evicted == nil {
		t.Fatal("Expected to evict an entry")
	}

	// Should have 4 entries now
	if lru.Len() != 4 {
		t.Errorf("Expected 4 entries after evict, got %d", lru.Len())
	}
}

// TestAddressMapping tests the address mapping
func TestAddressMapping(t *testing.T) {
	mapping := NewAddressMapping()

	entry := PageTableEntry{
		PhysicalAddr: PhysicalAddress(0x10000),
		Flags:        RegionRead | RegionWrite,
		Present:      true,
		PageSize:     PageSize,
	}

	// Map
	err := mapping.Map(VirtualAddress(0x1000), entry)
	if err != nil {
		t.Fatalf("Failed to map: %v", err)
	}

	// Lookup
	found, ok := mapping.Lookup(VirtualAddress(0x1000))
	if !ok {
		t.Fatal("Failed to lookup mapped address")
	}

	if found.PhysicalAddr != entry.PhysicalAddr {
		t.Errorf("Wrong physical address: got %x, want %x",
			found.PhysicalAddr, entry.PhysicalAddr)
	}

	// Lookup with offset
	_, offset, ok := mapping.LookupWithOffset(VirtualAddress(0x1100))
	if !ok {
		t.Fatal("Failed to lookup with offset")
	}
	if offset != 0x100 {
		t.Errorf("Wrong offset: got %x, want %x", offset, 0x100)
	}

	// Unmap
	_, err = mapping.Unmap(VirtualAddress(0x1000))
	if err != nil {
		t.Fatalf("Failed to unmap: %v", err)
	}

	// Should not find after unmap
	_, ok = mapping.Lookup(VirtualAddress(0x1000))
	if ok {
		t.Error("Should not find after unmap")
	}
}

// TestTLB tests the TLB cache
func TestTLB(t *testing.T) {
	tlb := NewTLB(4)

	// Insert entries
	tlb.Insert(VirtualAddress(0x1000), PhysicalAddress(0x10000))
	tlb.Insert(VirtualAddress(0x2000), PhysicalAddress(0x20000))

	// Lookup hit
	paddr, found := tlb.Lookup(VirtualAddress(0x1000))
	if !found {
		t.Error("TLB lookup should hit")
	}
	if paddr != PhysicalAddress(0x10000) {
		t.Errorf("Wrong physical address from TLB")
	}

	// Lookup miss
	_, found = tlb.Lookup(VirtualAddress(0x3000))
	if found {
		t.Error("TLB lookup should miss")
	}

	// Test invalidation
	tlb.Invalidate(VirtualAddress(0x1000))
	_, found = tlb.Lookup(VirtualAddress(0x1000))
	if found {
		t.Error("TLB should miss after invalidation")
	}

	// Test flush
	tlb.Flush()
	_, found = tlb.Lookup(VirtualAddress(0x2000))
	if found {
		t.Error("TLB should miss after flush")
	}
}

// BenchmarkVASAllocate benchmarks VAS allocation
func BenchmarkVASAllocate(b *testing.B) {
	vas := NewVirtualAddressSpace(
		WithBackend(NewMemoryBackend(1*GB)),
	)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		addr, _ := vas.Allocate(4 * KB)
		vas.Free(addr)
	}
}

// BenchmarkMemoryBackend benchmarks memory backend
func BenchmarkMemoryBackend(b *testing.B) {
	backend := NewMemoryBackend(1 * GB)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		addr, _ := backend.Allocate(4 * KB)
		backend.Free(addr, 4*KB)
	}
}


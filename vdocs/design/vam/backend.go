package vam

import (
	"errors"
	"sync"
)

// Common errors
var (
	ErrOutOfMemory      = errors.New("out of memory/resources")
	ErrInvalidAddress   = errors.New("invalid address")
	ErrAddressInUse     = errors.New("address already in use")
	ErrNotMapped        = errors.New("address not mapped")
	ErrPermissionDenied = errors.New("permission denied")
	ErrRegionNotFound   = errors.New("region not found")
	ErrOvercommitLimit  = errors.New("overcommit limit exceeded")
)

// ResourceBackend defines the interface for physical resource backends
// This abstracts the actual resource storage (memory, disk, network, etc.)
type ResourceBackend interface {
	// Name returns the backend name
	Name() string

	// TotalCapacity returns the total capacity of the backend
	TotalCapacity() uint64

	// AvailableCapacity returns the available capacity
	AvailableCapacity() uint64

	// Allocate allocates physical resources
	// Returns the physical address of the allocated resource
	Allocate(size uint64) (PhysicalAddress, error)

	// Free releases physical resources
	Free(paddr PhysicalAddress, size uint64) error

	// Read reads data from the physical address
	Read(paddr PhysicalAddress, offset, size uint64) ([]byte, error)

	// Write writes data to the physical address
	Write(paddr PhysicalAddress, offset uint64, data []byte) error

	// Stats returns backend statistics
	Stats() ResourceStats
}

// MemoryBackend implements ResourceBackend using memory
// This is the simplest backend for testing and memory-based resources
type MemoryBackend struct {
	name     string
	capacity uint64
	mu       sync.RWMutex

	// Memory storage (simulated)
	storage map[PhysicalAddress][]byte

	// Free list
	freeList     []memoryBlock
	allocCounter uint64

	// Statistics
	usedCapacity    uint64
	allocationCount uint64
	freeCount       uint64
}

type memoryBlock struct {
	addr PhysicalAddress
	size uint64
}

// NewMemoryBackend creates a new memory backend
func NewMemoryBackend(capacity uint64) *MemoryBackend {
	return &MemoryBackend{
		name:     "memory",
		capacity: capacity,
		storage:  make(map[PhysicalAddress][]byte),
		freeList: []memoryBlock{
			{addr: PhysicalAddress(PageSize), size: capacity}, // Start from PageSize to avoid 0 address
		},
	}
}

// Name returns the backend name
func (b *MemoryBackend) Name() string {
	return b.name
}

// TotalCapacity returns the total capacity
func (b *MemoryBackend) TotalCapacity() uint64 {
	return b.capacity
}

// AvailableCapacity returns the available capacity
func (b *MemoryBackend) AvailableCapacity() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.capacity - b.usedCapacity
}

// Allocate allocates physical resources using first-fit algorithm
func (b *MemoryBackend) Allocate(size uint64) (PhysicalAddress, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Align size to page boundary
	size = alignUp(size, PageSize)

	// First-fit allocation
	for i, block := range b.freeList {
		if block.size >= size {
			addr := block.addr

			// Update free list
			if block.size == size {
				b.freeList = append(b.freeList[:i], b.freeList[i+1:]...)
			} else {
				b.freeList[i].addr += PhysicalAddress(size)
				b.freeList[i].size -= size
			}

			// Allocate storage
			b.storage[addr] = make([]byte, size)
			b.usedCapacity += size
			b.allocationCount++

			return addr, nil
		}
	}

	return 0, ErrOutOfMemory
}

// Free releases physical resources
func (b *MemoryBackend) Free(paddr PhysicalAddress, size uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Check if allocated
	data, exists := b.storage[paddr]
	if !exists {
		return ErrInvalidAddress
	}

	size = uint64(len(data))

	// Remove from storage
	delete(b.storage, paddr)
	b.usedCapacity -= size
	b.freeCount++

	// Add back to free list (simple append, could optimize with coalescing)
	b.freeList = append(b.freeList, memoryBlock{
		addr: paddr,
		size: size,
	})

	// Coalesce adjacent free blocks
	b.coalesceFreeList()

	return nil
}

// coalesceFreeList merges adjacent free blocks
func (b *MemoryBackend) coalesceFreeList() {
	if len(b.freeList) < 2 {
		return
	}

	// Sort by address
	for i := 0; i < len(b.freeList)-1; i++ {
		for j := i + 1; j < len(b.freeList); j++ {
			if b.freeList[i].addr > b.freeList[j].addr {
				b.freeList[i], b.freeList[j] = b.freeList[j], b.freeList[i]
			}
		}
	}

	// Coalesce
	merged := []memoryBlock{b.freeList[0]}
	for i := 1; i < len(b.freeList); i++ {
		last := &merged[len(merged)-1]
		curr := b.freeList[i]

		if last.addr+PhysicalAddress(last.size) == curr.addr {
			last.size += curr.size
		} else {
			merged = append(merged, curr)
		}
	}

	b.freeList = merged
}

// Read reads data from the physical address
func (b *MemoryBackend) Read(paddr PhysicalAddress, offset, size uint64) ([]byte, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	data, exists := b.storage[paddr]
	if !exists {
		return nil, ErrInvalidAddress
	}

	if offset+size > uint64(len(data)) {
		return nil, ErrInvalidAddress
	}

	result := make([]byte, size)
	copy(result, data[offset:offset+size])
	return result, nil
}

// Write writes data to the physical address
func (b *MemoryBackend) Write(paddr PhysicalAddress, offset uint64, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	storage, exists := b.storage[paddr]
	if !exists {
		return ErrInvalidAddress
	}

	if offset+uint64(len(data)) > uint64(len(storage)) {
		return ErrInvalidAddress
	}

	copy(storage[offset:], data)
	return nil
}

// Stats returns backend statistics
func (b *MemoryBackend) Stats() ResourceStats {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var fragmentedSpace uint64
	for _, block := range b.freeList {
		if block.size < PageSize {
			fragmentedSpace += block.size
		}
	}

	return ResourceStats{
		TotalCapacity:     b.capacity,
		UsedCapacity:      b.usedCapacity,
		AvailableCapacity: b.capacity - b.usedCapacity,
		FragmentedSpace:   fragmentedSpace,
		AllocationCount:   b.allocationCount,
		FreeCount:         b.freeCount,
	}
}

// SwapBackend implements ResourceBackend for swapped/secondary storage
// This is used for resources that can be swapped out
type SwapBackend struct {
	*MemoryBackend
}

// NewSwapBackend creates a new swap backend
func NewSwapBackend(capacity uint64) *SwapBackend {
	backend := NewMemoryBackend(capacity)
	backend.name = "swap"
	return &SwapBackend{MemoryBackend: backend}
}

// alignUp aligns a value up to the given alignment
func alignUp(val, align uint64) uint64 {
	return (val + align - 1) &^ (align - 1)
}

// alignDown aligns a value down to the given alignment
func alignDown(val, align uint64) uint64 {
	return val &^ (align - 1)
}


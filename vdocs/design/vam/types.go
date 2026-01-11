// Package vam provides Virtual Address Management for abstract resources
// inspired by Linux kernel's virtual memory management.
package vam

import (
	"sync"
	"time"
)

// Common size constants
const (
	KB = 1024
	MB = 1024 * KB
	GB = 1024 * MB
	TB = 1024 * GB

	// PageSize is the default page/unit size
	PageSize = 4 * KB

	// LargePageSize for large resource units
	LargePageSize = 2 * MB
)

// VirtualAddress represents a virtual address in the virtual address space
type VirtualAddress uint64

// PhysicalAddress represents an address in the physical resource backend
type PhysicalAddress uint64

// InvalidAddress represents an invalid address
const InvalidAddress VirtualAddress = 0

// OvercommitPolicy defines the overcommit strategy
// Reference: Linux sysctl_overcommit_memory values
type OvercommitPolicy int

const (
	// OvercommitGuess: Heuristic overcommit (default)
	// Allow overcommit based on available resources
	// Reference: OVERCOMMIT_GUESS (0) in Linux
	OvercommitGuess OvercommitPolicy = iota

	// OvercommitAlways: Always allow overcommit
	// Reference: OVERCOMMIT_ALWAYS (1) in Linux
	OvercommitAlways

	// OvercommitNever: Never allow overcommit
	// Strict accounting, fail if not enough physical resources
	// Reference: OVERCOMMIT_NEVER (2) in Linux
	OvercommitNever
)

func (p OvercommitPolicy) String() string {
	switch p {
	case OvercommitGuess:
		return "OVERCOMMIT_GUESS"
	case OvercommitAlways:
		return "OVERCOMMIT_ALWAYS"
	case OvercommitNever:
		return "OVERCOMMIT_NEVER"
	default:
		return "UNKNOWN"
	}
}

// RegionFlags defines permissions and attributes of a virtual region
// Reference: Linux vm_flags in vm_area_struct
type RegionFlags uint32

const (
	// Permission flags
	RegionRead    RegionFlags = 1 << 0 // VM_READ
	RegionWrite   RegionFlags = 1 << 1 // VM_WRITE
	RegionExec    RegionFlags = 1 << 2 // VM_EXEC
	RegionShared  RegionFlags = 1 << 3 // VM_SHARED
	RegionPrivate RegionFlags = 1 << 4 // VM_PRIVATE (for COW)

	// Special flags
	RegionLocked    RegionFlags = 1 << 8  // VM_LOCKED - don't swap out
	RegionIO        RegionFlags = 1 << 9  // VM_IO - memory mapped I/O
	RegionReserved  RegionFlags = 1 << 10 // VM_RESERVED
	RegionHugePage  RegionFlags = 1 << 11 // VM_HUGEPAGE
	RegionDontCopy  RegionFlags = 1 << 12 // VM_DONTCOPY
	RegionDontDump  RegionFlags = 1 << 13 // VM_DONTDUMP

	// Default flags
	RegionDefault = RegionRead | RegionWrite | RegionPrivate
)

// FaultType defines the type of page/resource fault
type FaultType int

const (
	FaultRead  FaultType = iota // Read access to unmapped region
	FaultWrite                  // Write access to unmapped or readonly region
	FaultExec                   // Execute access
)

// FaultResult defines the result of handling a fault
type FaultResult int

const (
	FaultHandled  FaultResult = iota // Fault was handled successfully
	FaultRetry                       // Should retry the access
	FaultOOM                         // Out of memory/resources
	FaultSIGBUS                      // Bus error (bad access)
	FaultSIGSEGV                     // Segmentation fault
)

// VASStats holds statistics for a virtual address space
// Reference: Linux mm_struct statistics
type VASStats struct {
	// Virtual memory statistics
	TotalVirtual   uint64 // Total virtual address space allocated
	MappedVirtual  uint64 // Virtual addresses that are mapped to physical
	RegionCount    int    // Number of virtual regions (VMAs)

	// Physical resource statistics
	TotalPhysical    uint64 // Total physical resources used
	CommittedVirtual uint64 // Committed (promised) virtual space
	SwappedOut       uint64 // Resources swapped out

	// Fault statistics
	MinorFaults uint64 // Faults handled without I/O
	MajorFaults uint64 // Faults requiring I/O
	OOMCount    uint64 // Out of memory events

	// Performance metrics
	LastFaultTime    time.Time
	AvgFaultLatency  time.Duration
}

// ResourceStats holds statistics for a resource backend
type ResourceStats struct {
	TotalCapacity     uint64
	UsedCapacity      uint64
	AvailableCapacity uint64
	FragmentedSpace   uint64
	AllocationCount   uint64
	FreeCount         uint64
}

// AllocationHint provides hints for allocation
type AllocationHint struct {
	PreferredAddress VirtualAddress // Preferred virtual address
	Alignment        uint64         // Alignment requirement
	Flags            RegionFlags    // Region flags
	NUMANode         int            // Preferred NUMA node (-1 for any)
}

// DefaultAllocationHint returns default allocation hints
func DefaultAllocationHint() AllocationHint {
	return AllocationHint{
		PreferredAddress: InvalidAddress,
		Alignment:        PageSize,
		Flags:            RegionDefault,
		NUMANode:         -1,
	}
}

// PageTableEntry represents an entry in the address mapping table
// Reference: Linux pte_t (page table entry)
type PageTableEntry struct {
	PhysicalAddr PhysicalAddress
	Flags        RegionFlags
	Present      bool   // Is the mapping valid?
	Dirty        bool   // Has the page been modified?
	Accessed     bool   // Has the page been accessed?
	PageSize     uint64 // Size of the mapped page
}

// LRUEntry represents an entry in the LRU list for page replacement
type LRUEntry struct {
	VAddr      VirtualAddress
	PAddr      PhysicalAddress
	AccessTime time.Time
	AccessCount uint64
	Active     bool // Active or inactive list
}

// LRUList implements a Least Recently Used list for resource eviction
// Reference: Linux's active_list and inactive_list
type LRUList struct {
	mu       sync.RWMutex
	active   []*LRUEntry
	inactive []*LRUEntry
	maxSize  int
}

// NewLRUList creates a new LRU list
func NewLRUList(maxSize int) *LRUList {
	return &LRUList{
		active:   make([]*LRUEntry, 0),
		inactive: make([]*LRUEntry, 0),
		maxSize:  maxSize,
	}
}

// Add adds an entry to the active list
func (l *LRUList) Add(entry *LRUEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry.Active = true
	entry.AccessTime = time.Now()
	l.active = append([]*LRUEntry{entry}, l.active...)

	l.balance()
}

// Access marks an entry as accessed
func (l *LRUList) Access(vaddr VirtualAddress) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Check inactive list
	for i, e := range l.inactive {
		if e.VAddr == vaddr {
			// Promote to active
			l.inactive = append(l.inactive[:i], l.inactive[i+1:]...)
			e.Active = true
			e.AccessTime = time.Now()
			e.AccessCount++
			l.active = append([]*LRUEntry{e}, l.active...)
			return
		}
	}

	// Check active list
	for _, e := range l.active {
		if e.VAddr == vaddr {
			e.AccessTime = time.Now()
			e.AccessCount++
			return
		}
	}
}

// Evict returns the least recently used entry for eviction
func (l *LRUList) Evict() *LRUEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.inactive) > 0 {
		// Evict from inactive list (tail)
		idx := len(l.inactive) - 1
		entry := l.inactive[idx]
		l.inactive = l.inactive[:idx]
		return entry
	}

	if len(l.active) > 0 {
		// Demote from active to inactive first
		l.demoteActive()
		return l.Evict()
	}

	return nil
}

// balance ensures the lists are balanced
func (l *LRUList) balance() {
	// Move old entries from active to inactive
	activeTarget := l.maxSize / 2
	for len(l.active) > activeTarget {
		l.demoteActive()
	}
}

// demoteActive moves the oldest entry from active to inactive
func (l *LRUList) demoteActive() {
	if len(l.active) == 0 {
		return
	}

	idx := len(l.active) - 1
	entry := l.active[idx]
	l.active = l.active[:idx]
	entry.Active = false
	l.inactive = append([]*LRUEntry{entry}, l.inactive...)
}

// Remove removes an entry from the LRU lists
func (l *LRUList) Remove(vaddr VirtualAddress) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for i, e := range l.active {
		if e.VAddr == vaddr {
			l.active = append(l.active[:i], l.active[i+1:]...)
			return
		}
	}

	for i, e := range l.inactive {
		if e.VAddr == vaddr {
			l.inactive = append(l.inactive[:i], l.inactive[i+1:]...)
			return
		}
	}
}

// Len returns the total number of entries
func (l *LRUList) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.active) + len(l.inactive)
}


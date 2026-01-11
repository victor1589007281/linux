package vam

import (
	"sync"
	"time"
)

// VirtualAddressSpace represents a complete virtual address space
// This is inspired by Linux's mm_struct
//
// Reference: include/linux/mm_types.h - struct mm_struct
type VirtualAddressSpace struct {
	mu sync.RWMutex

	// Virtual region management
	regions *RegionTree

	// Address mapping (page tables)
	mapping *AddressMapping

	// TLB cache
	tlb *TLB

	// Resource backend
	backend ResourceBackend

	// Swap backend for evicted pages
	swapBackend ResourceBackend

	// Overcommit checker
	overcommit *OvercommitChecker

	// LRU for page replacement
	lru *LRUList

	// Address space limits
	minAddress VirtualAddress
	maxAddress VirtualAddress

	// Statistics
	stats     VASStats
	statsLock sync.RWMutex
}

// VASOption is a functional option for configuring VirtualAddressSpace
type VASOption func(*VirtualAddressSpace)

// WithBackend sets the resource backend
func WithBackend(backend ResourceBackend) VASOption {
	return func(vas *VirtualAddressSpace) {
		vas.backend = backend
	}
}

// WithSwapBackend sets the swap backend
func WithSwapBackend(backend ResourceBackend) VASOption {
	return func(vas *VirtualAddressSpace) {
		vas.swapBackend = backend
	}
}

// WithOvercommitPolicy sets the overcommit policy
func WithOvercommitPolicy(policy OvercommitPolicy) VASOption {
	return func(vas *VirtualAddressSpace) {
		vas.overcommit.SetPolicy(policy)
	}
}

// WithOvercommitRatio sets the overcommit ratio (percentage)
func WithOvercommitRatio(ratio uint64) VASOption {
	return func(vas *VirtualAddressSpace) {
		vas.overcommit.SetOvercommitRatio(ratio)
	}
}

// WithAddressRange sets the valid address range
func WithAddressRange(min, max VirtualAddress) VASOption {
	return func(vas *VirtualAddressSpace) {
		vas.minAddress = min
		vas.maxAddress = max
	}
}

// NewVirtualAddressSpace creates a new virtual address space
func NewVirtualAddressSpace(opts ...VASOption) *VirtualAddressSpace {
	// Create with default backend if not specified
	defaultBackend := NewMemoryBackend(1 * GB)

	vas := &VirtualAddressSpace{
		regions:    NewRegionTree(),
		mapping:    NewAddressMapping(),
		tlb:        NewTLB(1024),
		backend:    defaultBackend,
		overcommit: NewOvercommitChecker(OvercommitGuess, 1*GB, 0),
		lru:        NewLRUList(10000),
		minAddress: VirtualAddress(PageSize),
		maxAddress: VirtualAddress(1 * TB),
	}

	for _, opt := range opts {
		opt(vas)
	}

	// Update overcommit checker with actual backend capacity
	vas.overcommit = NewOvercommitChecker(
		vas.overcommit.GetPolicy(),
		vas.backend.TotalCapacity(),
		0,
	)

	return vas
}

// Allocate allocates virtual address space
// This doesn't allocate physical resources (lazy allocation)
//
// Reference: mm/mmap.c - do_mmap()
func (vas *VirtualAddressSpace) Allocate(size uint64, opts ...AllocOption) (VirtualAddress, error) {
	vas.mu.Lock()
	defer vas.mu.Unlock()

	// Apply options
	hint := DefaultAllocationHint()
	for _, opt := range opts {
		opt(&hint)
	}

	// Align size
	size = alignUp(size, PageSize)
	if size == 0 {
		return InvalidAddress, ErrInvalidAddress
	}

	// Check overcommit limits
	pages := size / PageSize
	if err := vas.overcommit.CheckOvercommit(pages, false); err != nil {
		return InvalidAddress, err
	}

	// Find a gap for the new region
	start := vas.regions.FindGap(size, hint.PreferredAddress)
	if start < vas.minAddress {
		start = vas.minAddress
	}
	end := start + VirtualAddress(size)
	if end > vas.maxAddress {
		vas.overcommit.Uncommit(pages)
		return InvalidAddress, ErrOutOfMemory
	}

	// Create the virtual region
	region := NewVirtualRegion(start, end, hint.Flags, vas.backend)
	region.FaultHandler = NewDefaultFaultHandler(vas.backend, vas.mapping)

	// Insert into region tree
	if err := vas.regions.Insert(region); err != nil {
		vas.overcommit.Uncommit(pages)
		return InvalidAddress, err
	}

	// Update statistics
	vas.updateStats(func(s *VASStats) {
		s.TotalVirtual += size
		s.CommittedVirtual += size
		s.RegionCount++
	})

	return start, nil
}

// AllocOption is a functional option for allocation
type AllocOption func(*AllocationHint)

// WithPreferredAddress sets a preferred virtual address
func WithPreferredAddress(addr VirtualAddress) AllocOption {
	return func(h *AllocationHint) {
		h.PreferredAddress = addr
	}
}

// WithAlignment sets the alignment requirement
func WithAlignment(align uint64) AllocOption {
	return func(h *AllocationHint) {
		h.Alignment = align
	}
}

// WithFlags sets the region flags
func WithFlags(flags RegionFlags) AllocOption {
	return func(h *AllocationHint) {
		h.Flags = flags
	}
}

// Free releases virtual address space
//
// Reference: mm/mmap.c - do_munmap()
func (vas *VirtualAddressSpace) Free(addr VirtualAddress) error {
	vas.mu.Lock()
	defer vas.mu.Unlock()

	// Find the region
	region := vas.regions.Find(addr)
	if region == nil {
		return ErrRegionNotFound
	}

	// Remove all mappings in this region
	start := region.Start
	for vaddr := start; vaddr < region.End; vaddr += VirtualAddress(PageSize) {
		entry, err := vas.mapping.Unmap(vaddr)
		if err == nil {
			// Free the physical resource
			vas.backend.Free(entry.PhysicalAddr, entry.PageSize)
			vas.lru.Remove(vaddr)
			vas.tlb.Invalidate(vaddr)
		}
	}

	// Remove the region
	_, err := vas.regions.Remove(region.Start)
	if err != nil {
		return err
	}

	// Update committed memory
	pages := region.Size() / PageSize
	vas.overcommit.Uncommit(pages)

	// Update statistics
	vas.updateStats(func(s *VASStats) {
		s.TotalVirtual -= region.Size()
		s.CommittedVirtual -= region.Size()
		s.RegionCount--
	})

	return nil
}

// Access accesses data at a virtual address (may trigger fault)
//
// Reference: mm/memory.c - handle_mm_fault()
func (vas *VirtualAddressSpace) Access(addr VirtualAddress) ([]byte, error) {
	return vas.accessInternal(addr, FaultRead)
}

// Write writes data to a virtual address
func (vas *VirtualAddressSpace) Write(addr VirtualAddress, data []byte) error {
	vas.mu.Lock()
	defer vas.mu.Unlock()

	// Find the region
	region := vas.regions.Find(addr)
	if region == nil {
		return ErrRegionNotFound
	}

	// Check write permission
	if !region.CanWrite() {
		return ErrPermissionDenied
	}

	// Check TLB first
	if paddr, found := vas.tlb.Lookup(addr); found {
		offset := uint64(addr) % PageSize
		return vas.backend.Write(paddr, offset, data)
	}

	// Look up in page table
	entry, offset, found := vas.mapping.LookupWithOffset(addr)
	if !found {
		// Handle fault
		result := vas.handleFault(region, addr, FaultWrite)
		if result != FaultHandled {
			return vas.faultResultToError(result)
		}
		// Retry lookup
		entry, offset, found = vas.mapping.LookupWithOffset(addr)
		if !found {
			return ErrNotMapped
		}
	}

	// Mark as dirty and accessed
	vas.mapping.SetDirty(addr)
	vas.mapping.SetAccessed(addr)
	vas.lru.Access(addr)
	vas.tlb.Insert(addr, entry.PhysicalAddr)

	return vas.backend.Write(entry.PhysicalAddr, offset, data)
}

// accessInternal handles access with fault type
func (vas *VirtualAddressSpace) accessInternal(addr VirtualAddress, faultType FaultType) ([]byte, error) {
	vas.mu.Lock()
	defer vas.mu.Unlock()

	// Find the region
	region := vas.regions.Find(addr)
	if region == nil {
		return nil, ErrRegionNotFound
	}

	// Check permission
	switch faultType {
	case FaultRead:
		if !region.CanRead() {
			return nil, ErrPermissionDenied
		}
	case FaultWrite:
		if !region.CanWrite() {
			return nil, ErrPermissionDenied
		}
	case FaultExec:
		if !region.CanExec() {
			return nil, ErrPermissionDenied
		}
	}

	// Check TLB first
	if paddr, found := vas.tlb.Lookup(addr); found {
		offset := uint64(addr) % PageSize
		data, err := vas.backend.Read(paddr, offset, PageSize-offset)
		if err == nil {
			vas.lru.Access(addr)
		}
		return data, err
	}

	// Look up in page table
	entry, offset, found := vas.mapping.LookupWithOffset(addr)
	if !found {
		// Handle fault (demand paging)
		result := vas.handleFault(region, addr, faultType)
		if result != FaultHandled {
			return nil, vas.faultResultToError(result)
		}
		// Retry lookup
		entry, offset, found = vas.mapping.LookupWithOffset(addr)
		if !found {
			return nil, ErrNotMapped
		}
	}

	// Mark as accessed
	vas.mapping.SetAccessed(addr)
	vas.lru.Access(addr)
	vas.tlb.Insert(addr, entry.PhysicalAddr)

	return vas.backend.Read(entry.PhysicalAddr, offset, PageSize-offset)
}

// handleFault handles a page/resource fault
// This is the core of demand paging
//
// Reference: mm/memory.c - __handle_mm_fault()
func (vas *VirtualAddressSpace) handleFault(region *VirtualRegion, addr VirtualAddress, faultType FaultType) FaultResult {
	startTime := time.Now()
	defer func() {
		vas.updateStats(func(s *VASStats) {
			s.LastFaultTime = startTime
		})
	}()

	region.IncrementFaultCount()

	// Try to allocate physical resource
	paddr, err := vas.backend.Allocate(PageSize)
	if err != nil {
		// Try to reclaim resources
		if vas.tryReclaim() {
			paddr, err = vas.backend.Allocate(PageSize)
		}
		if err != nil {
			vas.updateStats(func(s *VASStats) {
				s.OOMCount++
			})
			return FaultOOM
		}
	}

	// Create mapping
	entry := PageTableEntry{
		PhysicalAddr: paddr,
		Flags:        region.Flags,
		Present:      true,
		Dirty:        faultType == FaultWrite,
		Accessed:     true,
		PageSize:     PageSize,
	}

	vas.mapping.Map(addr, entry)

	// Add to LRU
	vas.lru.Add(&LRUEntry{
		VAddr:      addr,
		PAddr:      paddr,
		AccessTime: time.Now(),
		Active:     true,
	})

	// Update statistics
	vas.updateStats(func(s *VASStats) {
		s.MappedVirtual += PageSize
		s.TotalPhysical += PageSize
		s.MinorFaults++
	})

	return FaultHandled
}

// tryReclaim tries to reclaim resources by evicting pages
func (vas *VirtualAddressSpace) tryReclaim() bool {
	entry := vas.lru.Evict()
	if entry == nil {
		return false
	}

	// Unmap the entry
	pte, err := vas.mapping.Unmap(entry.VAddr)
	if err != nil {
		return false
	}

	// If dirty, would need to write back (to swap if available)
	if pte.Dirty && vas.swapBackend != nil {
		// Swap out (simplified)
		data, _ := vas.backend.Read(pte.PhysicalAddr, 0, pte.PageSize)
		swapAddr, err := vas.swapBackend.Allocate(pte.PageSize)
		if err == nil {
			vas.swapBackend.Write(swapAddr, 0, data)
			vas.updateStats(func(s *VASStats) {
				s.SwappedOut += pte.PageSize
			})
		}
	}

	// Free physical resource
	vas.backend.Free(pte.PhysicalAddr, pte.PageSize)
	vas.tlb.Invalidate(entry.VAddr)

	vas.updateStats(func(s *VASStats) {
		s.TotalPhysical -= pte.PageSize
		s.MappedVirtual -= pte.PageSize
	})

	return true
}

// faultResultToError converts fault result to error
func (vas *VirtualAddressSpace) faultResultToError(result FaultResult) error {
	switch result {
	case FaultOOM:
		return ErrOutOfMemory
	case FaultSIGBUS:
		return ErrInvalidAddress
	case FaultSIGSEGV:
		return ErrPermissionDenied
	default:
		return ErrInvalidAddress
	}
}

// updateStats safely updates statistics
func (vas *VirtualAddressSpace) updateStats(fn func(*VASStats)) {
	vas.statsLock.Lock()
	defer vas.statsLock.Unlock()
	fn(&vas.stats)
}

// Stats returns a copy of the current statistics
func (vas *VirtualAddressSpace) Stats() VASStats {
	vas.statsLock.RLock()
	defer vas.statsLock.RUnlock()
	stats := vas.stats
	stats.RegionCount = vas.regions.Count()
	return stats
}

// GetRegion returns the region containing the given address
func (vas *VirtualAddressSpace) GetRegion(addr VirtualAddress) *VirtualRegion {
	vas.mu.RLock()
	defer vas.mu.RUnlock()
	return vas.regions.Find(addr)
}

// GetAllRegions returns all regions
func (vas *VirtualAddressSpace) GetAllRegions() []*VirtualRegion {
	vas.mu.RLock()
	defer vas.mu.RUnlock()
	return vas.regions.All()
}

// GetOvercommitStats returns overcommit statistics
func (vas *VirtualAddressSpace) GetOvercommitStats() OvercommitStats {
	return vas.overcommit.GetStats()
}


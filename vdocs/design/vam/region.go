package vam

import (
	"sync"
)

// VirtualRegion represents a contiguous region in the virtual address space
// This is inspired by Linux's vm_area_struct (VMA)
//
// Reference: include/linux/mm_types.h - struct vm_area_struct
type VirtualRegion struct {
	mu sync.RWMutex

	// Address range
	Start VirtualAddress // vm_start
	End   VirtualAddress // vm_end

	// Flags and permissions
	Flags RegionFlags // vm_flags

	// Page offset (for file-backed mappings)
	PageOffset uint64 // vm_pgoff

	// Backend for this region
	Backend ResourceBackend

	// Fault handler for this region
	FaultHandler FaultHandler

	// Private data
	PrivateData interface{}

	// Statistics
	faultCount uint64
	accessCount uint64
}

// NewVirtualRegion creates a new virtual region
func NewVirtualRegion(start, end VirtualAddress, flags RegionFlags, backend ResourceBackend) *VirtualRegion {
	return &VirtualRegion{
		Start:   start,
		End:     end,
		Flags:   flags,
		Backend: backend,
	}
}

// Size returns the size of the region
func (r *VirtualRegion) Size() uint64 {
	return uint64(r.End - r.Start)
}

// Contains checks if an address is within this region
func (r *VirtualRegion) Contains(addr VirtualAddress) bool {
	return addr >= r.Start && addr < r.End
}

// Overlaps checks if this region overlaps with the given range
func (r *VirtualRegion) Overlaps(start, end VirtualAddress) bool {
	return r.Start < end && r.End > start
}

// CanRead checks if the region is readable
func (r *VirtualRegion) CanRead() bool {
	return r.Flags&RegionRead != 0
}

// CanWrite checks if the region is writable
func (r *VirtualRegion) CanWrite() bool {
	return r.Flags&RegionWrite != 0
}

// CanExec checks if the region is executable
func (r *VirtualRegion) CanExec() bool {
	return r.Flags&RegionExec != 0
}

// IsShared checks if the region is shared
func (r *VirtualRegion) IsShared() bool {
	return r.Flags&RegionShared != 0
}

// IsPrivate checks if the region is private (copy-on-write)
func (r *VirtualRegion) IsPrivate() bool {
	return r.Flags&RegionPrivate != 0
}

// IsLocked checks if the region is locked in memory
func (r *VirtualRegion) IsLocked() bool {
	return r.Flags&RegionLocked != 0
}

// Split splits the region at the given address
// Returns the new region (addr to original end) and modifies original region
func (r *VirtualRegion) Split(addr VirtualAddress) (*VirtualRegion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if addr <= r.Start || addr >= r.End {
		return nil, ErrInvalidAddress
	}

	// Create new region for the second half
	newRegion := &VirtualRegion{
		Start:        addr,
		End:          r.End,
		Flags:        r.Flags,
		PageOffset:   r.PageOffset + uint64(addr-r.Start)/PageSize,
		Backend:      r.Backend,
		FaultHandler: r.FaultHandler,
	}

	// Shrink original region
	r.End = addr

	return newRegion, nil
}

// Merge merges this region with another adjacent region
func (r *VirtualRegion) Merge(other *VirtualRegion) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if regions are adjacent and compatible
	if r.End != other.Start && other.End != r.Start {
		return ErrInvalidAddress
	}

	if r.Flags != other.Flags {
		return ErrPermissionDenied
	}

	// Merge
	if r.End == other.Start {
		r.End = other.End
	} else {
		r.Start = other.Start
	}

	return nil
}

// IncrementFaultCount increments the fault counter
func (r *VirtualRegion) IncrementFaultCount() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.faultCount++
}

// IncrementAccessCount increments the access counter
func (r *VirtualRegion) IncrementAccessCount() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accessCount++
}

// GetFaultCount returns the fault count
func (r *VirtualRegion) GetFaultCount() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.faultCount
}

// GetAccessCount returns the access count
func (r *VirtualRegion) GetAccessCount() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.accessCount
}

// FaultHandler defines the interface for handling page/resource faults
// Reference: Linux vm_operations_struct.fault
type FaultHandler interface {
	// HandleFault handles a fault at the given address
	HandleFault(region *VirtualRegion, addr VirtualAddress, faultType FaultType) FaultResult
}

// DefaultFaultHandler implements the default fault handling logic
type DefaultFaultHandler struct {
	backend ResourceBackend
	mapping *AddressMapping
}

// NewDefaultFaultHandler creates a new default fault handler
func NewDefaultFaultHandler(backend ResourceBackend, mapping *AddressMapping) *DefaultFaultHandler {
	return &DefaultFaultHandler{
		backend: backend,
		mapping: mapping,
	}
}

// HandleFault handles a fault by allocating physical resources
func (h *DefaultFaultHandler) HandleFault(region *VirtualRegion, addr VirtualAddress, faultType FaultType) FaultResult {
	// Check permissions
	switch faultType {
	case FaultRead:
		if !region.CanRead() {
			return FaultSIGSEGV
		}
	case FaultWrite:
		if !region.CanWrite() {
			return FaultSIGSEGV
		}
	case FaultExec:
		if !region.CanExec() {
			return FaultSIGSEGV
		}
	}

	// Allocate physical resource
	paddr, err := h.backend.Allocate(PageSize)
	if err != nil {
		return FaultOOM
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

	h.mapping.Map(addr, entry)

	return FaultHandled
}

// RegionTree manages virtual regions using an interval tree
// Reference: Linux's maple tree (mt) for VMA management
type RegionTree struct {
	mu      sync.RWMutex
	regions []*VirtualRegion
}

// NewRegionTree creates a new region tree
func NewRegionTree() *RegionTree {
	return &RegionTree{
		regions: make([]*VirtualRegion, 0),
	}
}

// Insert inserts a new region
func (t *RegionTree) Insert(region *VirtualRegion) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check for overlaps
	for _, r := range t.regions {
		if r.Overlaps(region.Start, region.End) {
			return ErrAddressInUse
		}
	}

	// Insert in sorted order
	idx := 0
	for i, r := range t.regions {
		if r.Start > region.Start {
			idx = i
			break
		}
		idx = i + 1
	}

	// Insert at index
	t.regions = append(t.regions[:idx], append([]*VirtualRegion{region}, t.regions[idx:]...)...)
	return nil
}

// Remove removes a region
func (t *RegionTree) Remove(start VirtualAddress) (*VirtualRegion, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for i, r := range t.regions {
		if r.Start == start {
			t.regions = append(t.regions[:i], t.regions[i+1:]...)
			return r, nil
		}
	}

	return nil, ErrRegionNotFound
}

// Find finds the region containing the given address
func (t *RegionTree) Find(addr VirtualAddress) *VirtualRegion {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Binary search could be used here for larger trees
	for _, r := range t.regions {
		if r.Contains(addr) {
			return r
		}
	}

	return nil
}

// FindOverlapping finds all regions overlapping with the given range
func (t *RegionTree) FindOverlapping(start, end VirtualAddress) []*VirtualRegion {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var result []*VirtualRegion
	for _, r := range t.regions {
		if r.Overlaps(start, end) {
			result = append(result, r)
		}
	}

	return result
}

// FindGap finds a gap of the given size starting from the hint address
func (t *RegionTree) FindGap(size uint64, hint VirtualAddress) VirtualAddress {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Start from hint or minimum address
	current := hint
	if current < VirtualAddress(PageSize) {
		current = VirtualAddress(PageSize) // Avoid 0 address
	}

	// Align to page boundary
	current = VirtualAddress(alignUp(uint64(current), PageSize))

	for _, r := range t.regions {
		if r.Start >= current+VirtualAddress(size) {
			// Found a gap before this region
			return current
		}
		// Skip past this region
		if r.End > current {
			current = VirtualAddress(alignUp(uint64(r.End), PageSize))
		}
	}

	// Gap at the end
	return current
}

// All returns all regions
func (t *RegionTree) All() []*VirtualRegion {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make([]*VirtualRegion, len(t.regions))
	copy(result, t.regions)
	return result
}

// Count returns the number of regions
func (t *RegionTree) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.regions)
}


package vam

import (
	"sync"
)

// AddressMapping manages the mapping between virtual and physical addresses
// This is inspired by Linux's page table structure
//
// Reference: Linux's multi-level page tables (pgd, p4d, pud, pmd, pte)
type AddressMapping struct {
	mu sync.RWMutex

	// Simple flat mapping for now
	// In a real implementation, this would be a multi-level page table
	entries map[VirtualAddress]PageTableEntry

	// Statistics
	mappingCount uint64
	unmapCount   uint64
}

// NewAddressMapping creates a new address mapping
func NewAddressMapping() *AddressMapping {
	return &AddressMapping{
		entries: make(map[VirtualAddress]PageTableEntry),
	}
}

// Map creates a mapping from virtual to physical address
func (m *AddressMapping) Map(vaddr VirtualAddress, entry PageTableEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Align address to page boundary
	vaddr = VirtualAddress(alignDown(uint64(vaddr), PageSize))

	if existing, exists := m.entries[vaddr]; exists && existing.Present {
		return ErrAddressInUse
	}

	entry.Present = true
	m.entries[vaddr] = entry
	m.mappingCount++

	return nil
}

// Unmap removes a mapping
func (m *AddressMapping) Unmap(vaddr VirtualAddress) (PageTableEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	vaddr = VirtualAddress(alignDown(uint64(vaddr), PageSize))

	entry, exists := m.entries[vaddr]
	if !exists || !entry.Present {
		return PageTableEntry{}, ErrNotMapped
	}

	delete(m.entries, vaddr)
	m.unmapCount++

	return entry, nil
}

// Lookup looks up the physical address for a virtual address
func (m *AddressMapping) Lookup(vaddr VirtualAddress) (PageTableEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pageAddr := VirtualAddress(alignDown(uint64(vaddr), PageSize))

	entry, exists := m.entries[pageAddr]
	if !exists || !entry.Present {
		return PageTableEntry{}, false
	}

	return entry, true
}

// LookupWithOffset looks up the mapping and returns the offset within the page
func (m *AddressMapping) LookupWithOffset(vaddr VirtualAddress) (PageTableEntry, uint64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	pageAddr := VirtualAddress(alignDown(uint64(vaddr), PageSize))
	offset := uint64(vaddr) - uint64(pageAddr)

	entry, exists := m.entries[pageAddr]
	if !exists || !entry.Present {
		return PageTableEntry{}, 0, false
	}

	return entry, offset, true
}

// SetDirty marks a page as dirty
func (m *AddressMapping) SetDirty(vaddr VirtualAddress) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pageAddr := VirtualAddress(alignDown(uint64(vaddr), PageSize))

	if entry, exists := m.entries[pageAddr]; exists {
		entry.Dirty = true
		m.entries[pageAddr] = entry
	}
}

// SetAccessed marks a page as accessed
func (m *AddressMapping) SetAccessed(vaddr VirtualAddress) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pageAddr := VirtualAddress(alignDown(uint64(vaddr), PageSize))

	if entry, exists := m.entries[pageAddr]; exists {
		entry.Accessed = true
		m.entries[pageAddr] = entry
	}
}

// ClearDirty clears the dirty flag (after writeback)
func (m *AddressMapping) ClearDirty(vaddr VirtualAddress) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pageAddr := VirtualAddress(alignDown(uint64(vaddr), PageSize))

	if entry, exists := m.entries[pageAddr]; exists {
		entry.Dirty = false
		m.entries[pageAddr] = entry
	}
}

// ClearAccessed clears the accessed flag (for aging)
func (m *AddressMapping) ClearAccessed(vaddr VirtualAddress) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pageAddr := VirtualAddress(alignDown(uint64(vaddr), PageSize))

	if entry, exists := m.entries[pageAddr]; exists {
		entry.Accessed = false
		m.entries[pageAddr] = entry
	}
}

// GetAllMappings returns all current mappings
func (m *AddressMapping) GetAllMappings() map[VirtualAddress]PageTableEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[VirtualAddress]PageTableEntry, len(m.entries))
	for k, v := range m.entries {
		result[k] = v
	}
	return result
}

// GetDirtyPages returns all dirty pages
func (m *AddressMapping) GetDirtyPages() []VirtualAddress {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var dirty []VirtualAddress
	for vaddr, entry := range m.entries {
		if entry.Dirty {
			dirty = append(dirty, vaddr)
		}
	}
	return dirty
}

// GetMappingCount returns the number of active mappings
func (m *AddressMapping) GetMappingCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}

// Stats returns mapping statistics
func (m *AddressMapping) Stats() (mappings, unmaps uint64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mappingCount, m.unmapCount
}

// Walk iterates over all mappings and calls the function for each
func (m *AddressMapping) Walk(fn func(vaddr VirtualAddress, entry PageTableEntry) bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for vaddr, entry := range m.entries {
		if !fn(vaddr, entry) {
			return
		}
	}
}

// PageTable represents a level in the page table hierarchy
// This is a simplified representation inspired by Linux's page table levels
type PageTable struct {
	level   int           // 0 = PGD, 1 = P4D, 2 = PUD, 3 = PMD, 4 = PTE
	entries [512]*PageTableEntry
	next    [512]*PageTable
}

// NewPageTable creates a new page table at the given level
func NewPageTable(level int) *PageTable {
	return &PageTable{
		level: level,
	}
}

// TranslationLookasideBuffer (TLB) cache for fast address translation
// Reference: Linux's TLB management
type TLB struct {
	mu      sync.RWMutex
	cache   map[VirtualAddress]PhysicalAddress
	maxSize int
	hits    uint64
	misses  uint64
}

// NewTLB creates a new TLB cache
func NewTLB(maxSize int) *TLB {
	return &TLB{
		cache:   make(map[VirtualAddress]PhysicalAddress),
		maxSize: maxSize,
	}
}

// Lookup looks up an address in the TLB
func (t *TLB) Lookup(vaddr VirtualAddress) (PhysicalAddress, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	pageAddr := VirtualAddress(alignDown(uint64(vaddr), PageSize))
	paddr, found := t.cache[pageAddr]
	if found {
		t.hits++
	} else {
		t.misses++
	}
	return paddr, found
}

// Insert inserts an entry into the TLB
func (t *TLB) Insert(vaddr VirtualAddress, paddr PhysicalAddress) {
	t.mu.Lock()
	defer t.mu.Unlock()

	pageAddr := VirtualAddress(alignDown(uint64(vaddr), PageSize))

	if len(t.cache) >= t.maxSize {
		// Evict a random entry
		for k := range t.cache {
			delete(t.cache, k)
			break
		}
	}

	t.cache[pageAddr] = paddr
}

// Invalidate removes an entry from the TLB
func (t *TLB) Invalidate(vaddr VirtualAddress) {
	t.mu.Lock()
	defer t.mu.Unlock()

	pageAddr := VirtualAddress(alignDown(uint64(vaddr), PageSize))
	delete(t.cache, pageAddr)
}

// Flush clears all entries from the TLB
func (t *TLB) Flush() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.cache = make(map[VirtualAddress]PhysicalAddress)
}

// Stats returns TLB statistics
func (t *TLB) Stats() (hits, misses uint64) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.hits, t.misses
}


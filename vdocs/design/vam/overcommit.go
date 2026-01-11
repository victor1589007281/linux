package vam

import (
	"sync/atomic"
)

// CommittedTracker tracks committed virtual address space
// This is inspired by Linux's vm_committed_as percpu counter
//
// Reference: mm/util.c - struct percpu_counter vm_committed_as
type CommittedTracker struct {
	committed uint64 // Total committed virtual space
	limit     uint64 // Commit limit
}

// NewCommittedTracker creates a new committed tracker
func NewCommittedTracker(limit uint64) *CommittedTracker {
	return &CommittedTracker{
		committed: 0,
		limit:     limit,
	}
}

// Commit adds to the committed count
func (t *CommittedTracker) Commit(pages uint64) {
	atomic.AddUint64(&t.committed, pages)
}

// Uncommit removes from the committed count
func (t *CommittedTracker) Uncommit(pages uint64) {
	atomic.AddUint64(&t.committed, ^(pages - 1)) // Subtract
}

// Get returns the current committed count
func (t *CommittedTracker) Get() uint64 {
	return atomic.LoadUint64(&t.committed)
}

// GetLimit returns the commit limit
func (t *CommittedTracker) GetLimit() uint64 {
	return t.limit
}

// SetLimit sets the commit limit
func (t *CommittedTracker) SetLimit(limit uint64) {
	t.limit = limit
}

// OvercommitChecker checks if an allocation should be allowed
// This is inspired by Linux's __vm_enough_memory() function
//
// Reference: mm/util.c - int __vm_enough_memory()
type OvercommitChecker struct {
	policy           OvercommitPolicy
	tracker          *CommittedTracker
	physicalTotal    uint64 // Total physical resources
	swapTotal        uint64 // Total swap/secondary storage
	overcommitRatio  uint64 // Percentage (e.g., 150 for 150%)
	overcommitKBytes uint64 // Fixed overcommit limit in KB
	adminReserve     uint64 // Reserved for admin
	userReserve      uint64 // Reserved per user
}

// NewOvercommitChecker creates a new overcommit checker
func NewOvercommitChecker(
	policy OvercommitPolicy,
	physicalTotal uint64,
	swapTotal uint64,
) *OvercommitChecker {
	limit := calculateCommitLimit(physicalTotal, swapTotal, 100, 0)
	return &OvercommitChecker{
		policy:          policy,
		tracker:         NewCommittedTracker(limit),
		physicalTotal:   physicalTotal,
		swapTotal:       swapTotal,
		overcommitRatio: 100,
		adminReserve:    8 * KB, // 8KB reserved for admin
		userReserve:     128 * KB, // 128KB reserved per user
	}
}

// calculateCommitLimit calculates the commit limit
// Reference: mm/util.c - unsigned long vm_commit_limit()
func calculateCommitLimit(physical, swap, ratio, kbytes uint64) uint64 {
	if kbytes > 0 {
		return kbytes
	}
	return physical*ratio/100 + swap
}

// SetOvercommitRatio sets the overcommit ratio
func (c *OvercommitChecker) SetOvercommitRatio(ratio uint64) {
	c.overcommitRatio = ratio
	c.updateLimit()
}

// SetOvercommitKBytes sets a fixed overcommit limit
func (c *OvercommitChecker) SetOvercommitKBytes(kbytes uint64) {
	c.overcommitKBytes = kbytes
	c.updateLimit()
}

// SetPolicy sets the overcommit policy
func (c *OvercommitChecker) SetPolicy(policy OvercommitPolicy) {
	c.policy = policy
}

// updateLimit updates the commit limit based on current settings
func (c *OvercommitChecker) updateLimit() {
	limit := calculateCommitLimit(c.physicalTotal, c.swapTotal, c.overcommitRatio, c.overcommitKBytes)
	c.tracker.SetLimit(limit)
}

// CheckOvercommit checks if the allocation should be allowed
// Returns nil if allowed, error otherwise
//
// Reference: mm/util.c - int __vm_enough_memory()
func (c *OvercommitChecker) CheckOvercommit(pages uint64, isAdmin bool) error {
	// Always allow if policy is OVERCOMMIT_ALWAYS
	if c.policy == OvercommitAlways {
		c.tracker.Commit(pages)
		return nil
	}

	// Heuristic check for OVERCOMMIT_GUESS
	if c.policy == OvercommitGuess {
		// Simple heuristic: allow if total pages requested is less than
		// physical + swap
		total := c.physicalTotal + c.swapTotal
		if pages > total {
			return ErrOutOfMemory
		}
		c.tracker.Commit(pages)
		return nil
	}

	// Strict check for OVERCOMMIT_NEVER
	allowed := c.tracker.GetLimit()

	// Reserve some for admin
	if !isAdmin {
		allowed -= c.adminReserve
	}

	// Check if within limit
	currentCommitted := c.tracker.Get()
	if currentCommitted+pages > allowed {
		return ErrOvercommitLimit
	}

	c.tracker.Commit(pages)
	return nil
}

// Uncommit releases committed pages
func (c *OvercommitChecker) Uncommit(pages uint64) {
	c.tracker.Uncommit(pages)
}

// GetCommitted returns the current committed amount
func (c *OvercommitChecker) GetCommitted() uint64 {
	return c.tracker.Get()
}

// GetLimit returns the current commit limit
func (c *OvercommitChecker) GetLimit() uint64 {
	return c.tracker.GetLimit()
}

// GetPolicy returns the current policy
func (c *OvercommitChecker) GetPolicy() OvercommitPolicy {
	return c.policy
}

// Stats returns overcommit statistics
type OvercommitStats struct {
	Policy          OvercommitPolicy
	Committed       uint64
	Limit           uint64
	PhysicalTotal   uint64
	SwapTotal       uint64
	OvercommitRatio uint64
	UsagePercent    float64
}

// GetStats returns current overcommit statistics
func (c *OvercommitChecker) GetStats() OvercommitStats {
	committed := c.tracker.Get()
	limit := c.tracker.GetLimit()
	var usage float64
	if limit > 0 {
		usage = float64(committed) / float64(limit) * 100
	}

	return OvercommitStats{
		Policy:          c.policy,
		Committed:       committed,
		Limit:           limit,
		PhysicalTotal:   c.physicalTotal,
		SwapTotal:       c.swapTotal,
		OvercommitRatio: c.overcommitRatio,
		UsagePercent:    usage,
	}
}


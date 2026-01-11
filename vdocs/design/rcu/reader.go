package rcu

import (
	"sync/atomic"
)

// ReaderState tracks the state of an RCU reader
// Inspired by Linux's per-task RCU state
//
// Reference: task_struct.rcu_read_lock_nesting, rcu_read_unlock_special
type ReaderState struct {
	// Nesting count for read locks
	// Positive value means we're in a critical section
	nesting int32

	// The GP sequence when this reader last saw a quiescent state
	gpSeqSeen uint64

	// Special handling flags
	special uint32
}

// ReaderState special flags
const (
	RCU_READ_UNLOCK_BLOCKED  uint32 = 1 << 0 // Was blocked in critical section
	RCU_READ_UNLOCK_NEED_QS  uint32 = 1 << 1 // Need to report quiescent state
	RCU_READ_UNLOCK_EXP_HINT uint32 = 1 << 2 // Expedited GP hint
)

// NewReaderState creates a new reader state
func NewReaderState() *ReaderState {
	return &ReaderState{
		nesting:   0,
		gpSeqSeen: 0,
		special:   0,
	}
}

// IsInCriticalSection returns true if the reader is in a critical section
func (rs *ReaderState) IsInCriticalSection() bool {
	return atomic.LoadInt32(&rs.nesting) > 0
}

// Depth returns the nesting depth
func (rs *ReaderState) Depth() int {
	return int(atomic.LoadInt32(&rs.nesting))
}

// SetNeedQS sets the need quiescent state flag
func (rs *ReaderState) SetNeedQS() {
	atomic.OrInt32((*int32)(&rs.special), int32(RCU_READ_UNLOCK_NEED_QS))
}

// ClearNeedQS clears the need quiescent state flag
func (rs *ReaderState) ClearNeedQS() {
	atomic.AndInt32((*int32)(&rs.special), ^int32(RCU_READ_UNLOCK_NEED_QS))
}

// NeedQS returns true if quiescent state reporting is needed
func (rs *ReaderState) NeedQS() bool {
	return atomic.LoadUint32(&rs.special)&RCU_READ_UNLOCK_NEED_QS != 0
}

// SetBlocked marks that the reader was blocked in critical section
func (rs *ReaderState) SetBlocked() {
	atomic.OrInt32((*int32)(&rs.special), int32(RCU_READ_UNLOCK_BLOCKED))
}

// WasBlocked returns true if the reader was blocked
func (rs *ReaderState) WasBlocked() bool {
	return atomic.LoadUint32(&rs.special)&RCU_READ_UNLOCK_BLOCKED != 0
}

// ReaderTracker manages all reader states
type ReaderTracker struct {
	// Active readers indexed by goroutine ID
	readers map[uint64]*ReaderState
}

// NewReaderTracker creates a new reader tracker
func NewReaderTracker() *ReaderTracker {
	return &ReaderTracker{
		readers: make(map[uint64]*ReaderState),
	}
}

// ReadSection provides a convenient way to execute code in RCU critical section
type ReadSection struct {
	rcu *RCU
}

// NewReadSection creates a new read section helper
func NewReadSection(rcu *RCU) *ReadSection {
	rcu.ReadLock()
	return &ReadSection{rcu: rcu}
}

// Close exits the read section
func (rs *ReadSection) Close() {
	rs.rcu.ReadUnlock()
}

// Do executes a function within an RCU read-side critical section
// This is a convenience wrapper that ensures proper lock/unlock
func (r *RCU) Do(fn func()) {
	r.ReadLock()
	defer r.ReadUnlock()
	fn()
}

// DoWithValue executes a function and returns its result within an RCU critical section
func DoWithValue[T any](r *RCU, fn func() T) T {
	r.ReadLock()
	defer r.ReadUnlock()
	return fn()
}


package rcu

import (
	"sync"
	"sync/atomic"
	"time"
)

// GracePeriod manages grace period detection and advancement
// Inspired by Linux's grace period handling in kernel/rcu/tree.c
type GracePeriod struct {
	// Sequence number for grace periods
	// Low bits indicate state, rest is the sequence
	seq uint64

	// Grace period state
	state int32

	// Readers that were present at GP start
	readers sync.Map

	// Signal for GP completion
	complete chan struct{}

	// Configuration
	timeout time.Duration
}

// GPState represents grace period state
type GPState int32

const (
	GPIdle     GPState = iota // No GP in progress
	GPStarting               // GP is starting
	GPActive                 // GP is in progress
	GPEnding                 // GP is completing
)

// NewGracePeriod creates a new grace period manager
func NewGracePeriod() *GracePeriod {
	return &GracePeriod{
		seq:      0,
		state:    int32(GPIdle),
		complete: make(chan struct{}),
		timeout:  time.Second,
	}
}

// GetSeq returns the current grace period sequence
func (gp *GracePeriod) GetSeq() uint64 {
	return atomic.LoadUint64(&gp.seq)
}

// GetState returns the current state
func (gp *GracePeriod) GetState() GPState {
	return GPState(atomic.LoadInt32(&gp.state))
}

// Start begins a new grace period
func (gp *GracePeriod) Start() bool {
	// Transition from Idle to Starting
	if !atomic.CompareAndSwapInt32(&gp.state, int32(GPIdle), int32(GPStarting)) {
		return false
	}

	// Record the starting sequence
	atomic.AddUint64(&gp.seq, 1<<RCU_SEQ_CTR_SHIFT)

	// Transition to Active
	atomic.StoreInt32(&gp.state, int32(GPActive))

	return true
}

// End completes the current grace period
func (gp *GracePeriod) End() bool {
	// Transition from Active to Ending
	if !atomic.CompareAndSwapInt32(&gp.state, int32(GPActive), int32(GPEnding)) {
		return false
	}

	// Advance sequence to indicate completion
	atomic.AddUint64(&gp.seq, 1<<RCU_SEQ_CTR_SHIFT)

	// Signal completion
	select {
	case gp.complete <- struct{}{}:
	default:
	}

	// Transition back to Idle
	atomic.StoreInt32(&gp.state, int32(GPIdle))

	return true
}

// Wait waits for the current grace period to complete
func (gp *GracePeriod) Wait() {
	if gp.GetState() == GPIdle {
		return
	}

	select {
	case <-gp.complete:
	case <-time.After(gp.timeout):
	}
}

// QuiescentState records a quiescent state for a CPU/goroutine
// Inspired by Linux's rcu_report_qs_* functions
type QuiescentState struct {
	// ID of the reporter (goroutine ID)
	id uint64

	// GP sequence when this QS was reported
	gpSeq uint64

	// Timestamp
	timestamp time.Time
}

// GPDetector detects when all readers have passed through a quiescent state
type GPDetector struct {
	mu sync.Mutex

	// Current GP sequence
	gpSeq uint64

	// Quiescent states from each goroutine
	qsStates map[uint64]*QuiescentState

	// Expected reporters (registered readers)
	expected map[uint64]bool
}

// NewGPDetector creates a new GP detector
func NewGPDetector() *GPDetector {
	return &GPDetector{
		qsStates: make(map[uint64]*QuiescentState),
		expected: make(map[uint64]bool),
	}
}

// RegisterReader registers a reader that must report QS
func (d *GPDetector) RegisterReader(id uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.expected[id] = true
}

// UnregisterReader removes a reader
func (d *GPDetector) UnregisterReader(id uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.expected, id)
	delete(d.qsStates, id)
}

// ReportQS reports a quiescent state
func (d *GPDetector) ReportQS(id uint64, gpSeq uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.qsStates[id] = &QuiescentState{
		id:        id,
		gpSeq:     gpSeq,
		timestamp: time.Now(),
	}
}

// AllQuiescent checks if all expected readers have reported QS
// for the given GP sequence
func (d *GPDetector) AllQuiescent(gpSeq uint64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	for id := range d.expected {
		qs, ok := d.qsStates[id]
		if !ok {
			return false
		}
		if qs.gpSeq < gpSeq {
			return false
		}
	}

	return true
}

// GetMissingQS returns IDs of readers that haven't reported QS
func (d *GPDetector) GetMissingQS(gpSeq uint64) []uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()

	var missing []uint64
	for id := range d.expected {
		qs, ok := d.qsStates[id]
		if !ok || qs.gpSeq < gpSeq {
			missing = append(missing, id)
		}
	}

	return missing
}

// GPSequence manages grace period sequence numbers
// Reference: kernel/rcu/tree.c - rcu_state.gp_seq
type GPSequence struct {
	seq uint64
}

// NewGPSequence creates a new GP sequence
func NewGPSequence() *GPSequence {
	return &GPSequence{seq: 0}
}

// Get returns the current sequence
func (s *GPSequence) Get() uint64 {
	return atomic.LoadUint64(&s.seq)
}

// Advance advances the sequence
func (s *GPSequence) Advance() uint64 {
	return atomic.AddUint64(&s.seq, 1<<RCU_SEQ_CTR_SHIFT)
}

// State returns the state portion (low bits)
func (s *GPSequence) State() uint64 {
	return atomic.LoadUint64(&s.seq) & RCU_SEQ_STATE_MASK
}

// Counter returns the counter portion (high bits)
func (s *GPSequence) Counter() uint64 {
	return atomic.LoadUint64(&s.seq) >> RCU_SEQ_CTR_SHIFT
}

// Started checks if a GP has started since the given old sequence
func (s *GPSequence) Started(oldSeq uint64) bool {
	return (s.Get() ^ oldSeq) & ^uint64(RCU_SEQ_STATE_MASK) != 0
}

// Completed checks if a GP has completed since the given old sequence
func (s *GPSequence) Completed(oldSeq uint64) bool {
	current := s.Get()
	// A GP is complete if the counter has advanced by at least 2
	return (current - oldSeq) >= (2 << RCU_SEQ_CTR_SHIFT)
}


package rcu

import (
	"sync/atomic"
	"unsafe"
)

// RCU protected pointer operations
// These operations provide the memory ordering guarantees required by RCU

// RCUPointer wraps a pointer for RCU protection
type RCUPointer[T any] struct {
	ptr unsafe.Pointer
}

// NewRCUPointer creates a new RCU-protected pointer
func NewRCUPointer[T any](val *T) *RCUPointer[T] {
	return &RCUPointer[T]{
		ptr: unsafe.Pointer(val),
	}
}

// Load atomically loads the pointer value
// Must be called within rcu_read_lock/rcu_read_unlock
// This is equivalent to rcu_dereference()
func (p *RCUPointer[T]) Load() *T {
	return (*T)(atomic.LoadPointer(&p.ptr))
}

// Store atomically stores a new pointer value
// The new value should be fully initialized before calling this
// This is equivalent to rcu_assign_pointer()
func (p *RCUPointer[T]) Store(val *T) {
	atomic.StorePointer(&p.ptr, unsafe.Pointer(val))
}

// Swap atomically swaps the pointer and returns the old value
func (p *RCUPointer[T]) Swap(val *T) *T {
	return (*T)(atomic.SwapPointer(&p.ptr, unsafe.Pointer(val)))
}

// CompareAndSwap performs an atomic compare-and-swap
func (p *RCUPointer[T]) CompareAndSwap(old, new *T) bool {
	return atomic.CompareAndSwapPointer(&p.ptr, unsafe.Pointer(old), unsafe.Pointer(new))
}

// IsNil returns true if the pointer is nil
func (p *RCUPointer[T]) IsNil() bool {
	return atomic.LoadPointer(&p.ptr) == nil
}

// RCUProtected wraps a value with RCU protection
type RCUProtected[T any] struct {
	ptr RCUPointer[T]
	rcu *RCU
}

// NewRCUProtected creates a new RCU-protected value
func NewRCUProtected[T any](rcu *RCU, initial *T) *RCUProtected[T] {
	return &RCUProtected[T]{
		ptr: RCUPointer[T]{ptr: unsafe.Pointer(initial)},
		rcu: rcu,
	}
}

// Read reads the current value within an RCU critical section
func (p *RCUProtected[T]) Read() *T {
	p.rcu.ReadLock()
	val := p.ptr.Load()
	p.rcu.ReadUnlock()
	return val
}

// ReadWith reads the value and calls a function within the critical section
func (p *RCUProtected[T]) ReadWith(fn func(*T)) {
	p.rcu.ReadLock()
	defer p.rcu.ReadUnlock()
	fn(p.ptr.Load())
}

// Update updates the value using copy-update semantics
// The update function receives the current value and returns the new value
func (p *RCUProtected[T]) Update(fn func(*T) *T) {
	// Read current value
	p.rcu.ReadLock()
	old := p.ptr.Load()
	p.rcu.ReadUnlock()

	// Create updated copy
	newVal := fn(old)

	// Atomically replace
	p.ptr.Store(newVal)

	// Wait for grace period before freeing old
	// (caller is responsible for this if needed)
}

// UpdateAndWait updates the value and waits for a grace period
func (p *RCUProtected[T]) UpdateAndWait(fn func(*T) *T) *T {
	// Read current value
	p.rcu.ReadLock()
	old := p.ptr.Load()
	p.rcu.ReadUnlock()

	// Create updated copy
	newVal := fn(old)

	// Atomically replace
	p.ptr.Store(newVal)

	// Wait for grace period
	p.rcu.Synchronize()

	// Return old value (can now be safely freed)
	return old
}

// RCUList is an RCU-protected linked list
type RCUList[T any] struct {
	head RCUPointer[RCUListNode[T]]
	rcu  *RCU
}

// RCUListNode is a node in the RCU list
type RCUListNode[T any] struct {
	value T
	next  RCUPointer[RCUListNode[T]]
}

// NewRCUList creates a new RCU-protected list
func NewRCUList[T any](rcu *RCU) *RCUList[T] {
	return &RCUList[T]{
		rcu: rcu,
	}
}

// Prepend adds an element to the front of the list
func (l *RCUList[T]) Prepend(value T) {
	node := &RCUListNode[T]{value: value}
	
	for {
		oldHead := l.head.Load()
		node.next.Store(oldHead)
		if l.head.CompareAndSwap(oldHead, node) {
			return
		}
	}
}

// ForEach iterates over the list within an RCU critical section
func (l *RCUList[T]) ForEach(fn func(T) bool) {
	l.rcu.ReadLock()
	defer l.rcu.ReadUnlock()

	for node := l.head.Load(); node != nil; node = node.next.Load() {
		if !fn(node.value) {
			return
		}
	}
}

// Find finds an element in the list
func (l *RCUList[T]) Find(predicate func(T) bool) (*T, bool) {
	l.rcu.ReadLock()
	defer l.rcu.ReadUnlock()

	for node := l.head.Load(); node != nil; node = node.next.Load() {
		if predicate(node.value) {
			return &node.value, true
		}
	}

	return nil, false
}

// Remove removes an element from the list
// This is a simplified implementation; a real one would be more complex
func (l *RCUList[T]) Remove(predicate func(T) bool) bool {
	// For simplicity, rebuild the list without the removed element
	var newHead *RCUListNode[T]
	var tail *RCUListNode[T]
	removed := false

	l.rcu.ReadLock()
	for node := l.head.Load(); node != nil; node = node.next.Load() {
		if !removed && predicate(node.value) {
			removed = true
			continue
		}
		
		newNode := &RCUListNode[T]{value: node.value}
		if tail == nil {
			newHead = newNode
		} else {
			tail.next.Store(newNode)
		}
		tail = newNode
	}
	l.rcu.ReadUnlock()

	if removed {
		l.head.Store(newHead)
		l.rcu.Synchronize()
	}

	return removed
}

// Len returns the length of the list
func (l *RCUList[T]) Len() int {
	count := 0
	l.rcu.ReadLock()
	defer l.rcu.ReadUnlock()

	for node := l.head.Load(); node != nil; node = node.next.Load() {
		count++
	}

	return count
}


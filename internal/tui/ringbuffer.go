package tui

import "sync"

// RingBuffer is a thread-safe circular buffer for storing log lines.
// When capacity is reached, oldest entries are overwritten.
type RingBuffer struct {
	lines    []string
	capacity int
	head     int // next write position
	count    int
	mu       sync.RWMutex
}

// NewRingBuffer creates a new ring buffer with the given capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	return &RingBuffer{
		lines:    make([]string, capacity),
		capacity: capacity,
	}
}

// Append adds a line to the buffer, overwriting the oldest if full.
func (rb *RingBuffer) Append(line string) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.lines[rb.head] = line
	rb.head = (rb.head + 1) % rb.capacity
	if rb.count < rb.capacity {
		rb.count++
	}
}

// Len returns the number of lines in the buffer.
func (rb *RingBuffer) Len() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.count
}

// Lines returns all lines in chronological order (oldest first).
func (rb *RingBuffer) Lines() []string {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.count == 0 {
		return nil
	}

	result := make([]string, rb.count)
	if rb.count < rb.capacity {
		// Buffer not full yet, lines are at the start
		copy(result, rb.lines[:rb.count])
	} else {
		// Buffer is full, head points to oldest entry
		// Copy from head to end, then from start to head
		firstPart := rb.capacity - rb.head
		copy(result[:firstPart], rb.lines[rb.head:])
		copy(result[firstPart:], rb.lines[:rb.head])
	}
	return result
}

// Tail returns the last n lines in chronological order.
// If n > count, returns all available lines.
func (rb *RingBuffer) Tail(n int) []string {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.count == 0 || n <= 0 {
		return nil
	}

	if n > rb.count {
		n = rb.count
	}

	result := make([]string, n)

	// Calculate the start position for the last n lines
	// head-1 is the most recent, we need to go back n lines
	start := (rb.head - n + rb.capacity) % rb.capacity

	if start+n <= rb.capacity {
		// Contiguous range
		copy(result, rb.lines[start:start+n])
	} else {
		// Wraps around
		firstPart := rb.capacity - start
		copy(result[:firstPart], rb.lines[start:])
		copy(result[firstPart:], rb.lines[:n-firstPart])
	}

	return result
}

// Slice returns lines from start index to end index (exclusive).
// Indices are logical (0 = oldest line in buffer).
// Returns nil if start >= count or start >= end.
func (rb *RingBuffer) Slice(start, end int) []string {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.count == 0 || start >= rb.count || start >= end || start < 0 {
		return nil
	}

	if end > rb.count {
		end = rb.count
	}

	n := end - start
	result := make([]string, n)

	// Convert logical index to physical index
	var physicalStart int
	if rb.count < rb.capacity {
		physicalStart = start
	} else {
		physicalStart = (rb.head + start) % rb.capacity
	}

	if physicalStart+n <= rb.capacity {
		// Contiguous range
		copy(result, rb.lines[physicalStart:physicalStart+n])
	} else {
		// Wraps around
		firstPart := rb.capacity - physicalStart
		copy(result[:firstPart], rb.lines[physicalStart:])
		copy(result[firstPart:], rb.lines[:n-firstPart])
	}

	return result
}

// Clear removes all lines from the buffer.
func (rb *RingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.head = 0
	rb.count = 0
}

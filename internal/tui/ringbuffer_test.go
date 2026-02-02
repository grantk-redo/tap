package tui

import (
	"fmt"
	"sync"
	"testing"
)

func TestRingBuffer_New(t *testing.T) {
	rb := NewRingBuffer(100)
	if rb == nil {
		t.Fatal("NewRingBuffer() returned nil")
	}
	if rb.capacity != 100 {
		t.Errorf("capacity = %d, want 100", rb.capacity)
	}
	if rb.Len() != 0 {
		t.Errorf("Len() = %d, want 0", rb.Len())
	}
}

func TestRingBuffer_Append(t *testing.T) {
	rb := NewRingBuffer(5)

	rb.Append("line1")
	rb.Append("line2")
	rb.Append("line3")

	if rb.Len() != 3 {
		t.Errorf("Len() = %d, want 3", rb.Len())
	}

	lines := rb.Lines()
	expected := []string{"line1", "line2", "line3"}
	if len(lines) != len(expected) {
		t.Fatalf("Lines() len = %d, want %d", len(lines), len(expected))
	}
	for i, line := range lines {
		if line != expected[i] {
			t.Errorf("Lines()[%d] = %q, want %q", i, line, expected[i])
		}
	}
}

func TestRingBuffer_Overflow(t *testing.T) {
	rb := NewRingBuffer(3)

	rb.Append("line1")
	rb.Append("line2")
	rb.Append("line3")
	rb.Append("line4") // overwrites line1
	rb.Append("line5") // overwrites line2

	if rb.Len() != 3 {
		t.Errorf("Len() = %d, want 3", rb.Len())
	}

	lines := rb.Lines()
	expected := []string{"line3", "line4", "line5"}
	if len(lines) != len(expected) {
		t.Fatalf("Lines() len = %d, want %d", len(lines), len(expected))
	}
	for i, line := range lines {
		if line != expected[i] {
			t.Errorf("Lines()[%d] = %q, want %q", i, line, expected[i])
		}
	}
}

func TestRingBuffer_Tail(t *testing.T) {
	rb := NewRingBuffer(10)

	for i := 1; i <= 7; i++ {
		rb.Append(fmt.Sprintf("line%d", i))
	}

	tests := []struct {
		n        int
		expected []string
	}{
		{3, []string{"line5", "line6", "line7"}},
		{1, []string{"line7"}},
		{7, []string{"line1", "line2", "line3", "line4", "line5", "line6", "line7"}},
		{10, []string{"line1", "line2", "line3", "line4", "line5", "line6", "line7"}}, // more than available
		{0, nil},
		{-1, nil},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("Tail(%d)", tt.n), func(t *testing.T) {
			result := rb.Tail(tt.n)
			if len(result) != len(tt.expected) {
				t.Errorf("Tail(%d) len = %d, want %d", tt.n, len(result), len(tt.expected))
				return
			}
			for i, line := range result {
				if line != tt.expected[i] {
					t.Errorf("Tail(%d)[%d] = %q, want %q", tt.n, i, line, tt.expected[i])
				}
			}
		})
	}
}

func TestRingBuffer_Tail_Overflow(t *testing.T) {
	rb := NewRingBuffer(3)

	for i := 1; i <= 5; i++ {
		rb.Append(fmt.Sprintf("line%d", i))
	}

	result := rb.Tail(2)
	expected := []string{"line4", "line5"}
	if len(result) != len(expected) {
		t.Fatalf("Tail(2) len = %d, want %d", len(result), len(expected))
	}
	for i, line := range result {
		if line != expected[i] {
			t.Errorf("Tail(2)[%d] = %q, want %q", i, line, expected[i])
		}
	}
}

func TestRingBuffer_Slice(t *testing.T) {
	rb := NewRingBuffer(10)

	for i := 1; i <= 5; i++ {
		rb.Append(fmt.Sprintf("line%d", i))
	}

	tests := []struct {
		start, end int
		expected   []string
	}{
		{0, 3, []string{"line1", "line2", "line3"}},
		{2, 5, []string{"line3", "line4", "line5"}},
		{0, 5, []string{"line1", "line2", "line3", "line4", "line5"}},
		{4, 5, []string{"line5"}},
		{5, 10, nil},  // start >= count
		{-1, 3, nil},  // negative start
		{3, 2, nil},   // start >= end
		{0, 100, []string{"line1", "line2", "line3", "line4", "line5"}}, // end > count
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("Slice(%d,%d)", tt.start, tt.end), func(t *testing.T) {
			result := rb.Slice(tt.start, tt.end)
			if len(result) != len(tt.expected) {
				t.Errorf("Slice(%d,%d) len = %d, want %d", tt.start, tt.end, len(result), len(tt.expected))
				return
			}
			for i, line := range result {
				if line != tt.expected[i] {
					t.Errorf("Slice(%d,%d)[%d] = %q, want %q", tt.start, tt.end, i, line, tt.expected[i])
				}
			}
		})
	}
}

func TestRingBuffer_Slice_Overflow(t *testing.T) {
	rb := NewRingBuffer(3)

	for i := 1; i <= 5; i++ {
		rb.Append(fmt.Sprintf("line%d", i))
	}

	// After overflow: line3, line4, line5 (indices 0, 1, 2)
	result := rb.Slice(1, 3)
	expected := []string{"line4", "line5"}
	if len(result) != len(expected) {
		t.Fatalf("Slice(1,3) len = %d, want %d", len(result), len(expected))
	}
	for i, line := range result {
		if line != expected[i] {
			t.Errorf("Slice(1,3)[%d] = %q, want %q", i, line, expected[i])
		}
	}
}

func TestRingBuffer_Clear(t *testing.T) {
	rb := NewRingBuffer(10)

	rb.Append("line1")
	rb.Append("line2")
	rb.Clear()

	if rb.Len() != 0 {
		t.Errorf("After Clear(), Len() = %d, want 0", rb.Len())
	}

	lines := rb.Lines()
	if len(lines) != 0 {
		t.Errorf("After Clear(), Lines() len = %d, want 0", len(lines))
	}
}

func TestRingBuffer_Empty(t *testing.T) {
	rb := NewRingBuffer(10)

	if rb.Len() != 0 {
		t.Errorf("Empty buffer Len() = %d, want 0", rb.Len())
	}

	lines := rb.Lines()
	if lines != nil {
		t.Errorf("Empty buffer Lines() = %v, want nil", lines)
	}

	tail := rb.Tail(5)
	if tail != nil {
		t.Errorf("Empty buffer Tail(5) = %v, want nil", tail)
	}

	slice := rb.Slice(0, 5)
	if slice != nil {
		t.Errorf("Empty buffer Slice(0,5) = %v, want nil", slice)
	}
}

func TestRingBuffer_ThreadSafety(t *testing.T) {
	rb := NewRingBuffer(100)
	var wg sync.WaitGroup

	// Start multiple writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				rb.Append(fmt.Sprintf("writer%d-line%d", id, j))
			}
		}(i)
	}

	// Start multiple readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = rb.Lines()
				_ = rb.Tail(10)
				_ = rb.Len()
			}
		}()
	}

	wg.Wait()

	// Should have 100 lines (capacity), not panic
	if rb.Len() != 100 {
		t.Errorf("After concurrent writes, Len() = %d, want 100", rb.Len())
	}
}

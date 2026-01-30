package tui

import (
	"testing"
)

func TestTerminal_New(t *testing.T) {
	term := NewTerminal()
	if term == nil {
		t.Fatal("NewTerminal() returned nil")
	}

	// fd should be stdin's file descriptor
	if term.fd < 0 {
		t.Errorf("Terminal fd = %d, want >= 0", term.fd)
	}

	// Should not be in raw mode initially
	if term.isRaw {
		t.Error("Terminal should not be in raw mode initially")
	}
}

func TestTerminal_IsTerminal(t *testing.T) {
	term := NewTerminal()

	// IsTerminal should not panic
	// In test environment, stdout is typically not a TTY
	result := term.IsTerminal()

	// We can't assert the value since it depends on the test environment
	// but we verify it returns a boolean without error
	_ = result
}

func TestTerminal_Restore_NotRaw(t *testing.T) {
	term := NewTerminal()

	// Restore should be safe to call even if not in raw mode
	err := term.Restore()
	if err != nil {
		t.Errorf("Restore() when not raw returned error: %v", err)
	}
}

func TestTerminal_GetSize(t *testing.T) {
	term := NewTerminal()

	width, height, err := term.GetSize()

	// In non-TTY test environments, this might fail
	// We just verify it doesn't panic
	if err != nil {
		// Expected in non-TTY environment
		t.Logf("GetSize() error (expected in non-TTY): %v", err)
		return
	}

	// If it succeeded, dimensions should be reasonable
	if width <= 0 {
		t.Errorf("GetSize() width = %d, want > 0", width)
	}
	if height <= 0 {
		t.Errorf("GetSize() height = %d, want > 0", height)
	}
}

func TestTerminal_EnterRaw_NonTTY(t *testing.T) {
	term := NewTerminal()

	// In test environment (non-TTY), EnterRaw will likely fail
	// We just verify it handles this gracefully
	err := term.EnterRaw()
	if err != nil {
		// Expected in non-TTY environment
		t.Logf("EnterRaw() error (expected in non-TTY): %v", err)
		return
	}

	// If it succeeded, clean up
	defer term.Restore()

	if !term.isRaw {
		t.Error("After EnterRaw(), isRaw should be true")
	}
}

func TestTerminal_EnterRaw_Idempotent(t *testing.T) {
	term := NewTerminal()

	// Skip if not a TTY
	if !term.IsTerminal() {
		t.Skip("Skipping raw mode test in non-TTY environment")
	}

	// First call
	if err := term.EnterRaw(); err != nil {
		t.Fatalf("First EnterRaw() failed: %v", err)
	}
	defer term.Restore()

	// Second call should be idempotent
	if err := term.EnterRaw(); err != nil {
		t.Errorf("Second EnterRaw() failed: %v", err)
	}
}

package tui

import (
	"strings"
	"testing"
)

func TestTUI_New(t *testing.T) {
	tests := []struct {
		name     string
		services []string
	}{
		{"single service", []string{"api"}},
		{"multiple services", []string{"api", "worker", "db"}},
		{"empty services", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := New(tt.services)

			if ui == nil {
				t.Fatal("New() returned nil")
			}

			if len(ui.services) != len(tt.services) {
				t.Errorf("Expected %d services, got %d", len(tt.services), len(ui.services))
			}

			// Check service index mapping
			for i, svc := range tt.services {
				if idx, ok := ui.serviceIndex[svc]; !ok || idx != i {
					t.Errorf("Service %q not correctly indexed, got idx=%d ok=%v", svc, idx, ok)
				}
			}

			// Initial filter should be 0 (all)
			if ui.currentFilter != 0 {
				t.Errorf("Initial filter should be 0, got %d", ui.currentFilter)
			}

			// Log files map should be initialized
			if ui.logFiles == nil {
				t.Error("Log files map should be initialized")
			}
		})
	}
}

func TestTUI_CurrentFilter(t *testing.T) {
	services := []string{"api", "worker", "db"}
	ui := New(services)

	tests := []struct {
		filterIdx int
		expected  string
	}{
		{0, ""},       // all
		{1, "api"},
		{2, "worker"},
		{3, "db"},
	}

	for _, tt := range tests {
		ui.SetFilter(tt.filterIdx)
		if filter := ui.CurrentFilter(); filter != tt.expected {
			t.Errorf("After SetFilter(%d), CurrentFilter() = %q, want %q", tt.filterIdx, filter, tt.expected)
		}
	}
}

func TestTUI_CurrentFilterIndex(t *testing.T) {
	services := []string{"api", "worker"}
	ui := New(services)

	if idx := ui.CurrentFilterIndex(); idx != 0 {
		t.Errorf("Initial CurrentFilterIndex() = %d, want 0", idx)
	}

	ui.SetFilter(2)
	if idx := ui.CurrentFilterIndex(); idx != 2 {
		t.Errorf("After SetFilter(2), CurrentFilterIndex() = %d, want 2", idx)
	}
}

func TestTUI_SetFilter_ValidBounds(t *testing.T) {
	services := []string{"api", "worker", "db"}
	ui := New(services)

	tests := []struct {
		input    int
		expected int
	}{
		{0, 0},
		{1, 1},
		{2, 2},
		{3, 3},
	}

	for _, tt := range tests {
		ui.SetFilter(tt.input)
		if got := ui.CurrentFilterIndex(); got != tt.expected {
			t.Errorf("SetFilter(%d) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestTUI_SetFilter_InvalidBounds(t *testing.T) {
	services := []string{"api", "worker"}
	ui := New(services)

	tests := []struct {
		name     string
		input    int
		expected int // should stay at previous value
	}{
		{"negative", -1, 0},
		{"beyond service count", 5, 0},
		{"way beyond", 100, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui.SetFilter(0) // reset
			ui.SetFilter(tt.input)
			if got := ui.CurrentFilterIndex(); got != tt.expected {
				t.Errorf("SetFilter(%d) should be ignored, got filter %d, want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestTUI_HandleKey_FilterSelection(t *testing.T) {
	services := []string{"api", "worker", "db"}
	ui := New(services)

	tests := []struct {
		key      byte
		expected int
	}{
		{'0', 0},
		{'1', 1},
		{'2', 2},
		{'3', 3},
		{'4', 3}, // no service 4, should stay at 3
		{'9', 3}, // no service 9, should stay at 3
		{'0', 0}, // back to all
	}

	for _, tt := range tests {
		ui.handleKey(tt.key)
		if got := ui.CurrentFilterIndex(); got != tt.expected {
			t.Errorf("After handleKey(%q), CurrentFilterIndex() = %d, want %d", tt.key, got, tt.expected)
		}
	}
}

func TestTUI_HandleKey_CtrlC(t *testing.T) {
	// Note: We can't test Ctrl+C handling directly because it sends SIGINT
	// to the current process, which would kill the test.
	t.Log("Ctrl+C handling tested manually - sends SIGINT to process")
}

func TestTUI_HandleKey_IgnoresOtherKeys(t *testing.T) {
	services := []string{"api", "worker"}
	ui := New(services)

	ui.SetFilter(1)

	// These keys should not change the filter
	ignoredKeys := []byte{'a', 'z', ' ', '\n', '\t', 'A', 'Z'}
	for _, key := range ignoredKeys {
		ui.handleKey(key)
		if got := ui.CurrentFilterIndex(); got != 1 {
			t.Errorf("handleKey(%q) should not change filter, got %d", key, got)
		}
	}
}

func TestTUI_ServiceWriter(t *testing.T) {
	services := []string{"api", "worker"}
	ui := New(services)

	writer := ui.ServiceWriter("api")
	if writer == nil {
		t.Fatal("ServiceWriter() returned nil")
	}
}

func TestTUI_IsEnabled(t *testing.T) {
	services := []string{"api"}
	ui := New(services)

	// IsEnabled should not panic
	_ = ui.IsEnabled()
}

func TestServiceWriter_Write(t *testing.T) {
	services := []string{"api"}
	ui := New(services)
	ui.enabled = false // Disable actual output

	writer := &serviceWriter{tui: ui, service: "api"}

	tests := []struct {
		input    string
		expected int
	}{
		{"hello\n", 6},
		{"hello", 5},
		{"multi\nline\n", 11},
		{"\n", 1},
		{"", 0},
	}

	for _, tt := range tests {
		n, err := writer.Write([]byte(tt.input))
		if err != nil {
			t.Errorf("Write(%q) error = %v", tt.input, err)
		}
		if n != tt.expected {
			t.Errorf("Write(%q) = %d, want %d", tt.input, n, tt.expected)
		}
	}
}

func TestLogFile_WriteAndReadAll(t *testing.T) {
	// Create a temp file
	tmpFile, err := newLogFile(t.TempDir() + "/test.log")
	if err != nil {
		t.Fatalf("Failed to create log file: %v", err)
	}
	defer tmpFile.Close()

	// Write some lines
	lines := []string{"line 1", "line 2", "line 3", "line 4", "line 5"}
	for _, line := range lines {
		tmpFile.WriteLine(line)
	}

	// Read all lines
	result := tmpFile.ReadAllLines()
	if len(result) != 5 {
		t.Errorf("Expected 5 lines, got %d", len(result))
	}

	for i, line := range result {
		if line != lines[i] {
			t.Errorf("Line %d: got %q, want %q", i, line, lines[i])
		}
	}
}

func TestLogFile_ReadAllLines_Empty(t *testing.T) {
	tmpFile, err := newLogFile(t.TempDir() + "/test.log")
	if err != nil {
		t.Fatalf("Failed to create log file: %v", err)
	}
	defer tmpFile.Close()

	// Don't write anything
	result := tmpFile.ReadAllLines()
	if len(result) != 0 {
		t.Errorf("Expected 0 lines from empty file, got %d", len(result))
	}
}

func TestLogFile_LineCount(t *testing.T) {
	tmpFile, err := newLogFile(t.TempDir() + "/test.log")
	if err != nil {
		t.Fatalf("Failed to create log file: %v", err)
	}
	defer tmpFile.Close()

	if tmpFile.LineCount() != 0 {
		t.Errorf("Expected 0 lines initially, got %d", tmpFile.LineCount())
	}

	tmpFile.WriteLine("line 1")
	tmpFile.WriteLine("line 2")

	if tmpFile.LineCount() != 2 {
		t.Errorf("Expected 2 lines, got %d", tmpFile.LineCount())
	}
}

func TestTUI_Integration_PrefixFormat(t *testing.T) {
	services := []string{"api", "worker"}
	ui := New(services)

	// Test that prefix only colors the service tag, not the message
	prefix := ui.renderer.FormatServicePrefix("api", 0)
	message := "ERROR: Something went wrong"
	fullLine := prefix + message

	// Reset should appear before the message
	resetIdx := strings.Index(fullLine, Reset)
	messageIdx := strings.Index(fullLine, message)

	if resetIdx == -1 || messageIdx == -1 {
		t.Fatal("Expected both Reset and message in output")
	}

	if resetIdx > messageIdx {
		t.Error("Reset should appear before message to ensure message is not colored")
	}
}

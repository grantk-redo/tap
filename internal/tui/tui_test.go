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
			if ui.CurrentFilterIndex() != 0 {
				t.Errorf("Initial filter should be 0, got %d", ui.CurrentFilterIndex())
			}

			// Log channel should be initialized
			if ui.logChan == nil {
				t.Error("Log channel should be initialized")
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

func TestModel_New(t *testing.T) {
	services := []string{"api", "worker", "db"}
	logChan := make(chan LogMsg, 10)
	model := NewModel(services, logChan, nil)

	if len(model.services) != len(services) {
		t.Errorf("Expected %d services, got %d", len(services), len(model.services))
	}

	if model.currentFilter != 0 {
		t.Errorf("Initial currentFilter = %d, want 0", model.currentFilter)
	}

	if model.scrollOffset != 0 {
		t.Errorf("Initial scrollOffset = %d, want 0", model.scrollOffset)
	}

	// Should have a buffer for each service plus allLogs
	if len(model.logBuffers) != len(services) {
		t.Errorf("Expected %d log buffers, got %d", len(services), len(model.logBuffers))
	}

	if model.allLogs == nil {
		t.Error("allLogs buffer should be initialized")
	}
}

func TestModel_AppendLog(t *testing.T) {
	services := []string{"api", "worker"}
	logChan := make(chan LogMsg, 10)
	model := NewModel(services, logChan, nil)

	model.appendLog(LogMsg{Service: "api", Text: "test message"})

	// Check service buffer
	if model.logBuffers["api"].Len() != 1 {
		t.Errorf("api buffer len = %d, want 1", model.logBuffers["api"].Len())
	}

	// Check all logs buffer
	if model.allLogs.Len() != 1 {
		t.Errorf("allLogs len = %d, want 1", model.allLogs.Len())
	}

	// All logs should have prefix
	lines := model.allLogs.Lines()
	if !strings.Contains(lines[0], "[api]") {
		t.Errorf("allLogs entry should contain service prefix, got %q", lines[0])
	}
}

func TestModel_CurrentFilter(t *testing.T) {
	services := []string{"api", "worker", "db"}
	logChan := make(chan LogMsg, 10)
	model := NewModel(services, logChan, nil)

	if model.CurrentFilter() != 0 {
		t.Errorf("Initial CurrentFilter() = %d, want 0", model.CurrentFilter())
	}

	model.currentFilter = 2
	if model.CurrentFilter() != 2 {
		t.Errorf("After setting currentFilter=2, CurrentFilter() = %d, want 2", model.CurrentFilter())
	}
}

func TestModel_CurrentFilterService(t *testing.T) {
	services := []string{"api", "worker", "db"}
	logChan := make(chan LogMsg, 10)
	model := NewModel(services, logChan, nil)

	tests := []struct {
		filter   int
		expected string
	}{
		{0, ""},
		{1, "api"},
		{2, "worker"},
		{3, "db"},
		{4, ""}, // out of bounds
	}

	for _, tt := range tests {
		model.currentFilter = tt.filter
		if got := model.CurrentFilterService(); got != tt.expected {
			t.Errorf("CurrentFilterService() with filter=%d = %q, want %q", tt.filter, got, tt.expected)
		}
	}
}

func TestModel_CurrentBuffer(t *testing.T) {
	services := []string{"api", "worker"}
	logChan := make(chan LogMsg, 10)
	model := NewModel(services, logChan, nil)

	// Filter 0 should return allLogs
	model.currentFilter = 0
	if model.currentBuffer() != model.allLogs {
		t.Error("currentBuffer() with filter=0 should return allLogs")
	}

	// Filter 1 should return api buffer
	model.currentFilter = 1
	if model.currentBuffer() != model.logBuffers["api"] {
		t.Error("currentBuffer() with filter=1 should return api buffer")
	}

	// Filter 2 should return worker buffer
	model.currentFilter = 2
	if model.currentBuffer() != model.logBuffers["worker"] {
		t.Error("currentBuffer() with filter=2 should return worker buffer")
	}

	// Out of bounds should return nil
	model.currentFilter = 10
	if model.currentBuffer() != nil {
		t.Error("currentBuffer() with filter=10 should return nil")
	}
}

func TestModel_Scroll(t *testing.T) {
	services := []string{"api"}
	logChan := make(chan LogMsg, 10)
	model := NewModel(services, logChan, nil)
	model.height = 10 // viewport of 6 lines (10 - 4 for header/separator/newline/help)

	// Add some logs
	for i := 0; i < 20; i++ {
		model.appendLog(LogMsg{Service: "api", Text: "line"})
	}

	// Initially at bottom
	if model.scrollOffset != 0 {
		t.Errorf("Initial scrollOffset = %d, want 0", model.scrollOffset)
	}

	// Scroll up
	model.scrollUp(5)
	if model.scrollOffset != 5 {
		t.Errorf("After scrollUp(5), scrollOffset = %d, want 5", model.scrollOffset)
	}

	// Scroll down
	model.scrollDown(3)
	if model.scrollOffset != 2 {
		t.Errorf("After scrollDown(3), scrollOffset = %d, want 2", model.scrollOffset)
	}

	// Scroll down past bottom
	model.scrollDown(10)
	if model.scrollOffset != 0 {
		t.Errorf("After scrollDown(10), scrollOffset = %d, want 0", model.scrollOffset)
	}
}

func TestModel_ScrollMaxOffset(t *testing.T) {
	services := []string{"api"}
	logChan := make(chan LogMsg, 10)
	model := NewModel(services, logChan, nil)
	model.height = 10 // viewport of 6 lines (10 - 4 for header/separator/newline/help)

	// Add 20 logs
	for i := 0; i < 20; i++ {
		model.appendLog(LogMsg{Service: "api", Text: "line"})
	}

	// Try to scroll way past top
	model.scrollUp(100)

	// Max offset should be 20 - 6 = 14
	if model.scrollOffset != 14 {
		t.Errorf("After scrollUp(100), scrollOffset = %d, want 14", model.scrollOffset)
	}
}

func TestModel_View_NotReady(t *testing.T) {
	services := []string{"api"}
	logChan := make(chan LogMsg, 10)
	model := NewModel(services, logChan, nil)

	// Model not ready yet
	view := model.View()
	if !strings.Contains(view, "Initializing") {
		t.Errorf("View() before ready should contain 'Initializing', got %q", view)
	}
}

func TestModel_RenderHeader(t *testing.T) {
	services := []string{"api", "worker"}
	logChan := make(chan LogMsg, 10)
	model := NewModel(services, logChan, nil)

	header := model.renderHeader()

	// Should contain All
	if !strings.Contains(header, "[0] All") {
		t.Error("Header should contain '[0] All'")
	}

	// Should contain services
	if !strings.Contains(header, "[1] api") {
		t.Error("Header should contain '[1] api'")
	}
	if !strings.Contains(header, "[2] worker") {
		t.Error("Header should contain '[2] worker'")
	}
}

func TestModel_FormatServicePrefix(t *testing.T) {
	services := []string{"api", "worker"}
	logChan := make(chan LogMsg, 10)
	model := NewModel(services, logChan, nil)

	prefix := model.formatServicePrefix("api", 0)

	// Should contain service name in brackets
	if !strings.Contains(prefix, "[api]") {
		t.Errorf("Prefix should contain '[api]', got %q", prefix)
	}

	// Should end with a space for separation
	if !strings.HasSuffix(prefix, " ") {
		t.Errorf("Prefix should end with space, got %q", prefix)
	}
}

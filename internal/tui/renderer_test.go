package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderer_DrawHeader(t *testing.T) {
	tests := []struct {
		name     string
		services []string
		selected int
		width    int
		contains []string
		notContains []string
	}{
		{
			name:     "all selected with two services",
			services: []string{"api", "worker"},
			selected: 0,
			width:    80,
			contains: []string{"[0] All", "[1] api", "[2] worker", InvertColors},
		},
		{
			name:     "first service selected",
			services: []string{"api", "worker"},
			selected: 1,
			width:    80,
			contains: []string{"[0] All", "[1] api", "[2] worker"},
		},
		{
			name:     "second service selected",
			services: []string{"api", "worker"},
			selected: 2,
			width:    80,
			contains: []string{"[0] All", "[1] api", "[2] worker"},
		},
		{
			name:     "single service",
			services: []string{"myservice"},
			selected: 0,
			width:    80,
			contains: []string{"[0] All", "[1] myservice"},
		},
		{
			name:     "many services - only first 9 shown",
			services: []string{"svc1", "svc2", "svc3", "svc4", "svc5", "svc6", "svc7", "svc8", "svc9", "svc10"},
			selected: 0,
			width:    200,
			contains: []string{"[1] svc1", "[9] svc9"},
			notContains: []string{"[10]", "svc10"},
		},
		{
			name:     "empty services",
			services: []string{},
			selected: 0,
			width:    80,
			contains: []string{"[0] All"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			r := NewRenderer(&buf)
			r.DrawHeader(tt.services, tt.selected, tt.width)

			output := buf.String()
			for _, substr := range tt.contains {
				if !strings.Contains(output, substr) {
					t.Errorf("DrawHeader() output missing %q\nGot: %q", substr, output)
				}
			}
			for _, substr := range tt.notContains {
				if strings.Contains(output, substr) {
					t.Errorf("DrawHeader() output should not contain %q\nGot: %q", substr, output)
				}
			}
		})
	}
}

func TestRenderer_DrawHeader_SelectionHighlight(t *testing.T) {
	tests := []struct {
		name     string
		selected int
	}{
		{"all selected", 0},
		{"first service selected", 1},
		{"second service selected", 2},
	}

	services := []string{"api", "worker"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			r := NewRenderer(&buf)
			r.DrawHeader(services, tt.selected, 80)

			output := buf.String()

			// Should contain InvertColors for selected item
			if !strings.Contains(output, InvertColors) {
				t.Error("Expected InvertColors in output for selected item")
			}

			// Should contain Reset to clear formatting
			if !strings.Contains(output, Reset) {
				t.Error("Expected Reset code in output")
			}
		})
	}
}

func TestRenderer_FormatServicePrefix(t *testing.T) {
	tests := []struct {
		name    string
		service string
		index   int
	}{
		{"first service", "api", 0},
		{"second service", "worker", 1},
		{"service with index wrap", "db", 10}, // should wrap colors
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			r := NewRenderer(&buf)

			prefix := r.FormatServicePrefix(tt.service, tt.index)

			// Should contain service name in brackets
			if !strings.Contains(prefix, "["+tt.service+"]") {
				t.Errorf("FormatServicePrefix() = %q, want to contain '[%s]'", prefix, tt.service)
			}

			// Should contain Reset code to not color the message
			if !strings.Contains(prefix, Reset) {
				t.Errorf("FormatServicePrefix() = %q, want to contain Reset code", prefix)
			}

			// Should end with space after Reset
			if !strings.HasSuffix(prefix, Reset+" ") {
				t.Errorf("FormatServicePrefix() = %q, should end with Reset and space", prefix)
			}

			// Should start with a color code
			hasColor := false
			for _, color := range serviceColors {
				if strings.HasPrefix(prefix, color) {
					hasColor = true
					break
				}
			}
			if !hasColor {
				t.Errorf("FormatServicePrefix() = %q, should start with a color code", prefix)
			}
		})
	}
}

func TestRenderer_FormatServicePrefix_DoesNotColorMessage(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)

	prefix := r.FormatServicePrefix("api", 0)
	message := "This is a log message"
	fullLine := prefix + message

	// The message part should not have any color codes
	// Find where the prefix ends (after Reset + space)
	resetIdx := strings.LastIndex(fullLine, Reset)
	if resetIdx == -1 {
		t.Fatal("Expected Reset code in output")
	}

	// Everything after Reset should be uncolored
	afterReset := fullLine[resetIdx+len(Reset):]
	for _, color := range serviceColors {
		if strings.Contains(afterReset, color) {
			t.Errorf("Message part should not contain color codes, got: %q", afterReset)
		}
	}
}

func TestRenderer_GetServiceColor(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)

	// Colors should be consistent for same index
	color1 := r.GetServiceColor(0)
	color2 := r.GetServiceColor(0)
	if color1 != color2 {
		t.Error("Same index should return same color")
	}

	// Different indices should have different colors (up to serviceColors length)
	color0 := r.GetServiceColor(0)
	color1 = r.GetServiceColor(1)
	if color0 == color1 {
		t.Error("Different indices should return different colors")
	}

	// Should wrap around
	colorWrap := r.GetServiceColor(len(serviceColors))
	if color0 != colorWrap {
		t.Error("Colors should wrap around")
	}
}

func TestRenderer_ANSICodes(t *testing.T) {
	tests := []struct {
		name     string
		action   func(*Renderer)
		expected string
	}{
		{
			name:     "ClearScreenAll",
			action:   func(r *Renderer) { r.ClearScreenAll() },
			expected: ClearScreen,
		},
		{
			name:     "ClearCurrentLine",
			action:   func(r *Renderer) { r.ClearCurrentLine() },
			expected: ClearLine,
		},
		{
			name:     "SaveCursorPosition",
			action:   func(r *Renderer) { r.SaveCursorPosition() },
			expected: SaveCursor,
		},
		{
			name:     "RestoreCursorPosition",
			action:   func(r *Renderer) { r.RestoreCursorPosition() },
			expected: RestoreCursor,
		},
		{
			name:     "HideCursorDisplay",
			action:   func(r *Renderer) { r.HideCursorDisplay() },
			expected: HideCursor,
		},
		{
			name:     "ShowCursorDisplay",
			action:   func(r *Renderer) { r.ShowCursorDisplay() },
			expected: ShowCursor,
		},
		{
			name:     "ResetScrollRegion",
			action:   func(r *Renderer) { r.ResetScrollRegion() },
			expected: ResetScrollAll,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			r := NewRenderer(&buf)
			tt.action(r)
			if buf.String() != tt.expected {
				t.Errorf("%s wrote %q, want %q", tt.name, buf.String(), tt.expected)
			}
		})
	}
}

func TestRenderer_MoveTo(t *testing.T) {
	tests := []struct {
		row, col int
		expected string
	}{
		{1, 1, "\x1b[1;1H"},
		{5, 10, "\x1b[5;10H"},
		{24, 80, "\x1b[24;80H"},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			var buf bytes.Buffer
			r := NewRenderer(&buf)
			r.MoveTo(tt.row, tt.col)
			if buf.String() != tt.expected {
				t.Errorf("MoveTo(%d, %d) wrote %q, want %q", tt.row, tt.col, buf.String(), tt.expected)
			}
		})
	}
}

func TestRenderer_SetScrollRegion(t *testing.T) {
	tests := []struct {
		top, bottom int
		expected    string
	}{
		{2, 24, "\x1b[2;24r"},
		{1, 100, "\x1b[1;100r"},
		{5, 10, "\x1b[5;10r"},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			var buf bytes.Buffer
			r := NewRenderer(&buf)
			r.SetScrollRegion(tt.top, tt.bottom)
			if buf.String() != tt.expected {
				t.Errorf("SetScrollRegion(%d, %d) wrote %q, want %q", tt.top, tt.bottom, buf.String(), tt.expected)
			}
		})
	}
}

func TestRenderer_AltScreen(t *testing.T) {
	var buf bytes.Buffer
	r := NewRenderer(&buf)

	r.EnterAltScreen()
	if buf.String() != AltScreenOn {
		t.Errorf("EnterAltScreen() wrote %q, want %q", buf.String(), AltScreenOn)
	}

	buf.Reset()
	r.ExitAltScreen()
	if buf.String() != AltScreenOff {
		t.Errorf("ExitAltScreen() wrote %q, want %q", buf.String(), AltScreenOff)
	}
}

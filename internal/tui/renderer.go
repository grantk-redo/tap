package tui

import (
	"fmt"
	"io"
	"strings"
)

// ANSI escape sequences
const (
	CursorHome     = "\x1b[H"        // Move to top-left
	ClearLine      = "\x1b[2K"       // Clear entire line
	SaveCursor     = "\x1b[s"        // Save cursor position
	RestoreCursor  = "\x1b[u"        // Restore cursor position
	HideCursor     = "\x1b[?25l"     // Hide cursor
	ShowCursor     = "\x1b[?25h"     // Show cursor
	Bold           = "\x1b[1m"       // Bold text
	Reset          = "\x1b[0m"       // Reset all attributes
	InvertColors   = "\x1b[7m"       // Inverted colors (for selection)
	Dim            = "\x1b[2m"       // Dim text
	ClearScreen    = "\x1b[2J"       // Clear entire screen
	ResetScrollAll = "\x1b[r"        // Reset scroll region to full screen
	AltScreenOn    = "\x1b[?1049h"   // Enter alternate screen buffer
	AltScreenOff   = "\x1b[?1049l"   // Exit alternate screen buffer
)

// Service colors for distinguishing services in "all" view
var serviceColors = []string{
	"\x1b[36m", // Cyan
	"\x1b[33m", // Yellow
	"\x1b[35m", // Magenta
	"\x1b[32m", // Green
	"\x1b[34m", // Blue
	"\x1b[91m", // Bright Red
	"\x1b[92m", // Bright Green
	"\x1b[93m", // Bright Yellow
	"\x1b[94m", // Bright Blue
}

// Renderer handles ANSI-based terminal rendering.
type Renderer struct {
	out io.Writer
}

// NewRenderer creates a new Renderer that writes to the given writer.
func NewRenderer(out io.Writer) *Renderer {
	return &Renderer{out: out}
}

// SetScrollRegion sets the terminal scroll region (1-indexed, inclusive).
// Lines outside this region won't scroll.
func (r *Renderer) SetScrollRegion(top, bottom int) {
	fmt.Fprintf(r.out, "\x1b[%d;%dr", top, bottom)
}

// ResetScrollRegion resets the scroll region to the full terminal.
func (r *Renderer) ResetScrollRegion() {
	fmt.Fprint(r.out, ResetScrollAll)
}

// MoveTo moves the cursor to the specified position (1-indexed).
func (r *Renderer) MoveTo(row, col int) {
	fmt.Fprintf(r.out, "\x1b[%d;%dH", row, col)
}

// ClearCurrentLine clears the current line.
func (r *Renderer) ClearCurrentLine() {
	fmt.Fprint(r.out, ClearLine)
}

// SaveCursorPosition saves the current cursor position.
func (r *Renderer) SaveCursorPosition() {
	fmt.Fprint(r.out, SaveCursor)
}

// RestoreCursorPosition restores the previously saved cursor position.
func (r *Renderer) RestoreCursorPosition() {
	fmt.Fprint(r.out, RestoreCursor)
}

// ClearScreenAll clears the entire screen.
func (r *Renderer) ClearScreenAll() {
	fmt.Fprint(r.out, ClearScreen)
}

// HideCursorDisplay hides the cursor.
func (r *Renderer) HideCursorDisplay() {
	fmt.Fprint(r.out, HideCursor)
}

// ShowCursorDisplay shows the cursor.
func (r *Renderer) ShowCursorDisplay() {
	fmt.Fprint(r.out, ShowCursor)
}

// DrawHeader renders the service selection header at the top of the terminal.
// selected is the 0-based filter index (0 = all, 1+ = specific service).
// width is the terminal width for potential truncation.
func (r *Renderer) DrawHeader(services []string, selected int, width int) {
	r.SaveCursorPosition()
	r.MoveTo(1, 1)
	r.ClearCurrentLine()

	var buf strings.Builder

	// [0] All
	if selected == 0 {
		buf.WriteString(InvertColors)
	}
	buf.WriteString("[0] All")
	buf.WriteString(Reset)
	buf.WriteString("  ")

	// [1] service1  [2] service2  etc.
	for i, svc := range services {
		if i >= 9 { // Only support 1-9 keys
			break
		}
		if selected == i+1 {
			buf.WriteString(InvertColors)
		} else {
			// Use service color
			buf.WriteString(serviceColors[i%len(serviceColors)])
		}
		fmt.Fprintf(&buf, "[%d] %s", i+1, svc)
		buf.WriteString(Reset)
		buf.WriteString("  ")
	}

	header := buf.String()

	// Truncate if header is too wide (accounting for ANSI codes)
	// For simplicity, we write as-is and let terminal handle wrapping
	// A more sophisticated approach would strip ANSI, measure, then truncate
	fmt.Fprint(r.out, header)

	r.RestoreCursorPosition()
}

// GetServiceColor returns the ANSI color code for a service by its index.
func (r *Renderer) GetServiceColor(index int) string {
	return serviceColors[index%len(serviceColors)]
}

// FormatServicePrefix formats a service name with color for log output.
func (r *Renderer) FormatServicePrefix(service string, index int) string {
	color := serviceColors[index%len(serviceColors)]
	return fmt.Sprintf("%s[%s]%s ", color, service, Reset)
}

// EnterAltScreen switches to the alternate screen buffer.
func (r *Renderer) EnterAltScreen() {
	fmt.Fprint(r.out, AltScreenOn)
}

// ExitAltScreen returns to the main screen buffer.
func (r *Renderer) ExitAltScreen() {
	fmt.Fprint(r.out, AltScreenOff)
}

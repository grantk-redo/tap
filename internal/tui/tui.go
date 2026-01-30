package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
)

// TUI provides an interactive terminal interface for filtering multi-service logs.
type TUI struct {
	services      []string
	serviceIndex  map[string]int
	logFiles      map[string]*logFile
	allLogs       *logFile // Combined log file for "All" view scrolling
	currentFilter int
	scrollOffset  int // lines from bottom (0 = showing newest)
	terminal      *Terminal
	renderer      *Renderer
	mu            sync.Mutex
	width         int
	height        int
	enabled       bool
	started       bool
	cancelInput   context.CancelFunc
	inputDone     chan struct{}
	tempDir       string
}

// logFile wraps a file for writing and reading logs.
type logFile struct {
	path  string
	file  *os.File
	mu    sync.Mutex
	lines int
}

// New creates a new TUI for the given services.
func New(services []string) *TUI {
	t := &TUI{
		services:      services,
		serviceIndex:  make(map[string]int),
		logFiles:      make(map[string]*logFile),
		currentFilter: 0,
		scrollOffset:  0,
		terminal:      NewTerminal(),
		renderer:      NewRenderer(os.Stdout),
		inputDone:     make(chan struct{}),
	}
	for i, s := range services {
		t.serviceIndex[s] = i
	}
	t.enabled = t.terminal.IsTerminal()
	return t
}

// IsEnabled returns true if the TUI is enabled (interactive TTY mode).
func (t *TUI) IsEnabled() bool {
	return t.enabled
}

// Start initializes the TUI.
func (t *TUI) Start(ctx context.Context) error {
	if !t.enabled {
		return nil
	}

	// Create temp directory for log files
	tempDir, err := os.MkdirTemp("", "tap-logs-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	t.tempDir = tempDir

	// Create log file for each service
	for _, svc := range t.services {
		lf, err := newLogFile(filepath.Join(tempDir, svc+".log"))
		if err != nil {
			t.cleanup()
			return fmt.Errorf("failed to create log file for %s: %w", svc, err)
		}
		t.logFiles[svc] = lf
	}

	// Create combined log file for "All" view scrolling
	allLogs, err := newLogFile(filepath.Join(tempDir, "_all.log"))
	if err != nil {
		t.cleanup()
		return fmt.Errorf("failed to create combined log file: %w", err)
	}
	t.allLogs = allLogs

	// Get terminal size
	width, height, err := t.terminal.GetSize()
	if err != nil {
		width, height = 80, 24
	}
	t.width = width
	t.height = height

	// Enter raw mode
	if err := t.terminal.EnterRaw(); err != nil {
		t.cleanup()
		return fmt.Errorf("failed to enter raw mode: %w", err)
	}

	t.started = true

	// Enter alternate screen buffer
	t.renderer.EnterAltScreen()

	// Draw initial screen
	t.drawHeader()
	t.renderer.SetScrollRegion(2, t.height)
	t.renderer.MoveTo(2, 1)

	// Handle terminal resize
	go t.handleResize(ctx)

	// Start input loop
	inputCtx, cancel := context.WithCancel(ctx)
	t.cancelInput = cancel
	go t.inputLoop(inputCtx)

	return nil
}

// Stop restores the terminal to its original state.
func (t *TUI) Stop() error {
	if !t.enabled || !t.started {
		return nil
	}

	if t.cancelInput != nil {
		t.cancelInput()
	}

	t.renderer.ResetScrollRegion()
	t.renderer.ExitAltScreen()
	t.renderer.ShowCursorDisplay()

	err := t.terminal.Restore()
	t.cleanup()

	return err
}

func (t *TUI) cleanup() {
	for _, lf := range t.logFiles {
		lf.Close()
	}
	if t.allLogs != nil {
		t.allLogs.Close()
	}
	if t.tempDir != "" {
		os.RemoveAll(t.tempDir)
	}
}

// CurrentFilter returns the name of the currently filtered service.
func (t *TUI) CurrentFilter() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.currentFilter == 0 || t.currentFilter > len(t.services) {
		return ""
	}
	return t.services[t.currentFilter-1]
}

// CurrentFilterIndex returns the current filter index.
func (t *TUI) CurrentFilterIndex() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.currentFilter
}

// SetFilter sets the current filter by index.
func (t *TUI) SetFilter(idx int) {
	t.mu.Lock()
	if idx < 0 || idx > len(t.services) || idx > 9 {
		t.mu.Unlock()
		return
	}
	oldFilter := t.currentFilter
	t.currentFilter = idx
	t.scrollOffset = 0 // Reset scroll when changing filter
	t.mu.Unlock()

	if t.enabled && t.started && oldFilter != idx {
		// Both All and filtered views now support scrolling
		t.redrawFilteredView()
	}
}

// ServiceWriter returns an io.Writer that writes output for the given service.
func (t *TUI) ServiceWriter(service string) io.Writer {
	return &serviceWriter{tui: t, service: service}
}

// WriteOutput writes a log line for a service.
func (t *TUI) WriteOutput(service string, text string) {
	// Always write to service-specific file
	if lf, ok := t.logFiles[service]; ok {
		lf.WriteLine(text)
	}

	// Also write to combined log file with prefix
	idx, ok := t.serviceIndex[service]
	if !ok {
		idx = 0
	}
	prefix := t.renderer.FormatServicePrefix(service, idx)
	if t.allLogs != nil {
		t.allLogs.WriteLine(prefix + text)
	}

	t.mu.Lock()
	filter := t.currentFilter
	scrollOffset := t.scrollOffset
	t.mu.Unlock()

	// In "All" mode (filter 0), stream to screen only if at bottom
	if filter == 0 {
		if scrollOffset == 0 {
			fmt.Fprint(os.Stdout, prefix+text+"\r\n")
		}
		// If scrolled up, don't auto-show (user is reading history)
		return
	}

	// In filtered mode, only show if matches AND we're at bottom (scrollOffset == 0)
	if filter-1 < len(t.services) && t.services[filter-1] == service {
		if scrollOffset == 0 {
			// At bottom, show new log
			fmt.Fprint(os.Stdout, text+"\r\n")
		}
		// If scrolled up, don't auto-show (user is reading history)
	}
}

// drawHeader draws the header bar.
func (t *TUI) drawHeader() {
	t.mu.Lock()
	filter := t.currentFilter
	width := t.width
	t.mu.Unlock()

	t.renderer.MoveTo(1, 1)
	t.renderer.ClearCurrentLine()
	t.renderer.DrawHeader(t.services, filter, width)
}

// redrawFilteredView redraws the screen for a filtered service view.
func (t *TUI) redrawFilteredView() {
	t.mu.Lock()
	filter := t.currentFilter
	height := t.height
	scrollOffset := t.scrollOffset
	t.mu.Unlock()

	var lf *logFile
	if filter == 0 {
		lf = t.allLogs
	} else if filter-1 < len(t.services) {
		svc := t.services[filter-1]
		var ok bool
		lf, ok = t.logFiles[svc]
		if !ok {
			return
		}
	} else {
		return
	}

	if lf == nil {
		return
	}

	// Clear screen and redraw
	t.renderer.ClearScreenAll()
	t.drawHeader()
	t.renderer.SetScrollRegion(2, height)
	t.renderer.MoveTo(2, 1)

	// Read all lines and show appropriate window
	lines := lf.ReadAllLines()
	viewHeight := height - 2

	// Calculate which lines to show
	totalLines := len(lines)
	endIdx := totalLines - scrollOffset
	startIdx := endIdx - viewHeight
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx < 0 {
		endIdx = 0
	}
	if endIdx > totalLines {
		endIdx = totalLines
	}

	// Show the lines
	for i := startIdx; i < endIdx; i++ {
		fmt.Fprint(os.Stdout, lines[i]+"\r\n")
	}

	// Show scroll indicator if not at bottom
	if scrollOffset > 0 {
		t.renderer.MoveTo(height, 1)
		fmt.Fprintf(os.Stdout, "\x1b[7m ↑ %d more lines above | ↓ to scroll down \x1b[0m", scrollOffset)
	}
}

// scrollUp scrolls up in filtered view.
func (t *TUI) scrollUp(lines int) {
	t.mu.Lock()
	filter := t.currentFilter

	var lf *logFile
	if filter == 0 {
		lf = t.allLogs
	} else {
		svc := t.services[filter-1]
		var ok bool
		lf, ok = t.logFiles[svc]
		if !ok {
			t.mu.Unlock()
			return
		}
	}

	if lf == nil {
		t.mu.Unlock()
		return
	}

	totalLines := lf.LineCount()
	viewHeight := t.height - 2
	maxOffset := totalLines - viewHeight
	if maxOffset < 0 {
		maxOffset = 0
	}

	t.scrollOffset += lines
	if t.scrollOffset > maxOffset {
		t.scrollOffset = maxOffset
	}
	t.mu.Unlock()

	t.redrawFilteredView()
}

// scrollDown scrolls down in filtered view.
func (t *TUI) scrollDown(lines int) {
	t.mu.Lock()
	t.scrollOffset -= lines
	if t.scrollOffset < 0 {
		t.scrollOffset = 0
	}
	t.mu.Unlock()

	t.redrawFilteredView()
}

// inputLoop reads keystrokes.
func (t *TUI) inputLoop(ctx context.Context) {
	defer close(t.inputDone)

	escBuf := make([]byte, 0, 3)
	inEscape := false

	for {
		select {
		case <-ctx.Done():
			return
		default:
			key, err := t.terminal.ReadKey(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}

			// Handle escape sequences (arrow keys)
			if key == 0x1b {
				inEscape = true
				escBuf = escBuf[:0]
				escBuf = append(escBuf, key)
				continue
			}

			if inEscape {
				escBuf = append(escBuf, key)
				if len(escBuf) == 3 {
					inEscape = false
					t.handleEscapeSequence(escBuf)
					continue
				}
				continue
			}

			t.handleKey(key)
		}
	}
}

// handleEscapeSequence handles arrow key sequences.
func (t *TUI) handleEscapeSequence(seq []byte) {
	if len(seq) != 3 || seq[0] != 0x1b || seq[1] != '[' {
		return
	}

	switch seq[2] {
	case 'A': // Up arrow
		t.scrollUp(1)
	case 'B': // Down arrow
		t.scrollDown(1)
	case '5': // Page Up (need to read one more byte but simplified)
		t.scrollUp(10)
	case '6': // Page Down
		t.scrollDown(10)
	}
}

// handleKey processes a single keystroke.
func (t *TUI) handleKey(key byte) {
	switch key {
	case '0':
		t.SetFilter(0)
	case '1', '2', '3', '4', '5', '6', '7', '8', '9':
		idx := int(key - '0')
		if idx <= len(t.services) {
			t.SetFilter(idx)
		}
	case 'k', 'K': // Vim-style up
		t.scrollUp(1)
	case 'j', 'J': // Vim-style down
		t.scrollDown(1)
	case 'u', 'U': // Half page up
		t.scrollUp(10)
	case 'd', 'D': // Half page down
		t.scrollDown(10)
	case 'g': // Go to top
		t.mu.Lock()
		var lf *logFile
		if t.currentFilter == 0 {
			lf = t.allLogs
		} else {
			svc := t.services[t.currentFilter-1]
			lf = t.logFiles[svc]
		}
		if lf != nil {
			t.scrollOffset = lf.LineCount() - (t.height - 2)
			if t.scrollOffset < 0 {
				t.scrollOffset = 0
			}
		}
		t.mu.Unlock()
		t.redrawFilteredView()
	case 'G': // Go to bottom
		t.mu.Lock()
		t.scrollOffset = 0
		t.mu.Unlock()
		t.redrawFilteredView()
	case 0x03: // Ctrl+C
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
	}
}

// handleResize handles terminal resize.
func (t *TUI) handleResize(ctx context.Context) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)

	for {
		select {
		case <-ctx.Done():
			return
		case <-sigCh:
			width, height, err := t.terminal.GetSize()
			if err != nil {
				continue
			}
			t.mu.Lock()
			t.width = width
			t.height = height
			t.mu.Unlock()

			t.redrawFilteredView()
		}
	}
}

// serviceWriter implements io.Writer for a specific service.
type serviceWriter struct {
	tui     *TUI
	service string
}

func (sw *serviceWriter) Write(p []byte) (n int, err error) {
	text := string(p)
	if len(text) > 0 && text[len(text)-1] == '\n' {
		text = text[:len(text)-1]
	}
	sw.tui.WriteOutput(sw.service, text)
	return len(p), nil
}

// Log file methods

func newLogFile(path string) (*logFile, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &logFile{path: path, file: f}, nil
}

func (lf *logFile) WriteLine(text string) {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	fmt.Fprintln(lf.file, text)
	lf.lines++
}

func (lf *logFile) LineCount() int {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	return lf.lines
}

func (lf *logFile) ReadAllLines() []string {
	lf.mu.Lock()
	defer lf.mu.Unlock()

	lf.file.Sync()

	f, err := os.Open(lf.path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

func (lf *logFile) Close() error {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	return lf.file.Close()
}

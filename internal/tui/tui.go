package tui

import (
	"context"
	"io"
	"os"
	"sync"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// TUI provides an interactive terminal interface for filtering multi-service logs.
type TUI struct {
	services     []string
	serviceIndex map[string]int
	enabled      bool
	started      bool

	logChan  chan LogMsg
	program  *tea.Program
	model    *Model
	modelMu  sync.RWMutex
	doneChan chan struct{}

	// Atomic current filter for lock-free reads from main goroutine
	currentFilterAtomic atomic.Int32
}

// New creates a new TUI for the given services.
func New(services []string) *TUI {
	t := &TUI{
		services:     services,
		serviceIndex: make(map[string]int),
		logChan:      make(chan LogMsg, 1000), // Buffered to avoid blocking writers
		doneChan:     make(chan struct{}),
	}
	for i, s := range services {
		t.serviceIndex[s] = i
	}
	t.enabled = term.IsTerminal(int(os.Stdin.Fd()))
	return t
}

// IsEnabled returns true if the TUI is enabled (interactive TTY mode).
func (t *TUI) IsEnabled() bool {
	return t.enabled
}

// Start initializes and runs the TUI.
func (t *TUI) Start(ctx context.Context) error {
	if !t.enabled {
		return nil
	}

	// Callback to sync filter changes from model to TUI wrapper
	filterChanged := func(idx int) {
		t.currentFilterAtomic.Store(int32(idx))
	}

	model := NewModel(t.services, t.logChan, filterChanged)
	t.modelMu.Lock()
	t.model = &model
	t.modelMu.Unlock()

	t.program = tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(), // Enable mouse for clicks and scroll
		tea.WithoutSignalHandler(), // We handle signals ourselves in main.go
	)

	t.started = true

	// Run bubbletea in a goroutine
	go func() {
		defer close(t.doneChan)
		_, _ = t.program.Run()
	}()

	// Watch for context cancellation to quit
	go func() {
		<-ctx.Done()
		t.program.Quit()
	}()

	return nil
}

// Stop stops the TUI.
func (t *TUI) Stop() error {
	if !t.enabled || !t.started {
		return nil
	}

	if t.program != nil {
		t.program.Quit()
		<-t.doneChan // Wait for program to exit
	}

	close(t.logChan)
	return nil
}

// CurrentFilter returns the name of the currently filtered service.
// Returns empty string for "All" view.
func (t *TUI) CurrentFilter() string {
	idx := int(t.currentFilterAtomic.Load())
	if idx == 0 || idx > len(t.services) {
		return ""
	}
	return t.services[idx-1]
}

// CurrentFilterIndex returns the current filter index.
func (t *TUI) CurrentFilterIndex() int {
	return int(t.currentFilterAtomic.Load())
}

// SetFilter sets the current filter by index.
func (t *TUI) SetFilter(idx int) {
	if idx < 0 || idx > len(t.services) || idx > 9 {
		return
	}
	t.currentFilterAtomic.Store(int32(idx))

	// Send key message to model to sync state
	if t.program != nil {
		key := '0' + byte(idx)
		t.program.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(key)}})
	}
}

// ServiceWriter returns an io.Writer that writes output for the given service.
func (t *TUI) ServiceWriter(service string) io.Writer {
	return &serviceWriter{tui: t, service: service}
}

// WriteOutput writes a log line for a service.
func (t *TUI) WriteOutput(service string, text string) {
	// Send to the model via channel (non-blocking with select to avoid deadlock)
	select {
	case t.logChan <- LogMsg{Service: service, Text: text}:
	default:
		// Channel full, drop the message (should be rare with buffered channel)
	}
}

// WriteSystem writes a system/status message that appears in the log stream.
// Use this for status messages like "Restarting..." instead of log.Printf
// when the TUI is active.
func (t *TUI) WriteSystem(text string) {
	if !t.enabled || !t.started {
		return
	}
	// Use a special "tap" service for system messages
	select {
	case t.logChan <- LogMsg{Service: "tap", Text: text}:
	default:
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

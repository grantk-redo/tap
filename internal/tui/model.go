package tui

import (
	"fmt"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	defaultBufferCapacity = 10000
)

// Styles
var (
	// Tab styles
	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Background(lipgloss.Color("62")).
			Foreground(lipgloss.Color("230")).
			Padding(0, 1)

	inactiveTabStyle = lipgloss.NewStyle().
				Padding(0, 1)

	// Service colors for tabs and log prefixes
	tabColors = []lipgloss.Color{
		lipgloss.Color("6"),   // Cyan
		lipgloss.Color("3"),   // Yellow
		lipgloss.Color("5"),   // Magenta
		lipgloss.Color("2"),   // Green
		lipgloss.Color("4"),   // Blue
		lipgloss.Color("9"),   // Bright Red
		lipgloss.Color("10"),  // Bright Green
		lipgloss.Color("11"),  // Bright Yellow
		lipgloss.Color("12"),  // Bright Blue
	}

	// Header bar style
	headerStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true).
			BorderForeground(lipgloss.Color("240"))

	// Help bar style
	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	// Scroll indicator style
	scrollIndicatorStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("62")).
				Foreground(lipgloss.Color("230")).
				Padding(0, 1)
)

// LogMsg represents an incoming log line from a service.
type LogMsg struct {
	Service string
	Text    string
}

// tabInfo stores position info for clickable tabs
type tabInfo struct {
	startX int
	endX   int
	index  int // 0 = All, 1+ = service index
}

// Model is the bubbletea model for the TUI.
type Model struct {
	services      []string
	serviceIndex  map[string]int
	logBuffers    map[string]*RingBuffer // Per-service buffers
	allLogs       *RingBuffer            // Combined "All" view
	currentFilter int                    // 0 = All, 1+ = specific service, 9 = secret ball game
	scrollOffset  int                    // lines from bottom (0 = showing newest)
	width         int
	height        int
	logChan       <-chan LogMsg // Receives logs from runners
	ready         bool          // true after first WindowSizeMsg
	tabs          []tabInfo     // Tab positions for click detection
	filterChanged func(int)     // Callback when filter changes

	// Secret ball game (press 9)
	ballGame *BallGame
}

// NewModel creates a new TUI model.
func NewModel(services []string, logChan <-chan LogMsg, filterChanged func(int)) Model {
	serviceIndex := make(map[string]int)
	logBuffers := make(map[string]*RingBuffer)

	for i, svc := range services {
		serviceIndex[svc] = i
		logBuffers[svc] = NewRingBuffer(defaultBufferCapacity)
	}

	m := Model{
		services:      services,
		serviceIndex:  serviceIndex,
		logBuffers:    logBuffers,
		allLogs:       NewRingBuffer(defaultBufferCapacity),
		currentFilter: 0,
		scrollOffset:  0,
		logChan:       logChan,
		width:         80,
		height:        24,
		filterChanged: filterChanged,
	}

	// Pre-calculate tab positions (they don't change)
	m.tabs = m.calculateTabs()
	return m
}

// calculateTabs computes the click regions for each tab.
func (m Model) calculateTabs() []tabInfo {
	var tabs []tabInfo
	currentX := 0

	// [0] All tab - padding adds 1 char each side
	allLabel := "[0] All"
	tabWidth := len(allLabel) + 2 // +2 for padding
	tabs = append(tabs, tabInfo{startX: currentX, endX: currentX + tabWidth, index: 0})
	currentX += tabWidth + 1 // +1 for space between tabs

	// Service tabs
	for i, svc := range m.services {
		if i >= 9 {
			break
		}
		label := fmt.Sprintf("[%d] %s", i+1, svc)
		tabWidth := len(label) + 2
		tabs = append(tabs, tabInfo{startX: currentX, endX: currentX + tabWidth, index: i + 1})
		currentX += tabWidth + 1
	}

	return tabs
}

// Init initializes the model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.listenForLogs(),
	)
}

// listenForLogs returns a command that waits for the next log message.
func (m Model) listenForLogs() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-m.logChan
		if !ok {
			return nil // Channel closed
		}
		return msg
	}
}

// Update handles messages and updates the model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		// Update ball game with correct game area dimensions
		if m.ballGame != nil {
			gameHeight := m.height - 4
			if gameHeight < 1 {
				gameHeight = 1
			}
			m.ballGame.width = m.width
			m.ballGame.height = gameHeight
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case LogMsg:
		m.appendLog(msg)
		return m, m.listenForLogs()

	case TickMsg:
		// Update ball game physics
		if m.currentFilter == 9 && m.ballGame != nil {
			// Game area is smaller than full terminal (header + separator + help bar)
			gameHeight := m.height - 4
			if gameHeight < 1 {
				gameHeight = 1
			}
			m.ballGame.Update(m.width, gameHeight)
			return m, Tick()
		}
		return m, nil
	}

	return m, nil
}

// handleMouse processes mouse input (scroll wheel and clicks).
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// In ball game mode, pass mouse to ball game
	if m.currentFilter == 9 && m.ballGame != nil {
		m.ballGame.HandleMouse(msg)
		return m, nil
	}

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scrollUp(3)
	case tea.MouseButtonWheelDown:
		m.scrollDown(3)
	case tea.MouseButtonLeft:
		// Only handle actual left clicks, not motion
		if msg.Y <= 1 {
			// Find which tab was clicked
			for _, tab := range m.tabs {
				if msg.X >= tab.startX && msg.X < tab.endX {
					m.currentFilter = tab.index
					m.scrollOffset = 0
					if m.filterChanged != nil {
						m.filterChanged(tab.index)
					}
					break
				}
			}
		}
	}
	return m, nil
}

// handleKey processes keyboard input.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		// Send SIGINT to self to trigger filtered restart logic in main
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGINT)
		return m, nil

	case "0":
		if m.currentFilter == 9 {
			m.ballGame = nil
		}
		m.currentFilter = 0
		m.scrollOffset = 0
		if m.filterChanged != nil {
			m.filterChanged(0)
		}
		return m, nil

	case "1", "2", "3", "4", "5", "6", "7", "8":
		idx := int(msg.String()[0] - '0')
		if idx <= len(m.services) {
			if m.currentFilter == 9 {
				m.ballGame = nil
			}
			m.currentFilter = idx
			m.scrollOffset = 0
			if m.filterChanged != nil {
				m.filterChanged(idx)
			}
		}
		return m, nil

	case "9":
		// Secret ball game mode!
		if m.currentFilter == 9 {
			// Already in ball mode, go back to All
			m.currentFilter = 0
			m.ballGame = nil
			m.scrollOffset = 0
			if m.filterChanged != nil {
				m.filterChanged(0)
			}
			return m, nil
		}
		m.currentFilter = 9
		gameHeight := m.height - 4
		if gameHeight < 1 {
			gameHeight = 1
		}
		m.ballGame = NewBallGame(m.width, gameHeight)
		return m, Tick()

	case "k", "K", "up":
		m.scrollUp(1)
		return m, nil

	case "j", "J", "down":
		m.scrollDown(1)
		return m, nil

	case "u", "U":
		m.scrollUp(10)
		return m, nil

	case "d", "D":
		m.scrollDown(10)
		return m, nil

	case "g":
		// Go to top (oldest)
		buf := m.currentBuffer()
		if buf != nil {
			viewHeight := m.viewportHeight()
			maxOffset := buf.Len() - viewHeight
			if maxOffset < 0 {
				maxOffset = 0
			}
			m.scrollOffset = maxOffset
		}
		return m, nil

	case "G":
		// Go to bottom (newest)
		m.scrollOffset = 0
		return m, nil
	}

	return m, nil
}

// appendLog adds a log message to the appropriate buffers.
func (m *Model) appendLog(msg LogMsg) {
	// Track if we need to pin scroll position
	wasScrolled := m.scrollOffset > 0
	currentBuf := m.currentBuffer()
	oldLen := 0
	if currentBuf != nil {
		oldLen = currentBuf.Len()
	}

	// Add to service-specific buffer
	if buf, ok := m.logBuffers[msg.Service]; ok {
		buf.Append(msg.Text)
	}

	// Add to combined "All" buffer with prefix
	idx, ok := m.serviceIndex[msg.Service]
	if !ok {
		idx = 0
	}
	prefix := m.formatServicePrefix(msg.Service, idx)
	m.allLogs.Append(prefix + msg.Text)

	// If user was scrolled up, keep them pinned to the same position
	// by incrementing scroll offset when new logs arrive in current view
	if wasScrolled && currentBuf != nil {
		newLen := currentBuf.Len()
		if newLen > oldLen {
			m.scrollOffset += (newLen - oldLen)
		}
	}
}

// currentBuffer returns the ring buffer for the current filter.
func (m Model) currentBuffer() *RingBuffer {
	if m.currentFilter == 0 {
		return m.allLogs
	}
	if m.currentFilter-1 < len(m.services) {
		return m.logBuffers[m.services[m.currentFilter-1]]
	}
	return nil
}

// viewportHeight returns the height available for log lines.
func (m Model) viewportHeight() int {
	// Header (1) + separator (1) + newline (1) + help bar (1) = 4 lines reserved
	h := m.height - 4
	if h < 1 {
		h = 1
	}
	return h
}

// scrollUp scrolls up (towards older logs).
func (m *Model) scrollUp(lines int) {
	buf := m.currentBuffer()
	if buf == nil {
		return
	}

	viewHeight := m.viewportHeight()
	maxOffset := buf.Len() - viewHeight
	if maxOffset < 0 {
		maxOffset = 0
	}

	m.scrollOffset += lines
	if m.scrollOffset > maxOffset {
		m.scrollOffset = maxOffset
	}
}

// scrollDown scrolls down (towards newer logs).
func (m *Model) scrollDown(lines int) {
	m.scrollOffset -= lines
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
}

// View renders the TUI.
func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	// Secret ball game mode
	if m.currentFilter == 9 && m.ballGame != nil {
		return m.renderBallGame()
	}

	var b strings.Builder

	// 1. Header with tabs
	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	// 2. Log viewport (fixed height)
	b.WriteString(m.renderLogs())

	// 3. Help/status bar
	b.WriteString(m.renderHelpBar())

	return b.String()
}

// renderHeader renders the clickable tab header.
func (m Model) renderHeader() string {
	var b strings.Builder

	// [0] All tab
	allLabel := "[0] All"
	var allTab string
	if m.currentFilter == 0 {
		allTab = activeTabStyle.Render(allLabel)
	} else {
		allTab = inactiveTabStyle.Foreground(lipgloss.Color("255")).Render(allLabel)
	}
	b.WriteString(allTab)
	b.WriteString(" ")

	// Service tabs
	for i, svc := range m.services {
		if i >= 9 {
			break
		}
		label := fmt.Sprintf("[%d] %s", i+1, svc)
		var tab string
		if m.currentFilter == i+1 {
			tab = activeTabStyle.Render(label)
		} else {
			style := inactiveTabStyle.Foreground(tabColors[i%len(tabColors)])
			tab = style.Render(label)
		}
		b.WriteString(tab)
		b.WriteString(" ")
	}

	// Add separator line
	header := b.String()
	padding := m.width - lipgloss.Width(header)
	if padding > 0 {
		header += strings.Repeat(" ", padding)
	}
	return header + "\n" + strings.Repeat("─", m.width)
}

// renderLogs renders the visible log lines.
func (m Model) renderLogs() string {
	buf := m.currentBuffer()
	viewHeight := m.viewportHeight()

	if buf == nil || buf.Len() == 0 {
		// Return empty space to maintain layout
		return strings.Repeat("\n", viewHeight)
	}

	totalLines := buf.Len()

	// Calculate visible window
	endIdx := totalLines - m.scrollOffset
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

	lines := buf.Slice(startIdx, endIdx)

	var b strings.Builder
	for _, line := range lines {
		// Truncate long lines to terminal width
		if lipgloss.Width(line) > m.width {
			line = line[:m.width-3] + "..."
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	// Pad remaining lines to fill viewport
	for i := len(lines); i < viewHeight; i++ {
		b.WriteString("\n")
	}

	return b.String()
}

// renderHelpBar renders the bottom help/status bar.
func (m Model) renderHelpBar() string {
	buf := m.currentBuffer()
	totalLines := 0
	if buf != nil {
		totalLines = buf.Len()
	}

	var status string
	if m.scrollOffset > 0 {
		status = scrollIndicatorStyle.Render(fmt.Sprintf(" ↑ %d more lines ", m.scrollOffset))
	} else {
		status = ""
	}

	help := helpStyle.Render("0-9:filter • j/k:scroll • g/G:top/bottom • Shift+drag:copy • Ctrl+C:restart")

	// Right-align status, left-align help
	if status != "" {
		gap := m.width - lipgloss.Width(help) - lipgloss.Width(status)
		if gap < 1 {
			gap = 1
		}
		return help + strings.Repeat(" ", gap) + status
	}

	// Show line count on the right
	lineCount := helpStyle.Render(fmt.Sprintf("%d lines", totalLines))
	gap := m.width - lipgloss.Width(help) - lipgloss.Width(lineCount)
	if gap < 1 {
		gap = 1
	}
	return help + strings.Repeat(" ", gap) + lineCount
}

// formatServicePrefix formats a service name with color for log output.
func (m Model) formatServicePrefix(service string, index int) string {
	style := lipgloss.NewStyle().Foreground(tabColors[index%len(tabColors)])
	return style.Render(fmt.Sprintf("[%s]", service)) + " "
}

// renderBallGame renders the secret ball game.
func (m Model) renderBallGame() string {
	var b strings.Builder

	// Header with score
	scoreStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("208")).
		Background(lipgloss.Color("236")).
		Padding(0, 1)
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("212")).
		Background(lipgloss.Color("236")).
		Padding(0, 1)

	score := scoreStyle.Render(fmt.Sprintf("🏀 %d", m.ballGame.Score()))
	title := titleStyle.Render("● BASKETBALL ●")

	// Score on left, title centered
	titlePadding := (m.width - lipgloss.Width(title)) / 2
	scorePadding := titlePadding - lipgloss.Width(score)
	if scorePadding < 0 {
		scorePadding = 0
	}

	b.WriteString(score)
	b.WriteString(strings.Repeat(" ", scorePadding))
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", m.width))
	b.WriteString("\n")

	// Ball game area
	gameHeight := m.height - 4 // header + separator + help
	if gameHeight < 1 {
		gameHeight = 1
	}
	b.WriteString(m.ballGame.Render(m.width, gameHeight))

	// Help bar
	help := helpStyle.Render("drag+throw ball into hoop • 9 or 0:back to logs")
	b.WriteString(help)

	return b.String()
}

// CurrentFilter returns the current filter index (for external access).
func (m Model) CurrentFilter() int {
	return m.currentFilter
}

// CurrentFilterService returns the name of the currently filtered service.
// Returns empty string for "All" view.
func (m Model) CurrentFilterService() string {
	if m.currentFilter == 0 || m.currentFilter > len(m.services) {
		return ""
	}
	return m.services[m.currentFilter-1]
}

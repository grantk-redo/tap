package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pjtatlow/tap/internal/logstore"
)

// RestartRequest represents a request to restart services
type RestartRequest struct {
	Services []string // Empty means restart all
}

type Server struct {
	store     *logstore.Store
	mux       *http.ServeMux
	startTime time.Time
	restartCh chan<- RestartRequest // nil if restart not supported
}

// ServerOption configures a Server
type ServerOption func(*Server)

// WithRestartChannel configures the server to send restart requests to the given channel
func WithRestartChannel(ch chan<- RestartRequest) ServerOption {
	return func(s *Server) {
		s.restartCh = ch
	}
}

func NewServer(store *logstore.Store, opts ...ServerOption) *Server {
	s := &Server{
		store:     store,
		mux:       http.NewServeMux(),
		startTime: time.Now(),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.setupRoutes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) setupRoutes() {
	// Streamable HTTP transport (newer) - POST to root
	s.mux.HandleFunc("/", s.handleStreamableHTTP)
	// SSE transport (older) - connect to /sse, POST to /message
	s.mux.HandleFunc("/sse", s.handleSSE)
	s.mux.HandleFunc("/message", s.handleMessage)
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/logs", s.handleLogs)
}

type HealthResponse struct {
	Status        string   `json:"status"`
	UptimeSeconds int64    `json:"uptime_seconds"`
	LogCount      int      `json:"log_count"`
	Services      []string `json:"services"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	stats := s.store.Stats()
	services := s.store.Services()

	resp := HealthResponse{
		Status:        "ok",
		UptimeSeconds: int64(time.Since(s.startTime).Seconds()),
		LogCount:      stats.Total,
		Services:      services,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// handleLogs returns recent logs as plain text or JSON.
// Query params: lines (default 100), service, level, stream, format (text|json)
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// Parse lines param
	lines := 100
	if n := q.Get("lines"); n != "" {
		if parsed, err := strconv.Atoi(n); err == nil && parsed > 0 {
			lines = parsed
		}
	}

	// Build filter options
	opts := logstore.FilterOptions{}
	if st := q.Get("stream"); st != "" {
		opts.Stream = st
	}
	if svc := q.Get("service"); svc != "" {
		opts.Service = svc
	}
	if lvl := q.Get("level"); lvl != "" {
		opts.Level = logstore.LogLevel(lvl)
	}
	if since := q.Get("since"); since != "" {
		if d, err := parseDuration(since); err == nil {
			opts.Since = time.Now().Add(-d)
		}
	}

	entries := s.store.Tail(lines, opts)

	// Output format
	format := q.Get("format")
	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(entries)
		return
	}

	// Default: plain text
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if len(entries) == 0 {
		_, _ = w.Write([]byte("No log entries\n"))
		return
	}
	for _, e := range entries {
		line := formatEntry(e)
		_, _ = w.Write([]byte(line + "\n"))
	}
}

// handleStreamableHTTP implements the MCP Streamable HTTP transport.
// This is the newer transport where clients POST JSON-RPC directly to /.
func (s *Server) handleStreamableHTTP(w http.ResponseWriter, r *http.Request) {
	// Only handle POST requests - other methods get 404
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	// Handle JSON-RPC request same as /message
	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, nil, -32700, "Parse error")
		return
	}

	switch req.Method {
	case "initialize":
		s.handleInitialize(w, &req)
	case "initialized":
		// Client notification that initialization is complete - just acknowledge
		writeResult(w, req.ID, map[string]any{})
	case "tools/list":
		s.handleToolsList(w, &req)
	case "tools/call":
		s.handleToolsCall(w, &req)
	default:
		writeError(w, req.ID, -32601, "Method not found")
	}
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		sessionID = "default"
	}

	// Send endpoint event per MCP spec
	endpoint := fmt.Sprintf("http://%s/message?session_id=%s", r.Host, sessionID)
	_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpoint)
	flusher.Flush()

	// Keep connection alive
	<-r.Context().Done()
}

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, nil, -32700, "Parse error")
		return
	}

	switch req.Method {
	case "initialize":
		s.handleInitialize(w, &req)
	case "tools/list":
		s.handleToolsList(w, &req)
	case "tools/call":
		s.handleToolsCall(w, &req)
	default:
		writeError(w, req.ID, -32601, "Method not found")
	}
}

func (s *Server) handleInitialize(w http.ResponseWriter, req *JSONRPCRequest) {
	resp := InitializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities: Capabilities{
			Tools: &ToolsCapability{},
		},
		ServerInfo: ServerInfo{
			Name:    "tap",
			Version: "0.1.0",
		},
	}
	writeResult(w, req.ID, resp)
}

func (s *Server) handleToolsList(w http.ResponseWriter, req *JSONRPCRequest) {
	tools := []Tool{
		{
			Name:        "list_services",
			Description: "List all service names detected in logs. Call this FIRST to discover available services before filtering with tail_logs or search_logs. Returns service names extracted via the --service-regex flag.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "tail_logs",
			Description: "Get the most recent log lines. Use list_services first to get valid service names for filtering. Returns logs with timestamp, stream, service, level, and text.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"lines": map[string]any{
						"type":        "integer",
						"description": "Number of lines to return (default: 50)",
					},
					"since": map[string]any{
						"type":        "string",
						"description": "Only show logs from this duration ago. Examples: '30s', '5m', '1h'",
					},
					"until": map[string]any{
						"type":        "string",
						"description": "Only show logs until this duration ago. Examples: '30s', '5m', '1h'. Can be combined with 'since' for a time window.",
					},
					"stream": map[string]any{
						"type":        "string",
						"enum":        []string{"stdout", "stderr"},
						"description": "Filter by stream (stdout or stderr)",
					},
					"service": map[string]any{
						"type":        "string",
						"description": "Filter by service name. Call list_services to see available names.",
					},
					"level": map[string]any{
						"type":        "string",
						"enum":        []string{"trace", "debug", "info", "warn", "error", "fatal"},
						"description": "Filter by log level",
					},
				},
			},
		},
		{
			Name:        "search_logs",
			Description: "Full-text search across logs. Supports queries like 'error timeout', '+Level:error +Service:api', or 'Text:connection'. Use list_services first to get valid service names for filtering.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query. Examples: 'timeout', 'Level:error', 'Service:api AND failed'",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum results to return (default: 100)",
					},
					"since": map[string]any{
						"type":        "string",
						"description": "Only search logs from this duration ago. Examples: '30s', '5m', '1h'",
					},
					"until": map[string]any{
						"type":        "string",
						"description": "Only search logs until this duration ago. Examples: '30s', '5m', '1h'. Can be combined with 'since' for a time window.",
					},
					"stream": map[string]any{
						"type":        "string",
						"enum":        []string{"stdout", "stderr"},
						"description": "Filter by stream",
					},
					"service": map[string]any{
						"type":        "string",
						"description": "Filter by service name. Call list_services to see available names.",
					},
					"level": map[string]any{
						"type":        "string",
						"enum":        []string{"trace", "debug", "info", "warn", "error", "fatal"},
						"description": "Filter by log level",
					},
					"before": map[string]any{
						"type":        "integer",
						"description": "Number of lines to show before each match (like grep -B)",
					},
					"after": map[string]any{
						"type":        "integer",
						"description": "Number of lines to show after each match (like grep -A)",
					},
					"context": map[string]any{
						"type":        "integer",
						"description": "Number of lines to show before and after each match (like grep -C). Shorthand for setting both before and after.",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "clear_logs",
			Description: "Clear all stored logs and reset the search index.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "log_stats",
			Description: "Get statistics about stored logs including total count, counts by level/service/stream, and time range.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "watch_logs",
			Description: "Wait for new log entries in real-time. Blocks until a matching log arrives or timeout is reached. Useful for waiting for specific events like errors or service startup messages.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"timeout": map[string]any{
						"type":        "string",
						"description": "How long to wait for new logs. Examples: '30s', '5m', '1h'. Default: '30s'",
					},
					"stream": map[string]any{
						"type":        "string",
						"enum":        []string{"stdout", "stderr"},
						"description": "Filter by stream (stdout or stderr)",
					},
					"service": map[string]any{
						"type":        "string",
						"description": "Filter by service name. Call list_services to see available names.",
					},
					"level": map[string]any{
						"type":        "string",
						"enum":        []string{"trace", "debug", "info", "warn", "error", "fatal"},
						"description": "Filter by log level",
					},
				},
			},
		},
		{
			Name:        "restart_services",
			Description: "Restart the managed processes. Sends SIGINT and restarts all processes.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
	writeResult(w, req.ID, map[string]any{"tools": tools})
}

func (s *Server) handleToolsCall(w http.ResponseWriter, req *JSONRPCRequest) {
	var params ToolCallParams
	paramsBytes, _ := json.Marshal(req.Params)
	if err := json.Unmarshal(paramsBytes, &params); err != nil {
		writeError(w, req.ID, -32602, "Invalid params")
		return
	}

	switch params.Name {
	case "tail_logs":
		s.handleTailLogs(w, req, params.Arguments)
	case "search_logs":
		s.handleSearchLogs(w, req, params.Arguments)
	case "list_services":
		s.handleListServices(w, req)
	case "clear_logs":
		s.handleClearLogs(w, req)
	case "log_stats":
		s.handleLogStats(w, req)
	case "watch_logs":
		s.handleWatchLogs(w, req, params.Arguments)
	case "restart_services":
		s.handleRestartServices(w, req)
	default:
		writeError(w, req.ID, -32602, "Unknown tool")
	}
}

func parseFilterOptions(args map[string]any) logstore.FilterOptions {
	opts := logstore.FilterOptions{}
	if st, ok := args["stream"].(string); ok {
		opts.Stream = st
	}
	if svc, ok := args["service"].(string); ok {
		opts.Service = svc
	}
	if lvl, ok := args["level"].(string); ok {
		opts.Level = logstore.LogLevel(lvl)
	}
	if since, ok := args["since"].(string); ok {
		if d, err := parseDuration(since); err == nil {
			opts.Since = time.Now().Add(-d)
		}
	}
	if until, ok := args["until"].(string); ok {
		if d, err := parseDuration(until); err == nil {
			opts.Until = time.Now().Add(-d)
		}
	}
	return opts
}

// parseDuration parses duration strings like "30s", "5m", "1h", "2h30m"
// Supports: s (seconds), m (minutes), h (hours), d (days)
var durationRegex = regexp.MustCompile(`^(\d+)([smhd])$`)

func parseDuration(s string) (time.Duration, error) {
	// Try standard Go duration first (handles "1h30m", "2h", "30s", etc.)
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}

	// Handle days specially since Go doesn't support "d"
	matches := durationRegex.FindStringSubmatch(s)
	if matches == nil {
		return 0, fmt.Errorf("invalid duration: %s", s)
	}

	value, _ := strconv.Atoi(matches[1])
	unit := matches[2]

	switch unit {
	case "s":
		return time.Duration(value) * time.Second, nil
	case "m":
		return time.Duration(value) * time.Minute, nil
	case "h":
		return time.Duration(value) * time.Hour, nil
	case "d":
		return time.Duration(value) * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid duration unit: %s", unit)
	}
}

func (s *Server) handleTailLogs(w http.ResponseWriter, req *JSONRPCRequest, args map[string]any) {
	lines := 50
	if n, ok := args["lines"].(float64); ok {
		lines = int(n)
	}
	opts := parseFilterOptions(args)

	entries := s.store.Tail(lines, opts)
	text := formatEntries(entries)
	writeResult(w, req.ID, ToolResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
	})
}

func (s *Server) handleSearchLogs(w http.ResponseWriter, req *JSONRPCRequest, args map[string]any) {
	query, _ := args["query"].(string)
	if query == "" {
		writeError(w, req.ID, -32602, "query is required")
		return
	}

	limit := 100
	if n, ok := args["limit"].(float64); ok {
		limit = int(n)
	}
	opts := parseFilterOptions(args)

	// Parse context parameters
	// context sets both before and after, but explicit before/after override
	before, after := 0, 0
	if c, ok := args["context"].(float64); ok {
		before = int(c)
		after = int(c)
	}
	if _, ok := args["before"]; ok {
		if b, ok := args["before"].(float64); ok {
			before = int(b)
		}
	}
	if _, ok := args["after"]; ok {
		if a, ok := args["after"].(float64); ok {
			after = int(a)
		}
	}

	entries, err := s.store.Search(query, limit, opts)
	if err != nil {
		writeError(w, req.ID, -32000, err.Error())
		return
	}

	var text string
	if before > 0 || after > 0 {
		text = s.formatEntriesWithContext(entries, before, after)
	} else {
		text = formatEntries(entries)
	}
	writeResult(w, req.ID, ToolResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
	})
}

func (s *Server) handleListServices(w http.ResponseWriter, req *JSONRPCRequest) {
	services := s.store.Services()
	var text string
	if len(services) == 0 {
		text = "No services detected. Make sure --service-regex is configured and logs contain matching patterns."
	} else {
		text = "Detected services:\n" + strings.Join(services, "\n")
	}
	writeResult(w, req.ID, ToolResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
	})
}

func (s *Server) handleClearLogs(w http.ResponseWriter, req *JSONRPCRequest) {
	s.store.Clear()
	writeResult(w, req.ID, ToolResult{
		Content: []ContentBlock{{Type: "text", Text: "Logs cleared successfully"}},
	})
}

func (s *Server) handleLogStats(w http.ResponseWriter, req *JSONRPCRequest) {
	stats := s.store.Stats()
	text := formatStats(stats)
	writeResult(w, req.ID, ToolResult{
		Content: []ContentBlock{{Type: "text", Text: text}},
	})
}

func (s *Server) handleWatchLogs(w http.ResponseWriter, req *JSONRPCRequest, args map[string]any) {
	// Parse timeout (default 30s)
	timeout := 30 * time.Second
	if t, ok := args["timeout"].(string); ok && t != "" {
		if d, err := parseDuration(t); err == nil {
			timeout = d
		}
	}

	// Parse filter options
	var streamFilter string
	var serviceFilter string
	var levelFilter logstore.LogLevel
	if st, ok := args["stream"].(string); ok {
		streamFilter = st
	}
	if svc, ok := args["service"].(string); ok {
		serviceFilter = svc
	}
	if lvl, ok := args["level"].(string); ok {
		levelFilter = logstore.LogLevel(lvl)
	}

	// Subscribe to new log entries
	ch := make(chan logstore.LogEntry, 100)
	unsubscribe := s.store.Subscribe(ch)
	defer unsubscribe()

	// Collect matching entries
	var matched []logstore.LogEntry
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case entry := <-ch:
			// Apply filters
			if streamFilter != "" && entry.Stream != streamFilter {
				continue
			}
			if serviceFilter != "" && entry.Service != serviceFilter {
				continue
			}
			if levelFilter != "" && entry.Level != levelFilter {
				continue
			}
			matched = append(matched, entry)
		case <-timer.C:
			// Timeout reached
			var text string
			if len(matched) == 0 {
				text = fmt.Sprintf("No matching logs received within %s", timeout)
			} else {
				text = fmt.Sprintf("Received %d matching log(s) within %s:\n%s", len(matched), timeout, formatEntries(matched))
			}
			writeResult(w, req.ID, ToolResult{
				Content: []ContentBlock{{Type: "text", Text: text}},
			})
			return
		}
	}
}

func (s *Server) handleRestartServices(w http.ResponseWriter, req *JSONRPCRequest) {
	if s.restartCh == nil {
		writeError(w, req.ID, -32000, "Restart not supported in this mode")
		return
	}

	select {
	case s.restartCh <- RestartRequest{}:
		writeResult(w, req.ID, ToolResult{
			Content: []ContentBlock{{Type: "text", Text: "Restart requested for all services"}},
		})
	default:
		writeResult(w, req.ID, ToolResult{
			Content: []ContentBlock{{Type: "text", Text: "Restart already pending"}},
		})
	}
}

func formatStats(stats logstore.Stats) string {
	if stats.Total == 0 {
		return "No log entries"
	}

	var sb strings.Builder

	// Total and time range
	sb.WriteString(fmt.Sprintf("Total: %d entries\n", stats.Total))
	sb.WriteString(fmt.Sprintf("Earliest: %s\n", stats.EarliestTime.Format("2006-01-02 15:04:05")))
	sb.WriteString(fmt.Sprintf("Latest: %s\n", stats.LatestTime.Format("2006-01-02 15:04:05")))

	// By Level
	sb.WriteString("\nBy Level:\n")
	levelOrder := []logstore.LogLevel{
		logstore.LevelTrace,
		logstore.LevelDebug,
		logstore.LevelInfo,
		logstore.LevelWarn,
		logstore.LevelError,
		logstore.LevelFatal,
		logstore.LevelUnknown,
	}
	for _, level := range levelOrder {
		if count, ok := stats.ByLevel[level]; ok && count > 0 {
			levelName := string(level)
			if levelName == "" {
				levelName = "unknown"
			}
			sb.WriteString(fmt.Sprintf("  %s: %d\n", levelName, count))
		}
	}

	// By Service
	if len(stats.ByService) > 0 {
		sb.WriteString("\nBy Service:\n")
		for service, count := range stats.ByService {
			sb.WriteString(fmt.Sprintf("  %s: %d\n", service, count))
		}
	}

	// By Stream
	sb.WriteString("\nBy Stream:\n")
	for stream, count := range stats.ByStream {
		sb.WriteString(fmt.Sprintf("  %s: %d\n", stream, count))
	}

	return sb.String()
}

func formatEntries(entries []logstore.LogEntry) string {
	if len(entries) == 0 {
		return "No log entries found"
	}
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("[%s] [%s]",
			e.Timestamp.Format("15:04:05.000"),
			e.Stream,
		))
		if e.Service != "" {
			sb.WriteString(fmt.Sprintf(" [%s]", e.Service))
		}
		if e.Level != "" {
			sb.WriteString(fmt.Sprintf(" [%s]", strings.ToUpper(string(e.Level))))
		}
		sb.WriteString(fmt.Sprintf(" %s\n", e.Message))
	}
	return sb.String()
}

func formatEntry(e logstore.LogEntry) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[%s] [%s]",
		e.Timestamp.Format("15:04:05.000"),
		e.Stream,
	))
	if e.Service != "" {
		sb.WriteString(fmt.Sprintf(" [%s]", e.Service))
	}
	if e.Level != "" {
		sb.WriteString(fmt.Sprintf(" [%s]", strings.ToUpper(string(e.Level))))
	}
	sb.WriteString(fmt.Sprintf(" %s", e.Message))
	return sb.String()
}

// formatEntriesWithContext formats search results with surrounding context lines.
// It deduplicates overlapping context by merging groups that would overlap.
func (s *Server) formatEntriesWithContext(matches []logstore.LogEntry, before, after int) string {
	if len(matches) == 0 {
		return "No log entries found"
	}

	// Build a set of match IDs for marking which lines are matches
	matchIDs := make(map[uint64]bool)
	for _, m := range matches {
		matchIDs[m.ID] = true
	}

	// Collect context for each match and merge overlapping groups
	type contextGroup struct {
		entries  []logstore.LogEntry
		matchIDs map[uint64]bool // IDs within this group that are matches
	}

	var groups []contextGroup

	for _, match := range matches {
		ctx := s.store.GetContext(match.ID, before, after)
		if len(ctx) == 0 {
			continue
		}

		// Check if this context overlaps with the last group
		if len(groups) > 0 {
			lastGroup := &groups[len(groups)-1]
			lastEntry := lastGroup.entries[len(lastGroup.entries)-1]

			// Check for overlap: if the first entry of new context has ID <= last entry ID + 1
			if ctx[0].ID <= lastEntry.ID+1 {
				// Merge: add only entries that come after the last entry in the group
				for _, e := range ctx {
					if e.ID > lastEntry.ID {
						lastGroup.entries = append(lastGroup.entries, e)
					}
				}
				lastGroup.matchIDs[match.ID] = true
				continue
			}
		}

		// No overlap, create new group
		groupMatchIDs := make(map[uint64]bool)
		groupMatchIDs[match.ID] = true
		groups = append(groups, contextGroup{
			entries:  ctx,
			matchIDs: groupMatchIDs,
		})
	}

	// Format output
	var sb strings.Builder
	for i, group := range groups {
		if i > 0 {
			sb.WriteString("--\n")
		}
		for _, e := range group.entries {
			line := formatEntry(e)
			if matchIDs[e.ID] {
				sb.WriteString(line + "  <-- match\n")
			} else {
				sb.WriteString(line + "\n")
			}
		}
	}

	return sb.String()
}

func writeResult(w http.ResponseWriter, id any, result any) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeError(w http.ResponseWriter, id any, code int, message string) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

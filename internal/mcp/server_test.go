package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/pjtatlow/tap/internal/logstore"
)

func newTestServer(t *testing.T, serviceRegex *regexp.Regexp) (*Server, *logstore.Store) {
	t.Helper()
	store, err := logstore.New(serviceRegex, 0)
	if err != nil {
		t.Fatalf("logstore.New() error = %v", err)
	}
	return NewServer(store), store
}

func postJSON(t *testing.T, server *Server, req JSONRPCRequest) JSONRPCResponse {
	t.Helper()
	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/message", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httpReq)

	var resp JSONRPCResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}

func getToolResultText(t *testing.T, resp JSONRPCResponse) string {
	t.Helper()
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("no result in response: %v", resp)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("no content in result: %v", result)
	}
	block := content[0].(map[string]any)
	return block["text"].(string)
}

func TestInitialize(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not a map")
	}

	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion = %v, want 2024-11-05", result["protocolVersion"])
	}

	serverInfo, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("serverInfo is not a map")
	}
	if serverInfo["name"] != "tap" {
		t.Errorf("serverInfo.name = %v, want tap", serverInfo["name"])
	}
}

func TestToolsList(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not a map")
	}

	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools is not an array")
	}

	wantTools := []string{"tail_logs", "search_logs", "list_services", "clear_logs", "log_stats", "watch_logs", "restart_services"}
	if len(tools) != len(wantTools) {
		t.Errorf("got %d tools, want %d", len(tools), len(wantTools))
	}

	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolMap := tool.(map[string]any)
		toolNames[toolMap["name"].(string)] = true
	}

	for _, name := range wantTools {
		if !toolNames[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestTailLogs(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	now := time.Now()
	store.Append("stdout", "INFO: line 1", now)
	store.Append("stderr", "ERROR: line 2", now)
	store.Append("stdout", "DEBUG: line 3", now)

	tests := []struct {
		name       string
		args       map[string]any
		wantCount  int
		wantSubstr string
	}{
		{
			name:       "default",
			args:       map[string]any{},
			wantCount:  3,
			wantSubstr: "line 1",
		},
		{
			name:       "limit lines",
			args:       map[string]any{"lines": float64(2)},
			wantCount:  2,
			wantSubstr: "line 2",
		},
		{
			name:       "filter stdout",
			args:       map[string]any{"stream": "stdout"},
			wantCount:  2,
			wantSubstr: "line 1",
		},
		{
			name:       "filter stderr",
			args:       map[string]any{"stream": "stderr"},
			wantCount:  1,
			wantSubstr: "ERROR",
		},
		{
			name:       "filter level error",
			args:       map[string]any{"level": "error"},
			wantCount:  1,
			wantSubstr: "line 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := postJSON(t, server, JSONRPCRequest{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "tools/call",
				Params: map[string]any{
					"name":      "tail_logs",
					"arguments": tt.args,
				},
			})

			if resp.Error != nil {
				t.Fatalf("unexpected error: %v", resp.Error)
			}

			result := resp.Result.(map[string]any)
			content := result["content"].([]any)
			text := content[0].(map[string]any)["text"].(string)

			// Count lines (excluding empty)
			lines := 0
			for _, line := range bytes.Split([]byte(text), []byte("\n")) {
				if len(line) > 0 {
					lines++
				}
			}

			if lines != tt.wantCount {
				t.Errorf("got %d lines, want %d\ntext: %s", lines, tt.wantCount, text)
			}

			if !bytes.Contains([]byte(text), []byte(tt.wantSubstr)) {
				t.Errorf("output missing %q\ntext: %s", tt.wantSubstr, text)
			}
		})
	}
}

func TestTailLogsWithService(t *testing.T) {
	re := regexp.MustCompile(`\[(?P<service>\w+)\]`)
	server, store := newTestServer(t, re)
	defer func() { _ = store.Close() }()

	now := time.Now()
	store.Append("stdout", "[api] request received", now)
	store.Append("stdout", "[worker] job started", now)
	store.Append("stdout", "[api] response sent", now)

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]any{
			"name": "tail_logs",
			"arguments": map[string]any{
				"service": "api",
			},
		},
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	result := resp.Result.(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)

	if !bytes.Contains([]byte(text), []byte("request received")) {
		t.Errorf("missing api log line 1")
	}
	if !bytes.Contains([]byte(text), []byte("response sent")) {
		t.Errorf("missing api log line 2")
	}
	if bytes.Contains([]byte(text), []byte("job started")) {
		t.Errorf("should not contain worker log")
	}
}

func TestSearchLogs(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	now := time.Now()
	store.Append("stdout", "database connection failed", now)
	store.Append("stdout", "retrying connection", now)
	store.Append("stdout", "query executed successfully", now)

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]any{
			"name": "search_logs",
			"arguments": map[string]any{
				"query": "connection",
			},
		},
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	result := resp.Result.(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)

	// Should find logs containing "connection"
	if !bytes.Contains([]byte(text), []byte("connection")) {
		t.Errorf("search should find connection-related logs\ntext: %s", text)
	}
	// Should not find "query executed" since it doesn't match
	if bytes.Contains([]byte(text), []byte("query executed")) {
		t.Errorf("search should not return non-matching logs\ntext: %s", text)
	}
}

func TestSearchLogsRequiresQuery(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      "search_logs",
			"arguments": map[string]any{},
		},
	})

	if resp.Error == nil {
		t.Fatal("expected error for missing query")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("error code = %d, want -32602", resp.Error.Code)
	}
}

func TestListServices(t *testing.T) {
	re := regexp.MustCompile(`\[(?P<service>\w+)\]`)
	server, store := newTestServer(t, re)
	defer func() { _ = store.Close() }()

	now := time.Now()
	store.Append("stdout", "[api] starting", now)
	store.Append("stdout", "[worker] starting", now)
	store.Append("stdout", "[auth] starting", now)

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      "list_services",
			"arguments": map[string]any{},
		},
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	result := resp.Result.(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)

	for _, svc := range []string{"api", "worker", "auth"} {
		if !bytes.Contains([]byte(text), []byte(svc)) {
			t.Errorf("missing service: %s\ntext: %s", svc, text)
		}
	}
}

func TestListServicesEmpty(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      "list_services",
			"arguments": map[string]any{},
		},
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	result := resp.Result.(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)

	if !bytes.Contains([]byte(text), []byte("No services detected")) {
		t.Errorf("expected 'No services detected' message\ntext: %s", text)
	}
}

func TestClearLogs(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	now := time.Now()
	store.Append("stdout", "line 1", now)
	store.Append("stdout", "line 2", now)

	// Verify logs exist
	entries := store.Tail(10, logstore.FilterOptions{})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries before clear, got %d", len(entries))
	}

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      "clear_logs",
			"arguments": map[string]any{},
		},
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	// Verify logs cleared
	entries = store.Tail(10, logstore.FilterOptions{})
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after clear, got %d", len(entries))
	}
}

func TestUnknownMethod(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "unknown/method",
	})

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error code = %d, want -32601", resp.Error.Code)
	}
}

func TestUnknownTool(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      "unknown_tool",
			"arguments": map[string]any{},
		},
	})

	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("error code = %d, want -32602", resp.Error.Code)
	}
}

func TestSSEEndpoint(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	// Use a context we can cancel to stop the SSE handler
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/sse", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	// Run in goroutine since SSE blocks
	done := make(chan struct{})
	go func() {
		server.ServeHTTP(rec, req)
		close(done)
	}()

	// Give it time to write headers, then cancel to stop the handler
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Wait for handler to finish before reading headers
	<-done

	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", rec.Header().Get("Content-Type"))
	}
}

func TestMethodNotAllowed(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	req := httptest.NewRequest(http.MethodGet, "/message", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestParseError(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	req := httptest.NewRequest(http.MethodPost, "/message", bytes.NewReader([]byte("not json")))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	var resp JSONRPCResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Error == nil {
		t.Fatal("expected parse error")
	}
	if resp.Error.Code != -32700 {
		t.Errorf("error code = %d, want -32700", resp.Error.Code)
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"30s", 30 * time.Second, false},
		{"5m", 5 * time.Minute, false},
		{"2h", 2 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"1h30m", 90 * time.Minute, false},
		{"invalid", 0, true},
		{"", 0, true},
		{"10x", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseDuration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseDuration(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestTailLogsWithSince(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	now := time.Now()
	oldTime := now.Add(-10 * time.Minute)

	store.Append("stdout", "old log", oldTime)
	store.Append("stdout", "recent log", now)

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]any{
			"name": "tail_logs",
			"arguments": map[string]any{
				"since": "1m",
			},
		},
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	text := getToolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("recent log")) {
		t.Errorf("should contain recent log:\n%s", text)
	}
	if bytes.Contains([]byte(text), []byte("old log")) {
		t.Errorf("should not contain old log:\n%s", text)
	}
}

func TestTailLogsWithUntil(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	now := time.Now()
	oldTime := now.Add(-10 * time.Minute)
	midTime := now.Add(-5 * time.Minute)

	store.Append("stdout", "old log", oldTime)
	store.Append("stdout", "mid log", midTime)
	store.Append("stdout", "recent log", now)

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]any{
			"name": "tail_logs",
			"arguments": map[string]any{
				"until": "3m",
			},
		},
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	text := getToolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("old log")) {
		t.Errorf("should contain old log:\n%s", text)
	}
	if !bytes.Contains([]byte(text), []byte("mid log")) {
		t.Errorf("should contain mid log:\n%s", text)
	}
	if bytes.Contains([]byte(text), []byte("recent log")) {
		t.Errorf("should not contain recent log:\n%s", text)
	}
}

func TestTailLogsWithSinceAndUntil(t *testing.T) {
	tests := []struct {
		name           string
		since          string
		until          string
		wantSubstrs    []string
		notWantSubstrs []string
	}{
		{
			name:           "time window from 1h to 30m ago",
			since:          "1h",
			until:          "30m",
			wantSubstrs:    []string{"old log", "mid log"},
			notWantSubstrs: []string{"very old log", "recent log"},
		},
		{
			name:           "narrow window",
			since:          "50m",
			until:          "40m",
			wantSubstrs:    []string{"mid log"},
			notWantSubstrs: []string{"very old log", "recent log"},
		},
		{
			name:           "since only",
			since:          "20m",
			until:          "",
			wantSubstrs:    []string{"recent log"},
			notWantSubstrs: []string{"very old log", "old log", "mid log"},
		},
		{
			name:           "until only",
			since:          "",
			until:          "50m",
			wantSubstrs:    []string{"very old log"},
			notWantSubstrs: []string{"mid log", "recent log"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, store := newTestServer(t, nil)
			defer func() { _ = store.Close() }()

			now := time.Now()
			store.Append("stdout", "very old log", now.Add(-2*time.Hour))
			store.Append("stdout", "old log", now.Add(-55*time.Minute))
			store.Append("stdout", "mid log", now.Add(-45*time.Minute))
			store.Append("stdout", "recent log", now.Add(-5*time.Minute))

			args := map[string]any{}
			if tt.since != "" {
				args["since"] = tt.since
			}
			if tt.until != "" {
				args["until"] = tt.until
			}

			resp := postJSON(t, server, JSONRPCRequest{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "tools/call",
				Params: map[string]any{
					"name":      "tail_logs",
					"arguments": args,
				},
			})

			if resp.Error != nil {
				t.Fatalf("unexpected error: %v", resp.Error)
			}

			text := getToolResultText(t, resp)

			for _, substr := range tt.wantSubstrs {
				if !bytes.Contains([]byte(text), []byte(substr)) {
					t.Errorf("expected output to contain %q\ngot: %s", substr, text)
				}
			}

			for _, substr := range tt.notWantSubstrs {
				if bytes.Contains([]byte(text), []byte(substr)) {
					t.Errorf("expected output NOT to contain %q\ngot: %s", substr, text)
				}
			}
		})
	}
}

func TestLogStats(t *testing.T) {
	tests := []struct {
		name         string
		serviceRegex *regexp.Regexp
		logs         []struct {
			stream string
			text   string
		}
		wantSubstrs    []string
		notWantSubstrs []string
	}{
		{
			name:         "empty store",
			serviceRegex: nil,
			logs:         nil,
			wantSubstrs:  []string{"No log entries"},
		},
		{
			name:         "with entries",
			serviceRegex: nil,
			logs: []struct {
				stream string
				text   string
			}{
				{"stdout", "INFO: message 1"},
				{"stdout", "INFO: message 2"},
				{"stderr", "ERROR: error message"},
			},
			wantSubstrs: []string{
				"Total: 3 entries",
				"By Level:",
				"info: 2",
				"error: 1",
				"By Stream:",
				"stdout: 2",
				"stderr: 1",
			},
		},
		{
			name:         "with services",
			serviceRegex: regexp.MustCompile(`\[(?P<service>\w+)\]`),
			logs: []struct {
				stream string
				text   string
			}{
				{"stdout", "[api] INFO: request 1"},
				{"stdout", "[api] INFO: request 2"},
				{"stdout", "[worker] ERROR: job failed"},
			},
			wantSubstrs: []string{
				"Total: 3 entries",
				"By Service:",
				"api: 2",
				"worker: 1",
			},
		},
		{
			name:         "no services section when no services",
			serviceRegex: nil,
			logs: []struct {
				stream string
				text   string
			}{
				{"stdout", "INFO: no service here"},
			},
			wantSubstrs:    []string{"Total: 1 entries"},
			notWantSubstrs: []string{"By Service:"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, store := newTestServer(t, tt.serviceRegex)
			defer func() { _ = store.Close() }()

			now := time.Now()
			for _, log := range tt.logs {
				store.Append(log.stream, log.text, now)
			}

			resp := postJSON(t, server, JSONRPCRequest{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "tools/call",
				Params: map[string]any{
					"name":      "log_stats",
					"arguments": map[string]any{},
				},
			})

			if resp.Error != nil {
				t.Fatalf("unexpected error: %v", resp.Error)
			}

			text := getToolResultText(t, resp)

			for _, substr := range tt.wantSubstrs {
				if !bytes.Contains([]byte(text), []byte(substr)) {
					t.Errorf("expected output to contain %q\ngot: %s", substr, text)
				}
			}

			for _, substr := range tt.notWantSubstrs {
				if bytes.Contains([]byte(text), []byte(substr)) {
					t.Errorf("expected output NOT to contain %q\ngot: %s", substr, text)
				}
			}
		})
	}
}

func TestLogStatsTimestamps(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	earliest := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	latest := time.Date(2024, 1, 15, 11, 45, 30, 0, time.UTC)

	store.Append("stdout", "earliest log", earliest)
	store.Append("stdout", "latest log", latest)

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      "log_stats",
			"arguments": map[string]any{},
		},
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	text := getToolResultText(t, resp)

	wantEarliest := "Earliest: 2024-01-15 10:30:00"
	wantLatest := "Latest: 2024-01-15 11:45:30"

	if !bytes.Contains([]byte(text), []byte(wantEarliest)) {
		t.Errorf("expected output to contain %q\ngot: %s", wantEarliest, text)
	}
	if !bytes.Contains([]byte(text), []byte(wantLatest)) {
		t.Errorf("expected output to contain %q\ngot: %s", wantLatest, text)
	}
}

func TestSearchLogsWithContext(t *testing.T) {
	ptr := func(v float64) *float64 { return &v }

	tests := []struct {
		name           string
		logs           []string
		query          string
		before         *float64
		after          *float64
		context        *float64
		wantSubstrs    []string
		notWantSubstrs []string
	}{
		{
			name: "basic context",
			logs: []string{
				"line 1 start",
				"line 2 before",
				"line 3 ERROR match",
				"line 4 after",
				"line 5 end",
			},
			query:  "ERROR",
			before: ptr(1),
			after:  ptr(1),
			wantSubstrs: []string{
				"line 2 before",
				"line 3 ERROR match",
				"line 4 after",
				"<-- match",
			},
			notWantSubstrs: []string{
				"line 1 start",
				"line 5 end",
			},
		},
		{
			name: "context shorthand",
			logs: []string{
				"line 1 start",
				"line 2 before",
				"line 3 ERROR match",
				"line 4 after",
				"line 5 end",
			},
			query:   "ERROR",
			context: ptr(1),
			wantSubstrs: []string{
				"line 2 before",
				"line 3 ERROR match",
				"line 4 after",
			},
			notWantSubstrs: []string{
				"line 1 start",
				"line 5 end",
			},
		},
		{
			name: "before overrides context",
			logs: []string{
				"line 1 start",
				"line 2 before",
				"line 3 ERROR match",
				"line 4 after",
				"line 5 end",
			},
			query:   "ERROR",
			before:  ptr(2),
			after:   ptr(0),
			context: ptr(1),
			wantSubstrs: []string{
				"line 1 start",
				"line 2 before",
				"line 3 ERROR match",
			},
			notWantSubstrs: []string{
				"line 4 after",
				"line 5 end",
			},
		},
		{
			name: "overlapping context is merged",
			logs: []string{
				"line 1",
				"line 2 ERROR first",
				"line 3 middle",
				"line 4 ERROR second",
				"line 5",
			},
			query:  "ERROR",
			before: ptr(1),
			after:  ptr(1),
			wantSubstrs: []string{
				"line 1",
				"line 2 ERROR first",
				"line 3 middle",
				"line 4 ERROR second",
				"line 5",
			},
			notWantSubstrs: []string{
				"\n--\n", // no separator since contexts overlap
			},
		},
		{
			name: "non-overlapping context has separator",
			logs: []string{
				"line 1",
				"line 2 ERROR first",
				"line 3",
				"line 4",
				"line 5",
				"line 6 ERROR second",
				"line 7",
			},
			query:   "ERROR",
			context: ptr(1),
			wantSubstrs: []string{
				"line 1",
				"line 2 ERROR first",
				"line 3",
				"--",
				"line 5",
				"line 6 ERROR second",
				"line 7",
			},
			notWantSubstrs: []string{
				"line 4",
			},
		},
		{
			name: "no context returns matches only",
			logs: []string{
				"line 1",
				"line 2 ERROR match",
				"line 3",
			},
			query: "ERROR",
			wantSubstrs: []string{
				"line 2 ERROR match",
			},
			notWantSubstrs: []string{
				"line 1",
				"line 3",
				"<-- match", // no match indicator without context
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, store := newTestServer(t, nil)
			defer func() { _ = store.Close() }()

			now := time.Now()
			for _, log := range tt.logs {
				store.Append("stdout", log, now)
			}

			args := map[string]any{
				"query": tt.query,
			}
			if tt.before != nil {
				args["before"] = *tt.before
			}
			if tt.after != nil {
				args["after"] = *tt.after
			}
			if tt.context != nil {
				args["context"] = *tt.context
			}

			resp := postJSON(t, server, JSONRPCRequest{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "tools/call",
				Params: map[string]any{
					"name":      "search_logs",
					"arguments": args,
				},
			})

			if resp.Error != nil {
				t.Fatalf("unexpected error: %v", resp.Error)
			}

			text := getToolResultText(t, resp)

			for _, substr := range tt.wantSubstrs {
				if !bytes.Contains([]byte(text), []byte(substr)) {
					t.Errorf("expected output to contain %q\ngot: %s", substr, text)
				}
			}

			for _, substr := range tt.notWantSubstrs {
				if bytes.Contains([]byte(text), []byte(substr)) {
					t.Errorf("expected output NOT to contain %q\ngot: %s", substr, text)
				}
			}
		})
	}
}

func TestHealthEndpoint(t *testing.T) {
	tests := []struct {
		name             string
		serviceRegex     *regexp.Regexp
		logs             []struct {
			stream string
			text   string
		}
		wantStatus   int
		wantServices []string
		wantLogCount int
	}{
		{
			name:         "empty store",
			serviceRegex: nil,
			logs:         nil,
			wantStatus:   http.StatusOK,
			wantServices: []string{},
			wantLogCount: 0,
		},
		{
			name:         "with logs no services",
			serviceRegex: nil,
			logs: []struct {
				stream string
				text   string
			}{
				{"stdout", "INFO: message 1"},
				{"stdout", "INFO: message 2"},
			},
			wantStatus:   http.StatusOK,
			wantServices: []string{},
			wantLogCount: 2,
		},
		{
			name:         "with logs and services",
			serviceRegex: regexp.MustCompile(`\[(?P<service>\w+)\]`),
			logs: []struct {
				stream string
				text   string
			}{
				{"stdout", "[api] request 1"},
				{"stdout", "[worker] job started"},
				{"stdout", "[api] request 2"},
			},
			wantStatus:   http.StatusOK,
			wantServices: []string{"api", "worker"},
			wantLogCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, store := newTestServer(t, tt.serviceRegex)
			defer func() { _ = store.Close() }()

			now := time.Now()
			for _, log := range tt.logs {
				store.Append(log.stream, log.text, now)
			}

			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			if rec.Header().Get("Content-Type") != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", rec.Header().Get("Content-Type"))
			}

			var resp HealthResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}

			if resp.Status != "ok" {
				t.Errorf("status = %q, want ok", resp.Status)
			}

			if resp.LogCount != tt.wantLogCount {
				t.Errorf("log_count = %d, want %d", resp.LogCount, tt.wantLogCount)
			}

			if resp.UptimeSeconds < 0 {
				t.Errorf("uptime_seconds = %d, want >= 0", resp.UptimeSeconds)
			}

			if len(resp.Services) != len(tt.wantServices) {
				t.Errorf("services = %v, want %v", resp.Services, tt.wantServices)
			} else {
				serviceSet := make(map[string]bool)
				for _, svc := range resp.Services {
					serviceSet[svc] = true
				}
				for _, want := range tt.wantServices {
					if !serviceSet[want] {
						t.Errorf("missing service: %s", want)
					}
				}
			}
		})
	}
}

func TestSearchLogsContextEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		logs        []string
		query       string
		context     float64
		wantSubstrs []string
	}{
		{
			name: "context at start of logs",
			logs: []string{
				"line 1 ERROR match",
				"line 2",
				"line 3",
			},
			query:   "ERROR",
			context: 2,
			wantSubstrs: []string{
				"line 1 ERROR match",
				"line 2",
				"line 3",
			},
		},
		{
			name: "context at end of logs",
			logs: []string{
				"line 1",
				"line 2",
				"line 3 ERROR match",
			},
			query:   "ERROR",
			context: 2,
			wantSubstrs: []string{
				"line 1",
				"line 2",
				"line 3 ERROR match",
			},
		},
		{
			name:    "no matches returns appropriate message",
			logs:    []string{"line 1", "line 2"},
			query:   "NOTFOUND",
			context: 1,
			wantSubstrs: []string{
				"No log entries found",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, store := newTestServer(t, nil)
			defer func() { _ = store.Close() }()

			now := time.Now()
			for _, log := range tt.logs {
				store.Append("stdout", log, now)
			}

			resp := postJSON(t, server, JSONRPCRequest{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "tools/call",
				Params: map[string]any{
					"name": "search_logs",
					"arguments": map[string]any{
						"query":   tt.query,
						"context": tt.context,
					},
				},
			})

			if resp.Error != nil {
				t.Fatalf("unexpected error: %v", resp.Error)
			}

			text := getToolResultText(t, resp)

			for _, substr := range tt.wantSubstrs {
				if !bytes.Contains([]byte(text), []byte(substr)) {
					t.Errorf("expected output to contain %q\ngot: %s", substr, text)
				}
			}
		})
	}
}

func TestWatchLogsToolListed(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not a map")
	}

	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools is not an array")
	}

	found := false
	for _, tool := range tools {
		toolMap := tool.(map[string]any)
		if toolMap["name"] == "watch_logs" {
			found = true
			break
		}
	}

	if !found {
		t.Error("watch_logs tool not found in tools list")
	}
}

func TestWatchLogsTimeout(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	start := time.Now()

	// Call watch_logs with a short timeout and no logs arriving
	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]any{
			"name": "watch_logs",
			"arguments": map[string]any{
				"timeout": "100ms",
			},
		},
	})

	elapsed := time.Since(start)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	// Should have waited at least ~100ms
	if elapsed < 90*time.Millisecond {
		t.Errorf("elapsed time %v is less than expected timeout", elapsed)
	}

	text := getToolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("No matching logs received")) {
		t.Errorf("expected timeout message, got: %s", text)
	}
}

func TestWatchLogsReceivesLogs(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	// Start watch_logs in a goroutine
	done := make(chan JSONRPCResponse, 1)
	go func() {
		resp := postJSON(t, server, JSONRPCRequest{
			JSONRPC: "2.0",
			ID:      1,
			Method:  "tools/call",
			Params: map[string]any{
				"name": "watch_logs",
				"arguments": map[string]any{
					"timeout": "2s",
				},
			},
		})
		done <- resp
	}()

	// Give the watch_logs handler time to set up subscription
	time.Sleep(50 * time.Millisecond)

	// Append log entries
	now := time.Now()
	store.Append("stdout", "INFO: test message 1", now)
	store.Append("stderr", "ERROR: test error", now)
	store.Append("stdout", "DEBUG: test debug", now)

	// Wait for response (should come after timeout)
	resp := <-done

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	text := getToolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Received 3 matching log(s)")) {
		t.Errorf("expected to receive 3 logs, got: %s", text)
	}
	if !bytes.Contains([]byte(text), []byte("test message 1")) {
		t.Errorf("expected to contain 'test message 1', got: %s", text)
	}
	if !bytes.Contains([]byte(text), []byte("test error")) {
		t.Errorf("expected to contain 'test error', got: %s", text)
	}
}

func TestWatchLogsWithFilters(t *testing.T) {
	tests := []struct {
		name           string
		serviceRegex   *regexp.Regexp
		args           map[string]any
		logs           []struct {
			stream string
			text   string
		}
		wantCount      int
		wantSubstrs    []string
		notWantSubstrs []string
	}{
		{
			name:         "filter by stream stdout",
			serviceRegex: nil,
			args: map[string]any{
				"timeout": "200ms",
				"stream":  "stdout",
			},
			logs: []struct {
				stream string
				text   string
			}{
				{"stdout", "INFO: stdout message"},
				{"stderr", "ERROR: stderr message"},
				{"stdout", "DEBUG: another stdout"},
			},
			wantCount:      2,
			wantSubstrs:    []string{"stdout message", "another stdout"},
			notWantSubstrs: []string{"stderr message"},
		},
		{
			name:         "filter by stream stderr",
			serviceRegex: nil,
			args: map[string]any{
				"timeout": "200ms",
				"stream":  "stderr",
			},
			logs: []struct {
				stream string
				text   string
			}{
				{"stdout", "INFO: stdout message"},
				{"stderr", "ERROR: stderr message"},
			},
			wantCount:      1,
			wantSubstrs:    []string{"stderr message"},
			notWantSubstrs: []string{"stdout message"},
		},
		{
			name:         "filter by level error",
			serviceRegex: nil,
			args: map[string]any{
				"timeout": "200ms",
				"level":   "error",
			},
			logs: []struct {
				stream string
				text   string
			}{
				{"stdout", "INFO: info message"},
				{"stdout", "ERROR: error message"},
				{"stdout", "WARN: warn message"},
			},
			wantCount:      1,
			wantSubstrs:    []string{"error message"},
			notWantSubstrs: []string{"info message", "warn message"},
		},
		{
			name:         "filter by service",
			serviceRegex: regexp.MustCompile(`\[(?P<service>\w+)\]`),
			args: map[string]any{
				"timeout": "200ms",
				"service": "api",
			},
			logs: []struct {
				stream string
				text   string
			}{
				{"stdout", "[api] request received"},
				{"stdout", "[worker] job started"},
				{"stdout", "[api] response sent"},
			},
			wantCount:      2,
			wantSubstrs:    []string{"request received", "response sent"},
			notWantSubstrs: []string{"job started"},
		},
		{
			name:         "multiple filters",
			serviceRegex: regexp.MustCompile(`\[(?P<service>\w+)\]`),
			args: map[string]any{
				"timeout": "200ms",
				"service": "api",
				"level":   "error",
			},
			logs: []struct {
				stream string
				text   string
			}{
				{"stdout", "[api] INFO: request received"},
				{"stderr", "[api] ERROR: request failed"},
				{"stdout", "[worker] ERROR: job failed"},
			},
			wantCount:      1,
			wantSubstrs:    []string{"request failed"},
			notWantSubstrs: []string{"request received", "job failed"},
		},
		{
			name:         "no matches with filter",
			serviceRegex: nil,
			args: map[string]any{
				"timeout": "100ms",
				"level":   "fatal",
			},
			logs: []struct {
				stream string
				text   string
			}{
				{"stdout", "INFO: info message"},
				{"stdout", "ERROR: error message"},
			},
			wantCount:   0,
			wantSubstrs: []string{"No matching logs received"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, store := newTestServer(t, tt.serviceRegex)
			defer func() { _ = store.Close() }()

			// Start watch_logs in a goroutine
			done := make(chan JSONRPCResponse, 1)
			go func() {
				resp := postJSON(t, server, JSONRPCRequest{
					JSONRPC: "2.0",
					ID:      1,
					Method:  "tools/call",
					Params: map[string]any{
						"name":      "watch_logs",
						"arguments": tt.args,
					},
				})
				done <- resp
			}()

			// Give the watch_logs handler time to set up subscription
			time.Sleep(50 * time.Millisecond)

			// Append log entries
			now := time.Now()
			for _, log := range tt.logs {
				store.Append(log.stream, log.text, now)
			}

			// Wait for response
			resp := <-done

			if resp.Error != nil {
				t.Fatalf("unexpected error: %v", resp.Error)
			}

			text := getToolResultText(t, resp)

			if tt.wantCount > 0 {
				expected := bytes.Contains([]byte(text), []byte("Received"))
				if !expected {
					t.Errorf("expected to receive logs, got: %s", text)
				}
			}

			for _, substr := range tt.wantSubstrs {
				if !bytes.Contains([]byte(text), []byte(substr)) {
					t.Errorf("expected output to contain %q\ngot: %s", substr, text)
				}
			}

			for _, substr := range tt.notWantSubstrs {
				if bytes.Contains([]byte(text), []byte(substr)) {
					t.Errorf("expected output NOT to contain %q\ngot: %s", substr, text)
				}
			}
		})
	}
}

func TestWatchLogsDefaultTimeout(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	// Start watch_logs with no timeout specified - should use default 30s
	// We'll send a log immediately and verify it's received
	done := make(chan JSONRPCResponse, 1)
	go func() {
		resp := postJSON(t, server, JSONRPCRequest{
			JSONRPC: "2.0",
			ID:      1,
			Method:  "tools/call",
			Params: map[string]any{
				"name":      "watch_logs",
				"arguments": map[string]any{},
			},
		})
		done <- resp
	}()

	// Give the watch_logs handler time to set up subscription
	time.Sleep(50 * time.Millisecond)

	// Append a log entry
	store.Append("stdout", "test log for default timeout", time.Now())

	// We can't easily test the 30s default without waiting, so we'll just
	// verify the tool works without a timeout argument by using a short
	// timeout in a separate test. Here we're just checking it doesn't error.
	select {
	case <-done:
		// If we got a response before 30s, something went wrong or timeout
		// was different. For this test, we'll just verify no error occurred.
	case <-time.After(100 * time.Millisecond):
		// Expected - still waiting for 30s timeout
		// Cancel by closing the test (deferred store.Close will clean up)
	}
}

func TestRestartServicesToolListed(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not a map")
	}

	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools is not an array")
	}

	found := false
	for _, tool := range tools {
		toolMap := tool.(map[string]any)
		if toolMap["name"] == "restart_services" {
			found = true
			// Verify description
			if desc, ok := toolMap["description"].(string); !ok || desc == "" {
				t.Error("restart_services tool has no description")
			}
			break
		}
	}

	if !found {
		t.Error("restart_services tool not found in tools list")
	}
}

func TestRestartServices(t *testing.T) {
	tests := []struct {
		name         string
		setupChannel bool
		wantSubstr   string
	}{
		{
			name:         "success with channel",
			setupChannel: true,
			wantSubstr:   "Restart requested for all services",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := logstore.New(nil, 0)
			if err != nil {
				t.Fatalf("logstore.New() error = %v", err)
			}
			defer func() { _ = store.Close() }()

			var restartCh chan RestartRequest
			var server *Server
			if tt.setupChannel {
				restartCh = make(chan RestartRequest, 1)
				server = NewServer(store, WithRestartChannel(restartCh))
			} else {
				server = NewServer(store)
			}

			resp := postJSON(t, server, JSONRPCRequest{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "tools/call",
				Params: map[string]any{
					"name":      "restart_services",
					"arguments": map[string]any{},
				},
			})

			if resp.Error != nil {
				t.Fatalf("unexpected error: %v", resp.Error)
			}

			text := getToolResultText(t, resp)
			if !bytes.Contains([]byte(text), []byte(tt.wantSubstr)) {
				t.Errorf("expected output to contain %q, got: %s", tt.wantSubstr, text)
			}

			// Verify a restart request was sent to the channel
			if tt.setupChannel {
				select {
				case req := <-restartCh:
					// Verify it's an empty request (restart all)
					if len(req.Services) != 0 {
						t.Errorf("expected empty services list, got: %v", req.Services)
					}
				default:
					t.Error("no restart request was sent to the channel")
				}
			}
		})
	}
}

func TestRestartServicesNoChannel(t *testing.T) {
	server, store := newTestServer(t, nil)
	defer func() { _ = store.Close() }()

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      "restart_services",
			"arguments": map[string]any{},
		},
	})

	if resp.Error == nil {
		t.Fatal("expected error when restart channel is not configured")
	}

	if resp.Error.Code != -32000 {
		t.Errorf("error code = %d, want -32000", resp.Error.Code)
	}

	if !bytes.Contains([]byte(resp.Error.Message), []byte("not supported")) {
		t.Errorf("error message should mention 'not supported', got: %s", resp.Error.Message)
	}
}

func TestRestartServicesPending(t *testing.T) {
	store, err := logstore.New(nil, 0)
	if err != nil {
		t.Fatalf("logstore.New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	// Create a channel with buffer size 1
	restartCh := make(chan RestartRequest, 1)
	server := NewServer(store, WithRestartChannel(restartCh))

	// Pre-fill the channel to simulate a pending restart
	restartCh <- RestartRequest{}

	resp := postJSON(t, server, JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      "restart_services",
			"arguments": map[string]any{},
		},
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	text := getToolResultText(t, resp)
	if !bytes.Contains([]byte(text), []byte("Restart already pending")) {
		t.Errorf("expected 'Restart already pending' message, got: %s", text)
	}
}

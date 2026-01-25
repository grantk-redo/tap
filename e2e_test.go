package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/pjtatlow/tap/internal/logstore"
	"github.com/pjtatlow/tap/internal/mcp"
	"github.com/pjtatlow/tap/internal/runner"
)

func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func postMCP(t *testing.T, url string, method string, params any) map[string]any {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}

	body, _ := json.Marshal(req)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s failed: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return result
}

func getToolResultText(t *testing.T, resp map[string]any) string {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
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

func TestE2EFullFlow(t *testing.T) {
	port := getFreePort(t)
	messageURL := fmt.Sprintf("http://127.0.0.1:%d/message", port)

	// Create store with service regex
	serviceRegex := regexp.MustCompile(`\[(?P<service>\w+)\]`)
	store, err := logstore.New(serviceRegex, 0)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Start MCP server
	mcpServer := mcp.NewServer(store)
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mcpServer,
	}

	go func() { _ = httpServer.ListenAndServe() }()
	defer func() { _ = httpServer.Shutdown(context.Background()) }()

	// Wait for server to be ready
	for i := 0; i < 50; i++ {
		conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Start a process that outputs logs
	proc := runner.New("sh", "-c", `
		echo "[api] INFO: Server starting"
		echo "[api] DEBUG: Loading config"
		echo "[worker] INFO: Processing jobs"
		echo "[api] ERROR: Request failed"
		echo "[worker] WARN: Queue full"
	`)

	proc.OnLine(func(line runner.LogLine) {
		store.Append(string(line.Stream), line.Text, line.Timestamp)
	})

	ctx := context.Background()
	if err := proc.Start(ctx); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	<-proc.Done()

	// Give bleve time to index
	time.Sleep(50 * time.Millisecond)

	t.Run("initialize", func(t *testing.T) {
		resp := postMCP(t, messageURL, "initialize", nil)
		if resp["error"] != nil {
			t.Fatalf("initialize failed: %v", resp["error"])
		}
		result := resp["result"].(map[string]any)
		if result["protocolVersion"] != "2024-11-05" {
			t.Errorf("wrong protocol version: %v", result["protocolVersion"])
		}
	})

	t.Run("tools/list", func(t *testing.T) {
		resp := postMCP(t, messageURL, "tools/list", nil)
		if resp["error"] != nil {
			t.Fatalf("tools/list failed: %v", resp["error"])
		}
		result := resp["result"].(map[string]any)
		tools := result["tools"].([]any)
		if len(tools) != 7 {
			t.Errorf("expected 7 tools, got %d", len(tools))
		}
	})

	t.Run("tail_logs returns all logs", func(t *testing.T) {
		resp := postMCP(t, messageURL, "tools/call", map[string]any{
			"name":      "tail_logs",
			"arguments": map[string]any{},
		})
		if resp["error"] != nil {
			t.Fatalf("tail_logs failed: %v", resp["error"])
		}
		text := getToolResultText(t, resp)
		if !bytes.Contains([]byte(text), []byte("Server starting")) {
			t.Errorf("missing 'Server starting' in output:\n%s", text)
		}
		if !bytes.Contains([]byte(text), []byte("Processing jobs")) {
			t.Errorf("missing 'Processing jobs' in output:\n%s", text)
		}
	})

	t.Run("tail_logs filters by service", func(t *testing.T) {
		resp := postMCP(t, messageURL, "tools/call", map[string]any{
			"name": "tail_logs",
			"arguments": map[string]any{
				"service": "api",
			},
		})
		text := getToolResultText(t, resp)
		if !bytes.Contains([]byte(text), []byte("Server starting")) {
			t.Errorf("missing api log in output:\n%s", text)
		}
		if bytes.Contains([]byte(text), []byte("Processing jobs")) {
			t.Errorf("should not contain worker log:\n%s", text)
		}
	})

	t.Run("tail_logs filters by level", func(t *testing.T) {
		resp := postMCP(t, messageURL, "tools/call", map[string]any{
			"name": "tail_logs",
			"arguments": map[string]any{
				"level": "error",
			},
		})
		text := getToolResultText(t, resp)
		if !bytes.Contains([]byte(text), []byte("Request failed")) {
			t.Errorf("missing error log in output:\n%s", text)
		}
		if bytes.Contains([]byte(text), []byte("Server starting")) {
			t.Errorf("should not contain info log:\n%s", text)
		}
	})

	t.Run("tail_logs filters by service and level", func(t *testing.T) {
		resp := postMCP(t, messageURL, "tools/call", map[string]any{
			"name": "tail_logs",
			"arguments": map[string]any{
				"service": "worker",
				"level":   "warn",
			},
		})
		text := getToolResultText(t, resp)
		if !bytes.Contains([]byte(text), []byte("Queue full")) {
			t.Errorf("missing worker warn log in output:\n%s", text)
		}
	})

	t.Run("list_services returns detected services", func(t *testing.T) {
		resp := postMCP(t, messageURL, "tools/call", map[string]any{
			"name":      "list_services",
			"arguments": map[string]any{},
		})
		text := getToolResultText(t, resp)
		if !bytes.Contains([]byte(text), []byte("api")) {
			t.Errorf("missing 'api' service:\n%s", text)
		}
		if !bytes.Contains([]byte(text), []byte("worker")) {
			t.Errorf("missing 'worker' service:\n%s", text)
		}
	})

	t.Run("search_logs finds matching entries", func(t *testing.T) {
		resp := postMCP(t, messageURL, "tools/call", map[string]any{
			"name": "search_logs",
			"arguments": map[string]any{
				"query": "failed",
			},
		})
		text := getToolResultText(t, resp)
		if !bytes.Contains([]byte(text), []byte("Request failed")) {
			t.Errorf("search should find 'Request failed':\n%s", text)
		}
	})

	t.Run("search_logs with service filter", func(t *testing.T) {
		resp := postMCP(t, messageURL, "tools/call", map[string]any{
			"name": "search_logs",
			"arguments": map[string]any{
				"query":   "INFO",
				"service": "worker",
			},
		})
		text := getToolResultText(t, resp)
		if !bytes.Contains([]byte(text), []byte("Processing")) {
			t.Errorf("should find worker INFO log:\n%s", text)
		}
		if bytes.Contains([]byte(text), []byte("Server starting")) {
			t.Errorf("should not find api INFO log:\n%s", text)
		}
	})

	t.Run("clear_logs removes all entries", func(t *testing.T) {
		// Clear
		resp := postMCP(t, messageURL, "tools/call", map[string]any{
			"name":      "clear_logs",
			"arguments": map[string]any{},
		})
		text := getToolResultText(t, resp)
		if !bytes.Contains([]byte(text), []byte("cleared")) {
			t.Errorf("expected clear confirmation:\n%s", text)
		}

		// Verify empty
		resp = postMCP(t, messageURL, "tools/call", map[string]any{
			"name":      "tail_logs",
			"arguments": map[string]any{},
		})
		text = getToolResultText(t, resp)
		if !bytes.Contains([]byte(text), []byte("No log entries")) {
			t.Errorf("logs should be empty after clear:\n%s", text)
		}
	})
}

func TestE2ESSEConnection(t *testing.T) {
	port := getFreePort(t)
	sseURL := fmt.Sprintf("http://127.0.0.1:%d/sse", port)

	store, _ := logstore.New(nil, 0)
	defer func() { _ = store.Close() }()

	mcpServer := mcp.NewServer(store)
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mcpServer,
	}

	go func() { _ = httpServer.ListenAndServe() }()
	defer func() { _ = httpServer.Shutdown(context.Background()) }()

	// Wait for server
	time.Sleep(50 * time.Millisecond)

	// Connect to SSE endpoint
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, sseURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connection failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", resp.Header.Get("Content-Type"))
	}

	// Read initial endpoint event
	buf := make([]byte, 1024)
	n, _ := resp.Body.Read(buf)
	data := string(buf[:n])

	if !bytes.Contains([]byte(data), []byte("event: endpoint")) {
		t.Errorf("expected endpoint event, got: %s", data)
	}
	if !bytes.Contains([]byte(data), []byte("/message")) {
		t.Errorf("expected message URL in endpoint event, got: %s", data)
	}
}

func TestE2ELongRunningProcess(t *testing.T) {
	port := getFreePort(t)
	messageURL := fmt.Sprintf("http://127.0.0.1:%d/message", port)

	store, _ := logstore.New(nil, 0)
	defer func() { _ = store.Close() }()

	mcpServer := mcp.NewServer(store)
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mcpServer,
	}

	go func() { _ = httpServer.ListenAndServe() }()
	defer func() { _ = httpServer.Shutdown(context.Background()) }()

	// Wait for server
	time.Sleep(50 * time.Millisecond)

	// Start a process that outputs logs over time
	proc := runner.New("sh", "-c", `
		echo "line 1"
		sleep 0.1
		echo "line 2"
		sleep 0.1
		echo "line 3"
	`)

	proc.OnLine(func(line runner.LogLine) {
		store.Append(string(line.Stream), line.Text, line.Timestamp)
	})

	ctx := context.Background()
	_ = proc.Start(ctx)

	// Check logs while process is running
	time.Sleep(150 * time.Millisecond)

	resp := postMCP(t, messageURL, "tools/call", map[string]any{
		"name":      "tail_logs",
		"arguments": map[string]any{},
	})
	text := getToolResultText(t, resp)

	// Should have at least line 1 and line 2
	if !bytes.Contains([]byte(text), []byte("line 1")) {
		t.Errorf("missing line 1:\n%s", text)
	}

	// Wait for process to finish
	<-proc.Done()
	time.Sleep(50 * time.Millisecond)

	// Now should have all lines
	resp = postMCP(t, messageURL, "tools/call", map[string]any{
		"name":      "tail_logs",
		"arguments": map[string]any{},
	})
	text = getToolResultText(t, resp)

	for _, line := range []string{"line 1", "line 2", "line 3"} {
		if !bytes.Contains([]byte(text), []byte(line)) {
			t.Errorf("missing %q:\n%s", line, text)
		}
	}
}

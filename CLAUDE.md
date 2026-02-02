# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
# Build
go build -o tap .

# Run all tests
go test ./...

# Run tests with race detection (used in CI)
go test -race ./...

# Run a single test
go test -v ./internal/mcp -run TestSSEEndpoint

# Lint
golangci-lint run
```

## Architecture

tap is a process wrapper that captures stdout/stderr and exposes logs via MCP (Model Context Protocol). It allows coding agents to inspect logs from running services.

```
Runner → LogStore ← MCP Server → Coding Agent
   ↓         ↑
Process   Bleve Index
```

### Key Packages

- **root package (main.go)** - CLI entry point using Cobra. Handles command mode, stdin mode, and `run` subcommand for multiple processes.
- **internal/runner** - Spawns processes, captures stdout/stderr via pipes, emits `LogLine` events to handlers.
- **internal/logstore** - In-memory log storage with Bleve full-text search. Parses logs to extract service, level, and message using pattern matching, JSON field detection, or text patterns.
- **internal/mcp** - HTTP server implementing MCP protocol (JSON-RPC 2.0 over SSE). Exposes tools: `list_services`, `tail_logs`, `search_logs`, `clear_logs`, `log_stats`, `watch_logs`, `restart_services`.

### Log Parsing Priority

1. Pattern extraction (`--log-pattern` regex with named groups)
2. JSON fields (`level`, `service`, `msg`, etc.)
3. Text pattern matching (e.g., "ERROR:", "[api]")

### MCP Endpoints

- `GET /sse` - SSE connection, returns message endpoint URL
- `POST /message` - JSON-RPC 2.0 handler
- `GET /health` - Health check (JSON)

## Testing Notes

- Use table-driven tests
- Tests with goroutines (like SSE tests) need proper synchronization to avoid races - cancel context and wait for handler completion before reading results

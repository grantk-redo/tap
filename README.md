# tap

**tap** is a process wrapper that captures stdout/stderr from local services and exposes them via an [MCP](https://modelcontextprotocol.io/) server for AI coding agents.

Run your development server through tap, and your coding agent gains the ability to search, filter, and monitor your logs in real-time.

## Installation

```bash
go install github.com/pjtatlow/tap@latest
```

## Quick Start

```bash
# Wrap any command
tap npm run dev

# Your app runs normally, but now an MCP server is available at :19280
# Configure your AI coding agent to connect to http://localhost:19280
```

## Usage Modes

### Command Mode

Wrap a process and capture its output:

```bash
tap ./start-server.sh
tap -p 9000 npm run dev
tap -f bracket ./run.sh
```

### Stdin Mode

Read from piped input:

```bash
kubectl logs -f mypod | tap -f klog
docker logs -f mycontainer | tap
tail -f /var/log/app.log | tap -s myapp
```

### Multi-Process Mode

Run multiple services with the `run` subcommand:

```bash
tap run "api:./start-api.sh" "worker:python worker.py" "db:docker compose up postgres"
```

Each service is tagged, making it easy to filter logs by service name.

## Flags

| Flag | Short | Description | Default |
|------|-------|-------------|---------|
| `--port` | `-p` | MCP server port | 19280 |
| `--format` | `-f` | Built-in log format name | |
| `--log-pattern` | | Custom regex with named groups | |
| `--max-entries` | `-m` | Max log entries to keep (0 = unlimited) | 0 |
| `--service` | `-s` | Service name for stdin mode | |

## Log Formats

tap can parse structured logs to extract service names, log levels, and messages. Use `tap formats` to see all built-in formats:

| Format | Description | Example |
|--------|-------------|---------|
| `bracket` | [service] LEVEL: message | `[api] INFO: server starting` |
| `bracket-ts` | [timestamp] [service] LEVEL: message | `[2024-01-15 10:30:00] [api] INFO: starting` |
| `level-first` | LEVEL [service]: message | `INFO [api]: server starting` |
| `service-only` | [service] ... (level auto-detected) | `[api] anything here` |
| `logfmt` | level=X service=Y msg=Z | `level=info service=api msg="handled"` |
| `klog` | Kubernetes klog format | `I0115 10:30:00.000 1234 server.go:42] Starting` |
| `pm2` | PM2 style | `2024-01-15T10:30:00 \| api \| starting` |
| `docker-compose` | Docker Compose | `web-1 \| Listening on port 3000` |

### Custom Patterns

Use `--log-pattern` with a regex containing named groups:

```bash
tap --log-pattern '^\[(?P<service>\w+)\]\s+(?P<level>\w+):\s+(?P<message>.*)$' ./run.sh
```

Named groups:
- `(?P<service>...)` - Extract service/component name
- `(?P<level>...)` - Extract log level (trace, debug, info, warn, error, fatal)
- `(?P<message>...)` - Extract message body

## Environment Variables

| Variable | Description |
|----------|-------------|
| `TAP_FORMAT` | Built-in format name (same as `--format`) |
| `TAP_LOG_PATTERN` | Custom regex pattern (same as `--log-pattern`) |
| `TAP_MAX_ENTRIES` | Maximum log entries to keep |
| `TAP_SERVICE` | Service name for stdin mode |

## MCP Tools

When connected, AI agents have access to these tools:

### `list_services`
List all detected service names. Call this first to discover available services for filtering.

### `tail_logs`
Get recent log lines with optional filtering:
- `lines` - Number of lines (default: 50)
- `since` / `until` - Time window (e.g., "5m", "1h", "30s")
- `service` - Filter by service name
- `level` - Filter by log level
- `stream` - Filter by stdout/stderr

### `search_logs`
Full-text search across logs:
- `query` - Search query (e.g., "error", "Level:error", "Service:api AND timeout")
- `limit` - Max results (default: 100)
- `since` / `until` - Time window
- `before` / `after` / `context` - Show surrounding lines (like grep -B/-A/-C)

### `watch_logs`
Wait for new log entries matching filters. Useful for waiting for specific events like errors or startup messages.
- `timeout` - How long to wait (default: "30s")
- `service` / `level` / `stream` - Filters

### `get_log`
Fetch a specific log entry by ID with optional context lines.

### `log_stats`
Get statistics: total count, counts by level/service/stream, time range.

### `clear_logs`
Clear all stored logs and reset the search index.

## HTTP Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /logs` | View logs as plain text or JSON |
| `GET /health` | Health check with log count and services |
| `GET /metrics` | Prometheus-style metrics |
| `POST /` | MCP Streamable HTTP transport |
| `GET /sse` | MCP SSE transport |

### `/logs` Query Parameters

```bash
# Get last 50 error logs from the api service as JSON
curl "http://localhost:19280/logs?lines=50&service=api&level=error&format=json"

# Get logs from the last 5 minutes
curl "http://localhost:19280/logs?since=5m"
```

## Agent Configuration

### Claude Code

Add to your MCP settings:

```json
{
  "mcpServers": {
    "tap": {
      "type": "http",
      "url": "http://localhost:19280"
    }
  }
}
```

## Signal Handling

- **First Ctrl+C**: Restart the subprocess
- **Second Ctrl+C** (within 2s): Exit gracefully after process stops
- **Third Ctrl+C** (within 2s): Force kill immediately

## Architecture

```
Runner -> LogStore <- MCP Server -> AI Agent
   |          ^
Process    Bleve Index
```

- **Runner** - Spawns processes, captures stdout/stderr via pipes
- **LogStore** - In-memory storage with Bleve full-text search
- **MCP Server** - HTTP server implementing MCP protocol (JSON-RPC 2.0)

## License

MIT

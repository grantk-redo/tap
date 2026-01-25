# tap - Process Log MCP

tap (Process Log MCP) is a process wrapper that captures stdout/stderr from local services and exposes them via an MCP (Model Context Protocol) server. This allows coding agents like Claude Code to inspect logs from running services during development.

## Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                           tap                                   │
│                                                                 │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────────────┐ │
│  │   Runner    │───▶│  LogStore   │◀───│    MCP Server       │ │
│  │             │    │             │    │                     │ │
│  │ - Spawns    │    │ - In-memory │    │ - HTTP/SSE          │ │
│  │   process   │    │   storage   │    │ - JSON-RPC 2.0      │ │
│  │ - Captures  │    │ - Bleve     │    │ - Tool handlers     │ │
│  │   stdout/   │    │   indexing  │    │                     │ │
│  │   stderr    │    │ - Filtering │    │                     │ │
│  └─────────────┘    └─────────────┘    └─────────────────────┘ │
│         │                                        │              │
│         ▼                                        ▼              │
│  ┌─────────────┐                         ┌─────────────┐       │
│  │  Wrapped    │                         │   Coding    │       │
│  │  Process    │                         │   Agent     │       │
│  │  (your      │                         │  (Claude    │       │
│  │  services)  │                         │   Code)     │       │
│  └─────────────┘                         └─────────────┘       │
└─────────────────────────────────────────────────────────────────┘
```

## Usage

```bash
tap [flags] <command> [args...]
tap [flags]                      # stdin mode (reads from piped input)
tap run <service:command>...     # run multiple commands with service tags
```

### Flags

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--port` | `-p` | `8080` | Port for the MCP HTTP server |
| `--format` | `-f` | `""` | Use a built-in log format (see `tap formats`) |
| `--log-pattern` | | `""` | Regex with named capture groups to parse logs |
| `--max-entries` | `-m` | `0` | Maximum log entries to keep (0 = unlimited) |
| `--service` | `-s` | `""` | Service name for stdin mode |

### Subcommands

| Command | Description |
|---------|-------------|
| `formats` | List available built-in log formats |
| `completion` | Generate shell completion scripts (bash, zsh, fish, powershell) |
| `run` | Run multiple commands with service tags (format: `service:command`) |

### Environment Variables

| Variable | Description |
|----------|-------------|
| `TAP_LOG_PATTERN` | Same as `--log-pattern` flag |
| `TAP_FORMAT` | Same as `--format` flag |
| `TAP_MAX_ENTRIES` | Same as `--max-entries` flag |
| `TAP_SERVICE` | Same as `--service` flag |

### Built-in Formats

Use `--format` (or `-f`) to select a preset log format:

| Format | Description | Example |
|--------|-------------|---------|
| `bracket` | `[service] LEVEL: message` | `[api] INFO: server starting` |
| `bracket-ts` | `[timestamp] [service] LEVEL: message` | `[2024-01-15 10:30:00] [api] INFO: starting` |
| `level-first` | `LEVEL [service]: message` | `INFO [api]: server starting` |
| `service-only` | `[service] ...` (level auto-detected) | `[api] anything here` |
| `logfmt` | `level=X service=Y msg=Z` | `level=info service=api msg="handled"` |
| `klog` | Kubernetes klog format | `I0115 10:30:00 12345 main.go:123] Starting` |
| `pm2` | PM2 style | `2024-01-15T10:30:00 \| api \| starting` |
| `docker-compose` | Docker Compose style | `web-1 \| Listening on port 3000` |

Run `tap formats` for details on each pattern.

### Custom Log Pattern

The `--log-pattern` flag accepts a regex with named capture groups:

| Group Name | Description |
|------------|-------------|
| `(?P<service>...)` | Extract service/component name |
| `(?P<level>...)` | Extract log level |
| `(?P<message>...)` | Extract message body |

### Examples

Basic usage (auto-detects levels from common patterns):
```bash
tap ./start-server.sh
```

With custom port:
```bash
tap -p 9000 npm run dev
```

Using a built-in format:
```bash
tap --format bracket ./run.sh
tap -f docker-compose docker compose up
```

With custom log pattern:
```bash
# Pattern: [service] LEVEL: message
tap --log-pattern '^\[(?P<service>\w+)\]\s+(?P<level>\w+):\s+(?P<message>.*)$' ./run.sh

# Example input:  [api] INFO: server starting on port 8080
# Extracted:      service=api, level=info, message="server starting on port 8080"
```

Service-only extraction:
```bash
# Just extract service name, auto-detect level
tap --log-pattern '\[(?P<service>\w+)\]' ./services.sh
```

Using environment variables:
```bash
export TAP_FORMAT=bracket
tap ./run.sh

# Or with custom pattern
export TAP_LOG_PATTERN='^\[(?P<service>\w+)\]\s+(?P<level>\w+):\s+(?P<message>.*)$'
tap ./run.sh
```

Shell completion (add to your shell profile):
```bash
# Bash
source <(tap completion bash)

# Zsh
source <(tap completion zsh)

# Fish
tap completion fish | source
```

### Stdin Mode

When no command is provided, tap reads from stdin. This is useful for piping logs from other processes:

```bash
# Pipe Kubernetes logs
kubectl logs -f my-pod | tap

# Tail a log file with a service name
tail -f /var/log/myapp.log | tap -s myapp

# Pipe from docker logs
docker logs -f my-container | tap --service my-container --format logfmt
```

### Run Subcommand

The `run` subcommand starts multiple commands simultaneously, each tagged with a service name:

```bash
# Format: service:command
tap run api:"npm run api" worker:"npm run worker" frontend:"npm run dev"

# Each service's output is automatically tagged
tap run db:"docker compose up postgres" api:"go run ./cmd/api"
```

### Limiting Log Entries

Use `--max-entries` to cap memory usage by limiting stored log entries:

```bash
# Keep only the last 10,000 log entries
tap -m 10000 ./start-all.sh

# Via environment variable
TAP_MAX_ENTRIES=5000 tap ./run.sh
```

### Process Control

In command mode and `run` subcommand, Ctrl+C provides process control:

| Signal | Action |
|--------|--------|
| **Ctrl+C** (1st) | Send SIGINT to subprocess, restart after it exits |
| **Ctrl+C** (2nd) | Cancel restart, exit tap after subprocess finishes |
| **Ctrl+C** (3rd) | Force SIGKILL and exit immediately |

This allows quick restarts during development without restarting the MCP server.

## Components

### Runner (`internal/runner`)

The Runner spawns and manages the wrapped process:

- Executes the command with provided arguments
- Captures stdout and stderr via pipes
- Scans output line-by-line in real-time
- Emits `LogLine` events to registered handlers
- Tracks process lifecycle and exit code

```go
type LogLine struct {
    Stream    Stream    // "stdout" or "stderr"
    Text      string    // The log line content
    Timestamp time.Time // When the line was captured
}
```

### LogStore (`internal/logstore`)

The LogStore provides in-memory log storage with full-text search:

**Storage:**
- Stores all log entries in memory (append-only slice)
- Each entry gets a unique incrementing ID
- Supports clearing all logs

**Log Parsing (in priority order):**

1. **Pattern extraction** - If `--log-pattern` is set, named groups are extracted first
2. **JSON fields** - For JSON logs, checks standard field names
3. **Text patterns** - Falls back to regex matching in log text

**Indexing:**
- Uses [Bleve](https://blevesearch.com/) for full-text search
- In-memory index (no disk persistence)
- Keyword fields for service/level/stream (exact match)
- Full-text field for message content

**Filtering:**
- Filter by stream (stdout/stderr)
- Filter by service name
- Filter by log level
- Filter by time (`since` parameter)

#### Log Level Detection

Levels are detected in priority order:

1. **Pattern match** - `(?P<level>...)` capture group
2. **JSON fields** (`level`, `lvl`, `severity`):
   ```json
   {"level": "error", "msg": "failed"}
   ```
3. **Text patterns** (case-insensitive word boundaries):
   - `fatal`, `panic` → `fatal`
   - `error`, `err` → `error`
   - `warn`, `warning` → `warn`
   - `info` → `info`
   - `debug` → `debug`
   - `trace` → `trace`

#### Service Extraction

Services are detected in priority order:

1. **Pattern match** - `(?P<service>...)` capture group
2. **JSON fields** - `service`, `svc`, `component`, `app`

#### Message Extraction

The message (displayed in output) is extracted in priority order:

1. **Pattern match** - `(?P<message>...)` capture group
2. **JSON fields** - `msg`, `message`, `text`, `error`
3. **Auto-strip** - Removes detected service tags and level prefixes from text

### MCP Server (`internal/mcp`)

Implements the Model Context Protocol over HTTP with Server-Sent Events:

**MCP Endpoints:**
- `GET /sse` - SSE connection endpoint, sends message endpoint URL
- `POST /message` - JSON-RPC 2.0 message handler

**HTTP Endpoints:**
- `GET /health` - Health check endpoint returning JSON with status, uptime, log count, and services
- `GET /metrics` - Prometheus-style metrics for monitoring

**Health Response Example:**
```json
{
  "status": "ok",
  "uptime": "2h15m30s",
  "log_count": 12543,
  "services": ["api", "worker", "frontend"]
}
```

**Protocol:**
- Implements MCP protocol version `2024-11-05`
- Supports `initialize`, `tools/list`, and `tools/call` methods

## MCP Tools

### `list_services`

List all detected service names. **Call this first** to discover available services before filtering.

**Parameters:** None

**Returns:** List of unique service names found in logs.

### `tail_logs`

Get the most recent log lines.

**Parameters:**
| Name | Type | Default | Description |
|------|------|---------|-------------|
| `lines` | integer | 50 | Number of lines to return |
| `since` | string | | Only logs from this duration ago (e.g., `"30s"`, `"5m"`, `"1h"`) |
| `until` | string | | Only logs until this duration ago (e.g., `"30s"`, `"5m"`, `"1h"`) |
| `stream` | string | | Filter: `"stdout"` or `"stderr"` |
| `service` | string | | Filter by service name |
| `level` | string | | Filter: `"trace"`, `"debug"`, `"info"`, `"warn"`, `"error"`, `"fatal"` |

**Example:**
```json
{
  "name": "tail_logs",
  "arguments": {
    "lines": 100,
    "since": "5m",
    "service": "api",
    "level": "error"
  }
}
```

### `search_logs`

Full-text search across all logs.

**Parameters:**
| Name | Type | Default | Description |
|------|------|---------|-------------|
| `query` | string | *required* | Search query (Bleve query syntax) |
| `limit` | integer | 100 | Maximum results |
| `stream` | string | | Filter by stream |
| `service` | string | | Filter by service |
| `level` | string | | Filter by level |
| `before` | integer | 0 | Number of log lines to include before each match |
| `after` | integer | 0 | Number of log lines to include after each match |
| `context` | integer | 0 | Number of log lines to include before and after each match (shorthand for setting both `before` and `after`) |

**Example:**
```json
{
  "name": "search_logs",
  "arguments": {
    "query": "timeout",
    "service": "worker",
    "limit": 50
  }
}
```

**Example with context:**
```json
{
  "name": "search_logs",
  "arguments": {
    "query": "error",
    "context": 5
  }
}
```

**Query Syntax:**
- Simple terms: `error timeout`
- Phrases: `"connection refused"`
- Field queries: `Level:error`, `Service:api`
- Boolean: `error AND database`
- Wildcards: `connect*`

### `clear_logs`

Clear all stored logs and reset the index.

**Parameters:** None

**Returns:** Confirmation message.

### `log_stats`

Get statistics about stored logs.

**Parameters:** None

**Returns:** Statistics including total log count, counts by service, counts by level, and time range of stored logs.

**Example Response:**
```json
{
  "total": 12543,
  "by_service": {
    "api": 5234,
    "worker": 4321,
    "frontend": 2988
  },
  "by_level": {
    "info": 8000,
    "error": 234,
    "warn": 1500,
    "debug": 2809
  },
  "oldest": "2024-01-15T10:00:00Z",
  "newest": "2024-01-15T12:30:00Z"
}
```

### `get_log`

Fetch a specific log entry by ID with optional surrounding context.

**Parameters:**
| Name | Type | Default | Description |
|------|------|---------|-------------|
| `id` | integer | *required* | The log entry ID to fetch |
| `before` | integer | 0 | Number of log lines to include before the entry |
| `after` | integer | 0 | Number of log lines to include after the entry |
| `context` | integer | 0 | Number of log lines to include before and after (shorthand for both) |

**Example:**
```json
{
  "name": "get_log",
  "arguments": {
    "id": 12345,
    "context": 10
  }
}
```

### `watch_logs`

Subscribe to new logs in real-time. Returns logs that arrive during the specified timeout period.

**Parameters:**
| Name | Type | Default | Description |
|------|------|---------|-------------|
| `timeout` | string | `"30s"` | How long to watch for new logs (e.g., `"10s"`, `"1m"`) |
| `stream` | string | | Filter: `"stdout"` or `"stderr"` |
| `service` | string | | Filter by service name |
| `level` | string | | Filter by log level |

**Example:**
```json
{
  "name": "watch_logs",
  "arguments": {
    "timeout": "60s",
    "service": "api",
    "level": "error"
  }
}
```

**Note:** This tool blocks until either the timeout expires or the maximum number of logs is collected. Use shorter timeouts for interactive debugging.

## Output Format

Log entries are formatted as:
```
[HH:MM:SS.mmm] [stream] [service] [LEVEL] <message>
```

Example:
```
[14:23:45.123] [stdout] [api] [INFO] Server starting on :8080
[14:23:45.456] [stderr] [worker] [ERROR] Failed to connect to database
[14:23:46.789] [stdout] [api] [DEBUG] Request received: GET /health
```

- Service and level fields are omitted if not detected
- Message is the extracted/cleaned message, not the raw log line

## Configuring Claude Code

Add to your MCP settings (`.claude/settings.json` or global settings):

```json
{
  "mcpServers": {
    "tap": {
      "type": "sse",
      "url": "http://localhost:8080/sse"
    }
  }
}
```

Then start your services with tap:
```bash
tap -f bracket ./start-dev.sh
# Or with a custom pattern:
tap --log-pattern '^\[(?P<service>\w+)\]\s+(?P<level>\w+):\s+(?P<message>.*)$' ./start-dev.sh
```

Claude Code will now have access to `list_services`, `tail_logs`, `search_logs`, `clear_logs`, `log_stats`, `get_log`, and `watch_logs` tools.

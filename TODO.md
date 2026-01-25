# tap Feature TODO

## Log Management
- [x] Max entries limit - Prevent unbounded memory growth (e.g., `--max-entries 10000`)
- [x] Log statistics tool - Count by level/service, error rate, etc.
- [x] Context lines - Show N lines before/after search matches (like `grep -B/-C`)

## Flexibility
- [x] Stdin mode - Accept piped input instead of wrapping a process (`kubectl logs -f | tap`)
- [x] Multiple commands - Wrap several processes, each tagged with a service name
- [x] Until parameter - Time range with both `since` and `until` for `tail_logs`

## Observability
- [x] Health endpoint - `/health` for monitoring the MCP server
- [x] Metrics - Log counts, index size, etc. as a tool or endpoint

## For Agents
- [x] Watch/stream tool - Subscribe to new logs in real-time instead of polling
- [x] Get log by ID - Fetch a specific log entry and surrounding context

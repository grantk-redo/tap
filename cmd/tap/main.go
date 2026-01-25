package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pjtatlow/tap/internal/logstore"
	"github.com/pjtatlow/tap/internal/mcp"
	"github.com/pjtatlow/tap/internal/runner"
	"github.com/spf13/cobra"
)

// Built-in log format patterns
var builtinPatterns = map[string]struct {
	pattern     string
	description string
	example     string
}{
	"bracket": {
		pattern:     `^\[(?P<service>\w+)\]\s+(?P<level>\w+)[:\s]+(?P<message>.*)$`,
		description: "[service] LEVEL: message",
		example:     "[api] INFO: server starting",
	},
	"bracket-ts": {
		pattern:     `^\[(?P<timestamp>[^\]]+)\]\s*\[(?P<service>\w+)\]\s+(?P<level>\w+)[:\s]+(?P<message>.*)$`,
		description: "[timestamp] [service] LEVEL: message",
		example:     "[2024-01-15 10:30:00] [api] INFO: server starting",
	},
	"level-first": {
		pattern:     `^(?P<level>\w+)\s+\[(?P<service>\w+)\][:\s]*(?P<message>.*)$`,
		description: "LEVEL [service]: message",
		example:     "INFO [api]: server starting",
	},
	"service-only": {
		pattern:     `^\[(?P<service>\w+)\]`,
		description: "[service] ... (level auto-detected)",
		example:     "[api] anything here",
	},
	"logfmt": {
		pattern:     `level=(?P<level>\w+).*?(?:service|svc|component)=(?P<service>\w+).*?(?:msg|message)="?(?P<message>[^"]+)"?`,
		description: "level=X service=Y msg=Z (logfmt style)",
		example:     `level=info service=api msg="request handled"`,
	},
	"klog": {
		pattern:     `^(?P<level>[IWEF])(?P<timestamp>\d{4}\s+[\d:\.]+)\s+\d+\s+(?P<file>[^:]+):(?P<line>\d+)\]\s+(?P<message>.*)$`,
		description: "Kubernetes klog format",
		example:     "I0115 10:30:00.000000 12345 server.go:123] Starting",
	},
	"pm2": {
		pattern:     `^(?P<timestamp>[\d\-T:\.Z]+)\s*\|\s*(?P<service>\w+)\s*\|\s*(?P<message>.*)$`,
		description: "PM2 style: timestamp | service | message",
		example:     "2024-01-15T10:30:00 | api | server starting",
	},
	"docker-compose": {
		pattern:     `^(?P<service>[\w\-]+)\s*\|\s*(?P<message>.*)$`,
		description: "Docker Compose: service | message",
		example:     "web-1 | Listening on port 3000",
	},
}

var (
	port          int
	logPatternStr string
	formatName    string
	maxEntries    int
	serviceName   string
)

// serviceCommand represents a service name and its command to run
type serviceCommand struct {
	service string
	command string
	args    []string
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "tap [flags] [command] [args...]",
		Short: "Process Log MCP - capture logs and expose via MCP server",
		Long: `tap (Process Log MCP) is a process wrapper that captures stdout/stderr
from local services and exposes them via an MCP server for coding agents.

It can operate in two modes:
  1. Command mode: Wrap a command and capture its output
  2. Stdin mode: Read from piped input (when no command is provided)

Environment Variables:
  TAP_FORMAT         Built-in format name (same as --format)
  TAP_LOG_PATTERN    Custom regex pattern (same as --log-pattern)
  TAP_MAX_ENTRIES    Maximum log entries to keep (same as --max-entries)
  TAP_SERVICE        Service name for stdin mode (same as --service)

Custom Pattern Named Groups:
  (?P<service>...)   Extract service/component name
  (?P<level>...)     Extract log level
  (?P<message>...)   Extract message body`,
		Example: `  # Command mode - wrap a process
  tap ./start-server.sh
  tap -p 9000 npm run dev
  tap -f bracket ./run.sh
  tap -f docker-compose docker compose up
  tap --log-pattern '^\[(?P<service>\w+)\]\s+(?P<level>\w+):\s+(?P<message>.*)$' ./run.sh

  # Stdin mode - read from piped input
  kubectl logs -f mypod | tap -f klog
  cat mylog.txt | tap --log-pattern '...'
  tail -f /var/log/app.log | tap -s myapp`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return nil
			}
			// No args provided, check if stdin is a pipe
			if isStdinPipe() {
				return nil
			}
			return fmt.Errorf("requires at least 1 arg(s) or piped input")
		},
		Run: runMain,
	}

	rootCmd.PersistentFlags().IntVarP(&port, "port", "p", 8080, "MCP server port")
	rootCmd.PersistentFlags().StringVar(&logPatternStr, "log-pattern", "", "regex with named groups: (?P<service>...), (?P<level>...), (?P<message>...)")
	rootCmd.PersistentFlags().StringVarP(&formatName, "format", "f", "", "built-in log format (see 'tap formats')")
	rootCmd.PersistentFlags().IntVarP(&maxEntries, "max-entries", "m", 0, "maximum log entries to keep (0 = unlimited)")
	rootCmd.Flags().StringVarP(&serviceName, "service", "s", "", "service name for stdin mode (defaults to empty)")

	formatsCmd := &cobra.Command{
		Use:   "formats",
		Short: "List available built-in log formats",
		Run: func(cmd *cobra.Command, args []string) {
			printFormats()
		},
	}
	rootCmd.AddCommand(formatsCmd)

	runCmd := &cobra.Command{
		Use:   "run service:command [service:command...]",
		Short: "Run multiple processes with service tags",
		Long: `Run multiple processes simultaneously, tagging each with a service name.

Each argument should be in the format "service:command" or "service:command arg1 arg2".
All processes share the same log store and MCP server.

The service name is used to tag all output from that process, overriding any
service name that might be extracted from log patterns.`,
		Example: `  # Run multiple services
  tap run "api:./start-api.sh" "worker:python worker.py" "db:docker compose up postgres"

  # With other flags
  tap -p 9000 -f bracket run "svc1:./cmd1" "svc2:./cmd2"

  # Services with arguments
  tap run "web:npm run dev" "api:go run ./cmd/api --port 8081"`,
		Args: cobra.MinimumNArgs(1),
		Run:  runMultiProcess,
	}
	rootCmd.AddCommand(runCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runMain(cmd *cobra.Command, args []string) {
	// Environment variable fallbacks
	if logPatternStr == "" {
		logPatternStr = os.Getenv("TAP_LOG_PATTERN")
	}
	if formatName == "" {
		formatName = os.Getenv("TAP_FORMAT")
	}
	if maxEntries == 0 {
		if envVal := os.Getenv("TAP_MAX_ENTRIES"); envVal != "" {
			if parsed, err := strconv.Atoi(envVal); err == nil {
				maxEntries = parsed
			}
		}
	}
	if serviceName == "" {
		serviceName = os.Getenv("TAP_SERVICE")
	}

	// Resolve pattern from format name or direct pattern
	var logPattern *regexp.Regexp
	if formatName != "" {
		builtin, ok := builtinPatterns[formatName]
		if !ok {
			log.Fatalf("unknown format %q, use 'tap formats' to see available formats", formatName)
		}
		var err error
		logPattern, err = regexp.Compile(builtin.pattern)
		if err != nil {
			log.Fatalf("invalid builtin pattern for %q: %v", formatName, err)
		}
	} else if logPatternStr != "" {
		var err error
		logPattern, err = regexp.Compile(logPatternStr)
		if err != nil {
			log.Fatalf("invalid log-pattern: %v", err)
		}
	}

	store, err := logstore.New(logPattern, maxEntries)
	if err != nil {
		log.Fatalf("failed to create log store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mcpServer := mcp.NewServer(store)
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mcpServer,
	}

	go func() {
		log.Printf("MCP server listening on :%d", port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Determine mode: command mode or stdin mode
	if len(args) > 0 {
		// Command mode: wrap a process
		runCommandMode(ctx, cancel, args, store, httpServer, sigCh)
	} else {
		// Stdin mode: read from piped input
		runStdinMode(ctx, cancel, store, httpServer, sigCh)
	}
}

func runCommandMode(ctx context.Context, cancel context.CancelFunc, args []string, store *logstore.Store, httpServer *http.Server, sigCh chan os.Signal) {
	for {
		// Create a new context for each subprocess iteration
		procCtx, procCancel := context.WithCancel(ctx)

		proc := runner.New(args[0], args[1:]...)
		proc.OnLine(func(line runner.LogLine) {
			store.Append(string(line.Stream), line.Text, line.Timestamp)
		})

		if err := proc.Start(procCtx); err != nil {
			procCancel()
			log.Fatalf("failed to start process: %v", err)
		}

		// Signal handling state
		sigCount := 0
		restartPending := false
		exitPending := false

		// Handle signals and process exit
	loop:
		for {
			select {
			case <-sigCh:
				sigCount++
				switch sigCount {
				case 1:
					log.Println("Restarting... (Ctrl+C again to exit, third time to force kill)")
					restartPending = true
					_ = proc.Signal(syscall.SIGINT)
				case 2:
					log.Println("Will exit after process stops... (Ctrl+C to force kill)")
					restartPending = false
					exitPending = true
				case 3:
					log.Println("Force killing process...")
					_ = proc.Signal(syscall.SIGKILL)
					procCancel()
					cancel()
					_ = httpServer.Shutdown(context.Background())
					return
				}
			case <-proc.Done():
				log.Printf("process exited with code %d", proc.ExitCode())
				break loop
			}
		}

		procCancel()

		// Decide what to do after process exits
		if restartPending {
			log.Println("Restarting process...")
			continue // restart the loop
		}
		if exitPending {
			log.Println("Exiting...")
		}

		// Normal exit or exit pending - shutdown gracefully
		cancel()
		_ = httpServer.Shutdown(context.Background())
		return
	}
}

func runStdinMode(ctx context.Context, cancel context.CancelFunc, store *logstore.Store, httpServer *http.Server, sigCh chan os.Signal) {
	stdinDone := make(chan struct{})

	// Determine the stream name
	streamName := "stdin"
	if serviceName != "" {
		streamName = serviceName
	}

	log.Printf("Reading from stdin (stream: %s)", streamName)

	go func() {
		defer close(stdinDone)
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
				store.Append(streamName, scanner.Text(), time.Now())
			}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("stdin read error: %v", err)
		}
	}()

	select {
	case <-sigCh:
		log.Println("received shutdown signal")
	case <-stdinDone:
		log.Println("stdin closed")
	}

	cancel()
	_ = httpServer.Shutdown(context.Background())
}

// isStdinPipe returns true if stdin is a pipe or file (not a terminal)
func isStdinPipe() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) == 0
}

func formatNames() []string {
	names := make([]string, 0, len(builtinPatterns))
	for name := range builtinPatterns {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func printFormats() {
	fmt.Println("Built-in log formats:")
	fmt.Println()
	for _, name := range formatNames() {
		f := builtinPatterns[name]
		fmt.Printf("  %s\n", name)
		fmt.Printf("    Description: %s\n", f.description)
		fmt.Printf("    Example:     %s\n", f.example)
		fmt.Printf("    Pattern:     %s\n", f.pattern)
		fmt.Println()
	}
}

// parseServiceCommand parses a "service:command" string into its components.
// The format is "service:command arg1 arg2 ..." where the command and args
// are separated by spaces after the first colon.
func parseServiceCommand(spec string) (serviceCommand, error) {
	idx := strings.Index(spec, ":")
	if idx == -1 {
		return serviceCommand{}, fmt.Errorf("invalid format %q: expected 'service:command'", spec)
	}

	service := spec[:idx]
	if service == "" {
		return serviceCommand{}, fmt.Errorf("invalid format %q: service name cannot be empty", spec)
	}

	cmdPart := strings.TrimSpace(spec[idx+1:])
	if cmdPart == "" {
		return serviceCommand{}, fmt.Errorf("invalid format %q: command cannot be empty", spec)
	}

	// Split command into executable and args
	parts := strings.Fields(cmdPart)
	return serviceCommand{
		service: service,
		command: parts[0],
		args:    parts[1:],
	}, nil
}

func runMultiProcess(cmd *cobra.Command, args []string) {
	// Environment variable fallbacks (same as runMain)
	if logPatternStr == "" {
		logPatternStr = os.Getenv("TAP_LOG_PATTERN")
	}
	if formatName == "" {
		formatName = os.Getenv("TAP_FORMAT")
	}
	if maxEntries == 0 {
		if envVal := os.Getenv("TAP_MAX_ENTRIES"); envVal != "" {
			if parsed, err := strconv.Atoi(envVal); err == nil {
				maxEntries = parsed
			}
		}
	}

	// Resolve pattern from format name or direct pattern
	var logPattern *regexp.Regexp
	if formatName != "" {
		builtin, ok := builtinPatterns[formatName]
		if !ok {
			log.Fatalf("unknown format %q, use 'tap formats' to see available formats", formatName)
		}
		var err error
		logPattern, err = regexp.Compile(builtin.pattern)
		if err != nil {
			log.Fatalf("invalid builtin pattern for %q: %v", formatName, err)
		}
	} else if logPatternStr != "" {
		var err error
		logPattern, err = regexp.Compile(logPatternStr)
		if err != nil {
			log.Fatalf("invalid log-pattern: %v", err)
		}
	}

	// Parse all service:command specifications
	var services []serviceCommand
	for _, arg := range args {
		svc, err := parseServiceCommand(arg)
		if err != nil {
			log.Fatalf("failed to parse service spec: %v", err)
		}
		services = append(services, svc)
	}

	store, err := logstore.New(logPattern, maxEntries)
	if err != nil {
		log.Fatalf("failed to create log store: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mcpServer := mcp.NewServer(store)
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mcpServer,
	}

	go func() {
		log.Printf("MCP server listening on :%d", port)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		// Create a new context for each subprocess iteration
		procCtx, procCancel := context.WithCancel(ctx)

		// Start all processes
		var runners []*runner.Runner
		for _, svc := range services {
			proc := runner.New(svc.command, svc.args...)
			svcName := svc.service // capture for closure
			proc.OnLine(func(line runner.LogLine) {
				// Use service name as the stream to tag all output
				store.AppendWithService(svcName, string(line.Stream), line.Text, line.Timestamp)
			})

			if err := proc.Start(procCtx); err != nil {
				log.Printf("failed to start %s: %v", svc.service, err)
				continue
			}
			log.Printf("started service: %s (command: %s)", svc.service, svc.command)
			runners = append(runners, proc)
		}

		if len(runners) == 0 {
			procCancel()
			log.Fatal("no processes started successfully")
		}

		// Wait for all processes to exit
		allDone := make(chan struct{})
		go func() {
			for _, r := range runners {
				<-r.Done()
			}
			close(allDone)
		}()

		// Signal handling state
		sigCount := 0
		restartPending := false
		exitPending := false

		// Handle signals and process exit
	loop:
		for {
			select {
			case <-sigCh:
				sigCount++
				switch sigCount {
				case 1:
					log.Println("Restarting... (Ctrl+C again to exit, third time to force kill)")
					restartPending = true
					for _, r := range runners {
						_ = r.Signal(syscall.SIGINT)
					}
				case 2:
					log.Println("Will exit after process stops... (Ctrl+C to force kill)")
					restartPending = false
					exitPending = true
				case 3:
					log.Println("Force killing process...")
					for _, r := range runners {
						_ = r.Signal(syscall.SIGKILL)
					}
					procCancel()
					cancel()
					_ = httpServer.Shutdown(context.Background())
					return
				}
			case <-allDone:
				log.Println("all processes exited")
				break loop
			}
		}

		procCancel()

		// Decide what to do after process exits
		if restartPending {
			log.Println("Restarting processes...")
			continue // restart the loop
		}
		if exitPending {
			log.Println("Exiting...")
		}

		// Normal exit or exit pending - shutdown gracefully
		cancel()
		_ = httpServer.Shutdown(context.Background())
		return
	}
}

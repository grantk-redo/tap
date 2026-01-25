package logstore

import (
	"fmt"
	"regexp"
	"testing"
	"time"
)

func TestExtractLevel(t *testing.T) {
	store, _ := New(nil, 0)
	defer func() { _ = store.Close() }()

	tests := []struct {
		name  string
		input string
		want  LogLevel
	}{
		{"plain info", "[INFO] starting", LevelInfo},
		{"plain error", "ERROR: something failed", LevelError},
		{"plain warn", "WARN: watch out", LevelWarn},
		{"plain debug", "debug message here", LevelDebug},
		{"json level", `{"level":"error","msg":"failed"}`, LevelError},
		{"json lvl", `{"lvl":"warn","msg":"warning"}`, LevelWarn},
		{"json severity", `{"severity":"info","msg":"ok"}`, LevelInfo},
		{"fatal", "FATAL: crash", LevelFatal},
		{"panic", "panic: runtime error", LevelFatal},
		{"unknown", "just some text", LevelUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			structured := parseStructured(tt.input)
			got := store.extractLevel(tt.input, structured, nil)
			if got != tt.want {
				t.Errorf("extractLevel(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseStructured(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNil bool
		wantKey string
	}{
		{"valid json", `{"msg":"hello","count":5}`, false, "msg"},
		{"plain text", "not json", true, ""},
		{"invalid json", `{"broken":}`, true, ""},
		{"empty", "", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseStructured(tt.input)
			if tt.wantNil && got != nil {
				t.Errorf("parseStructured(%q) = %v, want nil", tt.input, got)
			}
			if !tt.wantNil && got == nil {
				t.Errorf("parseStructured(%q) = nil, want non-nil", tt.input)
			}
			if !tt.wantNil && tt.wantKey != "" {
				if _, ok := got[tt.wantKey]; !ok {
					t.Errorf("parseStructured(%q) missing key %q", tt.input, tt.wantKey)
				}
			}
		})
	}
}

func TestExtractMessage(t *testing.T) {
	servicePattern := regexp.MustCompile(`\[(?P<service>\w+)\]`)

	tests := []struct {
		name    string
		input   string
		pattern *regexp.Regexp
		want    string
	}{
		// JSON logs - extract msg field
		{"json msg", `{"level":"info","msg":"user logged in"}`, nil, "user logged in"},
		{"json message", `{"level":"error","message":"connection failed"}`, nil, "connection failed"},
		{"json text", `{"level":"debug","text":"processing"}`, nil, "processing"},
		{"json no msg", `{"level":"info","count":5}`, nil, `{"level":"info","count":5}`},

		// Text logs - strip prefixes
		{"text with level", "INFO: server starting", nil, "server starting"},
		{"text with level bracket", "[ERROR] connection failed", nil, "connection failed"},
		{"text with service", "[api] starting server", servicePattern, "starting server"},
		{"text with service and level", "[api] ERROR: request failed", servicePattern, "request failed"},
		{"text plain", "just a message", nil, "just a message"},
		{"text level only", "DEBUG - loading config", nil, "loading config"},
		{"text warn", "WARN: disk space low", nil, "disk space low"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, _ := New(tt.pattern, 0)
			defer func() { _ = store.Close() }()
			structured := parseStructured(tt.input)
			patternMatches := store.extractFromPattern(tt.input)
			got := store.extractMessage(tt.input, structured, patternMatches)
			if got != tt.want {
				t.Errorf("extractMessage(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestStoreAppendAndTail(t *testing.T) {
	store, err := New(nil, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	store.Append("stdout", "INFO: line 1", now)
	store.Append("stderr", "ERROR: line 2", now)
	store.Append("stdout", "DEBUG: line 3", now)

	tests := []struct {
		name    string
		n       int
		opts    FilterOptions
		wantLen int
	}{
		{"all", 10, FilterOptions{}, 3},
		{"last 2", 2, FilterOptions{}, 2},
		{"stdout only", 10, FilterOptions{Stream: "stdout"}, 2},
		{"stderr only", 10, FilterOptions{Stream: "stderr"}, 1},
		{"error level", 10, FilterOptions{Level: LevelError}, 1},
		{"info level", 10, FilterOptions{Level: LevelInfo}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.Tail(tt.n, tt.opts)
			if len(got) != tt.wantLen {
				t.Errorf("Tail(%d, %+v) returned %d entries, want %d", tt.n, tt.opts, len(got), tt.wantLen)
			}
		})
	}
}

func TestServiceExtraction(t *testing.T) {
	// Use named capture group for service
	re := regexp.MustCompile(`\[(?P<service>\w+)\]`)
	store, err := New(re, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	store.Append("stdout", "[api] starting server", now)
	store.Append("stdout", "[worker] processing job", now)
	store.Append("stdout", "[api] request received", now)
	store.Append("stdout", "no service here", now)

	services := store.Services()
	if len(services) != 2 {
		t.Errorf("Services() = %v, want 2 services", services)
	}

	apiLogs := store.Tail(10, FilterOptions{Service: "api"})
	if len(apiLogs) != 2 {
		t.Errorf("Tail with service=api returned %d, want 2", len(apiLogs))
	}

	workerLogs := store.Tail(10, FilterOptions{Service: "worker"})
	if len(workerLogs) != 1 {
		t.Errorf("Tail with service=worker returned %d, want 1", len(workerLogs))
	}
}

func TestLogPatternFullExtraction(t *testing.T) {
	// Full pattern with service, level, and message
	pattern := regexp.MustCompile(`^\[(?P<service>\w+)\]\s+(?P<level>\w+):\s+(?P<message>.*)$`)
	store, err := New(pattern, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	store.Append("stdout", "[api] INFO: server starting on port 8080", now)
	store.Append("stdout", "[worker] ERROR: failed to process job", now)
	store.Append("stderr", "[api] DEBUG: request received from 127.0.0.1", now)

	entries := store.Tail(10, FilterOptions{})
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Check first entry
	e := entries[0]
	if e.Service != "api" {
		t.Errorf("entry 0: Service = %q, want %q", e.Service, "api")
	}
	if e.Level != LevelInfo {
		t.Errorf("entry 0: Level = %q, want %q", e.Level, LevelInfo)
	}
	if e.Message != "server starting on port 8080" {
		t.Errorf("entry 0: Message = %q, want %q", e.Message, "server starting on port 8080")
	}

	// Check second entry
	e = entries[1]
	if e.Service != "worker" {
		t.Errorf("entry 1: Service = %q, want %q", e.Service, "worker")
	}
	if e.Level != LevelError {
		t.Errorf("entry 1: Level = %q, want %q", e.Level, LevelError)
	}
	if e.Message != "failed to process job" {
		t.Errorf("entry 1: Message = %q, want %q", e.Message, "failed to process job")
	}

	// Test filtering
	errorLogs := store.Tail(10, FilterOptions{Level: LevelError})
	if len(errorLogs) != 1 {
		t.Errorf("error logs: got %d, want 1", len(errorLogs))
	}

	apiLogs := store.Tail(10, FilterOptions{Service: "api"})
	if len(apiLogs) != 2 {
		t.Errorf("api logs: got %d, want 2", len(apiLogs))
	}
}

func TestClear(t *testing.T) {
	store, err := New(nil, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	store.Append("stdout", "line 1", time.Now())
	store.Append("stdout", "line 2", time.Now())

	if len(store.Tail(10, FilterOptions{})) != 2 {
		t.Error("expected 2 entries before clear")
	}

	store.Clear()

	if len(store.Tail(10, FilterOptions{})) != 0 {
		t.Error("expected 0 entries after clear")
	}
}

func TestFilterSince(t *testing.T) {
	store, err := New(nil, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	oldTime := now.Add(-10 * time.Minute)
	recentTime := now.Add(-30 * time.Second)

	store.Append("stdout", "old log", oldTime)
	store.Append("stdout", "recent log 1", recentTime)
	store.Append("stdout", "recent log 2", now)

	tests := []struct {
		name    string
		since   time.Time
		wantLen int
	}{
		{"no filter", time.Time{}, 3},
		{"since 1 minute ago", now.Add(-1 * time.Minute), 2},
		{"since 5 minutes ago", now.Add(-5 * time.Minute), 2},
		{"since 15 minutes ago", now.Add(-15 * time.Minute), 3},
		{"since 10 seconds ago", now.Add(-10 * time.Second), 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.Tail(100, FilterOptions{Since: tt.since})
			if len(got) != tt.wantLen {
				t.Errorf("Tail with since=%v returned %d entries, want %d", tt.since, len(got), tt.wantLen)
			}
		})
	}
}

func TestFilterSinceCombined(t *testing.T) {
	re := regexp.MustCompile(`\[(?P<service>\w+)\]`)
	store, err := New(re, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	oldTime := now.Add(-10 * time.Minute)

	store.Append("stdout", "[api] ERROR: old error", oldTime)
	store.Append("stdout", "[api] ERROR: recent error", now)
	store.Append("stdout", "[api] INFO: recent info", now)
	store.Append("stdout", "[worker] ERROR: recent worker error", now)

	// Filter by since + level + service
	got := store.Tail(100, FilterOptions{
		Since:   now.Add(-1 * time.Minute),
		Level:   LevelError,
		Service: "api",
	})

	if len(got) != 1 {
		t.Errorf("expected 1 entry (recent api error), got %d", len(got))
	}
	if len(got) > 0 && got[0].Text != "[api] ERROR: recent error" {
		t.Errorf("wrong entry: %s", got[0].Text)
	}
}

func TestFilterUntil(t *testing.T) {
	store, err := New(nil, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	oldTime := now.Add(-10 * time.Minute)
	midTime := now.Add(-5 * time.Minute)
	recentTime := now.Add(-1 * time.Minute)

	store.Append("stdout", "old log", oldTime)
	store.Append("stdout", "mid log", midTime)
	store.Append("stdout", "recent log", recentTime)
	store.Append("stdout", "newest log", now)

	tests := []struct {
		name    string
		until   time.Time
		wantLen int
	}{
		{"no filter", time.Time{}, 4},
		{"until 30 seconds ago", now.Add(-30 * time.Second), 3},
		{"until 3 minutes ago", now.Add(-3 * time.Minute), 2},
		{"until 7 minutes ago", now.Add(-7 * time.Minute), 1},
		{"until 15 minutes ago", now.Add(-15 * time.Minute), 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.Tail(100, FilterOptions{Until: tt.until})
			if len(got) != tt.wantLen {
				t.Errorf("Tail with until=%v returned %d entries, want %d", tt.until, len(got), tt.wantLen)
			}
		})
	}
}

func TestFilterSinceAndUntil(t *testing.T) {
	store, err := New(nil, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	store.Append("stdout", "very old log", now.Add(-2*time.Hour))
	store.Append("stdout", "old log", now.Add(-1*time.Hour))
	store.Append("stdout", "mid log 1", now.Add(-45*time.Minute))
	store.Append("stdout", "mid log 2", now.Add(-35*time.Minute))
	store.Append("stdout", "recent log", now.Add(-15*time.Minute))
	store.Append("stdout", "newest log", now)

	tests := []struct {
		name      string
		since     time.Time
		until     time.Time
		wantLen   int
		wantTexts []string
	}{
		{
			name:      "window from 1h to 30m ago",
			since:     now.Add(-1 * time.Hour),
			until:     now.Add(-30 * time.Minute),
			wantLen:   3,
			wantTexts: []string{"old log", "mid log 1", "mid log 2"},
		},
		{
			name:      "window from 50m to 40m ago",
			since:     now.Add(-50 * time.Minute),
			until:     now.Add(-40 * time.Minute),
			wantLen:   1,
			wantTexts: []string{"mid log 1"},
		},
		{
			name:      "window with no entries",
			since:     now.Add(-3 * time.Hour),
			until:     now.Add(-2*time.Hour - 30*time.Minute),
			wantLen:   0,
			wantTexts: []string{},
		},
		{
			name:      "since only (no until)",
			since:     now.Add(-20 * time.Minute),
			until:     time.Time{},
			wantLen:   2,
			wantTexts: []string{"recent log", "newest log"},
		},
		{
			name:      "until only (no since)",
			since:     time.Time{},
			until:     now.Add(-90 * time.Minute),
			wantLen:   1,
			wantTexts: []string{"very old log"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.Tail(100, FilterOptions{Since: tt.since, Until: tt.until})
			if len(got) != tt.wantLen {
				t.Errorf("Tail with since=%v, until=%v returned %d entries, want %d", tt.since, tt.until, len(got), tt.wantLen)
			}
			for i, wantText := range tt.wantTexts {
				if i < len(got) && got[i].Text != wantText {
					t.Errorf("entry %d: got %q, want %q", i, got[i].Text, wantText)
				}
			}
		})
	}
}

func TestMaxEntries(t *testing.T) {
	tests := []struct {
		name           string
		maxEntries     int
		appendCount    int
		wantEntryCount int
		wantFirstText  string
		wantLastText   string
	}{
		{
			name:           "unlimited (0) keeps all entries",
			maxEntries:     0,
			appendCount:    5,
			wantEntryCount: 5,
			wantFirstText:  "line 1",
			wantLastText:   "line 5",
		},
		{
			name:           "max 3 with 5 appends keeps last 3",
			maxEntries:     3,
			appendCount:    5,
			wantEntryCount: 3,
			wantFirstText:  "line 3",
			wantLastText:   "line 5",
		},
		{
			name:           "max 3 with 3 appends keeps all",
			maxEntries:     3,
			appendCount:    3,
			wantEntryCount: 3,
			wantFirstText:  "line 1",
			wantLastText:   "line 3",
		},
		{
			name:           "max 3 with 2 appends keeps all",
			maxEntries:     3,
			appendCount:    2,
			wantEntryCount: 2,
			wantFirstText:  "line 1",
			wantLastText:   "line 2",
		},
		{
			name:           "max 1 keeps only last entry",
			maxEntries:     1,
			appendCount:    5,
			wantEntryCount: 1,
			wantFirstText:  "line 5",
			wantLastText:   "line 5",
		},
		{
			name:           "max 10 with 5 appends keeps all",
			maxEntries:     10,
			appendCount:    5,
			wantEntryCount: 5,
			wantFirstText:  "line 1",
			wantLastText:   "line 5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := New(nil, tt.maxEntries)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			defer func() { _ = store.Close() }()

			now := time.Now()
			for i := 1; i <= tt.appendCount; i++ {
				store.Append("stdout", fmt.Sprintf("line %d", i), now)
			}

			entries := store.Tail(100, FilterOptions{})
			if len(entries) != tt.wantEntryCount {
				t.Errorf("got %d entries, want %d", len(entries), tt.wantEntryCount)
			}
			if len(entries) > 0 {
				if entries[0].Text != tt.wantFirstText {
					t.Errorf("first entry text = %q, want %q", entries[0].Text, tt.wantFirstText)
				}
				if entries[len(entries)-1].Text != tt.wantLastText {
					t.Errorf("last entry text = %q, want %q", entries[len(entries)-1].Text, tt.wantLastText)
				}
			}
		})
	}
}

func TestMaxEntriesRemovesFromIndex(t *testing.T) {
	store, err := New(nil, 2)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	store.Append("stdout", "searchable first", now)
	store.Append("stdout", "searchable second", now)
	store.Append("stdout", "searchable third", now)

	// The store should have only 2 entries now
	entries := store.Tail(100, FilterOptions{})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Search should not find the removed entry
	results, err := store.Search("first", 10, FilterOptions{})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 search results for removed entry, got %d", len(results))
	}

	// Search should find entries that are still there
	results, err = store.Search("second", 10, FilterOptions{})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 search result for 'second', got %d", len(results))
	}

	results, err = store.Search("third", 10, FilterOptions{})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 search result for 'third', got %d", len(results))
	}
}

func TestStats(t *testing.T) {
	tests := []struct {
		name           string
		serviceRegex   *regexp.Regexp
		logs           []struct {
			stream string
			text   string
			ts     time.Duration // offset from base time
		}
		wantTotal     int
		wantByLevel   map[LogLevel]int
		wantByService map[string]int
		wantByStream  map[string]int
	}{
		{
			name:         "empty store",
			serviceRegex: nil,
			logs:         nil,
			wantTotal:    0,
			wantByLevel:  map[LogLevel]int{},
			wantByService: map[string]int{},
			wantByStream:  map[string]int{},
		},
		{
			name:         "single entry",
			serviceRegex: nil,
			logs: []struct {
				stream string
				text   string
				ts     time.Duration
			}{
				{"stdout", "INFO: hello world", 0},
			},
			wantTotal:     1,
			wantByLevel:   map[LogLevel]int{LevelInfo: 1},
			wantByService: map[string]int{},
			wantByStream:  map[string]int{"stdout": 1},
		},
		{
			name:         "multiple levels",
			serviceRegex: nil,
			logs: []struct {
				stream string
				text   string
				ts     time.Duration
			}{
				{"stdout", "INFO: message 1", 0},
				{"stdout", "INFO: message 2", time.Second},
				{"stderr", "ERROR: something failed", 2 * time.Second},
				{"stderr", "WARN: warning here", 3 * time.Second},
				{"stdout", "DEBUG: debugging", 4 * time.Second},
			},
			wantTotal:     5,
			wantByLevel:   map[LogLevel]int{LevelInfo: 2, LevelError: 1, LevelWarn: 1, LevelDebug: 1},
			wantByService: map[string]int{},
			wantByStream:  map[string]int{"stdout": 3, "stderr": 2},
		},
		{
			name:         "with services",
			serviceRegex: regexp.MustCompile(`\[(?P<service>\w+)\]`),
			logs: []struct {
				stream string
				text   string
				ts     time.Duration
			}{
				{"stdout", "[api] INFO: request received", 0},
				{"stdout", "[api] INFO: response sent", time.Second},
				{"stdout", "[worker] INFO: job started", 2 * time.Second},
				{"stderr", "[worker] ERROR: job failed", 3 * time.Second},
				{"stdout", "[scheduler] DEBUG: tick", 4 * time.Second},
				{"stdout", "no service here", 5 * time.Second},
			},
			wantTotal:     6,
			wantByLevel:   map[LogLevel]int{LevelInfo: 3, LevelError: 1, LevelDebug: 1, LevelUnknown: 1},
			wantByService: map[string]int{"api": 2, "worker": 2, "scheduler": 1},
			wantByStream:  map[string]int{"stdout": 5, "stderr": 1},
		},
		{
			name:         "all log levels",
			serviceRegex: nil,
			logs: []struct {
				stream string
				text   string
				ts     time.Duration
			}{
				{"stdout", "TRACE: tracing", 0},
				{"stdout", "DEBUG: debugging", time.Second},
				{"stdout", "INFO: info", 2 * time.Second},
				{"stdout", "WARN: warning", 3 * time.Second},
				{"stderr", "ERROR: error", 4 * time.Second},
				{"stderr", "FATAL: fatal", 5 * time.Second},
			},
			wantTotal:     6,
			wantByLevel:   map[LogLevel]int{LevelTrace: 1, LevelDebug: 1, LevelInfo: 1, LevelWarn: 1, LevelError: 1, LevelFatal: 1},
			wantByService: map[string]int{},
			wantByStream:  map[string]int{"stdout": 4, "stderr": 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := New(tt.serviceRegex, 0)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			defer func() { _ = store.Close() }()

			baseTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
			for _, log := range tt.logs {
				store.Append(log.stream, log.text, baseTime.Add(log.ts))
			}

			stats := store.Stats()

			if stats.Total != tt.wantTotal {
				t.Errorf("Total = %d, want %d", stats.Total, tt.wantTotal)
			}

			// Check ByLevel
			for level, wantCount := range tt.wantByLevel {
				if gotCount := stats.ByLevel[level]; gotCount != wantCount {
					t.Errorf("ByLevel[%q] = %d, want %d", level, gotCount, wantCount)
				}
			}
			// Check no extra levels
			for level, count := range stats.ByLevel {
				if _, expected := tt.wantByLevel[level]; !expected && count > 0 {
					t.Errorf("unexpected ByLevel[%q] = %d", level, count)
				}
			}

			// Check ByService
			for service, wantCount := range tt.wantByService {
				if gotCount := stats.ByService[service]; gotCount != wantCount {
					t.Errorf("ByService[%q] = %d, want %d", service, gotCount, wantCount)
				}
			}
			// Check no extra services
			for service, count := range stats.ByService {
				if _, expected := tt.wantByService[service]; !expected && count > 0 {
					t.Errorf("unexpected ByService[%q] = %d", service, count)
				}
			}

			// Check ByStream
			for stream, wantCount := range tt.wantByStream {
				if gotCount := stats.ByStream[stream]; gotCount != wantCount {
					t.Errorf("ByStream[%q] = %d, want %d", stream, gotCount, wantCount)
				}
			}

			// Check timestamps for non-empty stores
			if tt.wantTotal > 0 {
				if stats.EarliestTime.IsZero() {
					t.Error("EarliestTime should not be zero")
				}
				if stats.LatestTime.IsZero() {
					t.Error("LatestTime should not be zero")
				}
				if stats.LatestTime.Before(stats.EarliestTime) {
					t.Errorf("LatestTime (%v) is before EarliestTime (%v)", stats.LatestTime, stats.EarliestTime)
				}
			}
		})
	}
}

func TestStatsTimestamps(t *testing.T) {
	store, err := New(nil, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	earliest := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	middle := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	latest := time.Date(2024, 1, 15, 11, 45, 30, 0, time.UTC)

	// Add logs out of order to verify min/max tracking
	store.Append("stdout", "middle log", middle)
	store.Append("stdout", "earliest log", earliest)
	store.Append("stdout", "latest log", latest)

	stats := store.Stats()

	if !stats.EarliestTime.Equal(earliest) {
		t.Errorf("EarliestTime = %v, want %v", stats.EarliestTime, earliest)
	}
	if !stats.LatestTime.Equal(latest) {
		t.Errorf("LatestTime = %v, want %v", stats.LatestTime, latest)
	}
}

func TestGetContext(t *testing.T) {
	tests := []struct {
		name       string
		logCount   int
		targetID   uint64
		before     int
		after      int
		wantLen    int
		wantFirst  string
		wantLast   string
	}{
		{
			name:       "middle entry with context",
			logCount:   10,
			targetID:   5,
			before:     2,
			after:      2,
			wantLen:    5,
			wantFirst:  "line 3",
			wantLast:   "line 7",
		},
		{
			name:       "first entry with before context",
			logCount:   10,
			targetID:   1,
			before:     2,
			after:      2,
			wantLen:    3,
			wantFirst:  "line 1",
			wantLast:   "line 3",
		},
		{
			name:       "last entry with after context",
			logCount:   10,
			targetID:   10,
			before:     2,
			after:      2,
			wantLen:    3,
			wantFirst:  "line 8",
			wantLast:   "line 10",
		},
		{
			name:       "no context",
			logCount:   10,
			targetID:   5,
			before:     0,
			after:      0,
			wantLen:    1,
			wantFirst:  "line 5",
			wantLast:   "line 5",
		},
		{
			name:       "only before context",
			logCount:   10,
			targetID:   5,
			before:     3,
			after:      0,
			wantLen:    4,
			wantFirst:  "line 2",
			wantLast:   "line 5",
		},
		{
			name:       "only after context",
			logCount:   10,
			targetID:   5,
			before:     0,
			after:      3,
			wantLen:    4,
			wantFirst:  "line 5",
			wantLast:   "line 8",
		},
		{
			name:       "large context clips to boundaries",
			logCount:   5,
			targetID:   3,
			before:     10,
			after:      10,
			wantLen:    5,
			wantFirst:  "line 1",
			wantLast:   "line 5",
		},
		{
			name:       "nonexistent ID returns nil",
			logCount:   5,
			targetID:   999,
			before:     2,
			after:      2,
			wantLen:    0,
			wantFirst:  "",
			wantLast:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := New(nil, 0)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			defer func() { _ = store.Close() }()

			now := time.Now()
			for i := 1; i <= tt.logCount; i++ {
				store.Append("stdout", fmt.Sprintf("line %d", i), now)
			}

			result := store.GetContext(tt.targetID, tt.before, tt.after)

			if len(result) != tt.wantLen {
				t.Errorf("GetContext() returned %d entries, want %d", len(result), tt.wantLen)
			}

			if tt.wantLen > 0 {
				if result[0].Text != tt.wantFirst {
					t.Errorf("first entry = %q, want %q", result[0].Text, tt.wantFirst)
				}
				if result[len(result)-1].Text != tt.wantLast {
					t.Errorf("last entry = %q, want %q", result[len(result)-1].Text, tt.wantLast)
				}
			}
		})
	}
}

func TestGetContextEmptyStore(t *testing.T) {
	store, err := New(nil, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	result := store.GetContext(1, 2, 2)
	if result != nil {
		t.Errorf("GetContext on empty store should return nil, got %v", result)
	}
}

func TestGetContextAfterEviction(t *testing.T) {
	store, err := New(nil, 3)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	// Add 5 entries to a store with max 3, so entries 1 and 2 are evicted
	for i := 1; i <= 5; i++ {
		store.Append("stdout", fmt.Sprintf("line %d", i), now)
	}

	// Entry ID 1 and 2 should not exist
	result := store.GetContext(1, 1, 1)
	if result != nil {
		t.Errorf("GetContext for evicted entry should return nil, got %v", result)
	}

	// Entry ID 3 should exist and be first in store
	result = store.GetContext(3, 1, 1)
	if len(result) != 2 {
		t.Errorf("GetContext for entry 3 returned %d entries, want 2", len(result))
	}
	if len(result) > 0 && result[0].Text != "line 3" {
		t.Errorf("first entry = %q, want %q", result[0].Text, "line 3")
	}
}

func TestAppendWithService(t *testing.T) {
	tests := []struct {
		name            string
		pattern         *regexp.Regexp
		serviceName     string
		stream          string
		text            string
		wantService     string
		wantStream      string
	}{
		{
			name:        "explicit service overrides pattern extraction",
			pattern:     regexp.MustCompile(`\[(?P<service>\w+)\]`),
			serviceName: "myapi",
			stream:      "stdout",
			text:        "[worker] INFO: doing work",
			wantService: "myapi",
			wantStream:  "stdout",
		},
		{
			name:        "explicit service with no pattern",
			pattern:     nil,
			serviceName: "myworker",
			stream:      "stderr",
			text:        "some error message",
			wantService: "myworker",
			wantStream:  "stderr",
		},
		{
			name:        "empty service falls back to pattern extraction",
			pattern:     regexp.MustCompile(`\[(?P<service>\w+)\]`),
			serviceName: "",
			stream:      "stdout",
			text:        "[api] INFO: request received",
			wantService: "api",
			wantStream:  "stdout",
		},
		{
			name:        "empty service with no pattern",
			pattern:     nil,
			serviceName: "",
			stream:      "stdout",
			text:        "plain log line",
			wantService: "",
			wantStream:  "stdout",
		},
		{
			name:        "explicit service with JSON log",
			pattern:     nil,
			serviceName: "scheduler",
			stream:      "stdout",
			text:        `{"level":"info","service":"ignored","msg":"tick"}`,
			wantService: "scheduler",
			wantStream:  "stdout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := New(tt.pattern, 0)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			defer func() { _ = store.Close() }()

			now := time.Now()
			store.AppendWithService(tt.serviceName, tt.stream, tt.text, now)

			entries := store.Tail(1, FilterOptions{})
			if len(entries) != 1 {
				t.Fatalf("expected 1 entry, got %d", len(entries))
			}

			entry := entries[0]
			if entry.Service != tt.wantService {
				t.Errorf("Service = %q, want %q", entry.Service, tt.wantService)
			}
			if entry.Stream != tt.wantStream {
				t.Errorf("Stream = %q, want %q", entry.Stream, tt.wantStream)
			}
			if entry.Text != tt.text {
				t.Errorf("Text = %q, want %q", entry.Text, tt.text)
			}
		})
	}
}

func TestAppendWithServiceFilterByService(t *testing.T) {
	store, err := New(nil, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	// Simulate multiple processes with explicit service names
	store.AppendWithService("api", "stdout", "api log 1", now)
	store.AppendWithService("api", "stderr", "api error 1", now)
	store.AppendWithService("worker", "stdout", "worker log 1", now)
	store.AppendWithService("worker", "stdout", "worker log 2", now)
	store.AppendWithService("scheduler", "stdout", "scheduler log 1", now)

	tests := []struct {
		name      string
		service   string
		wantCount int
	}{
		{"all entries", "", 5},
		{"api only", "api", 2},
		{"worker only", "worker", 2},
		{"scheduler only", "scheduler", 1},
		{"nonexistent", "db", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := FilterOptions{Service: tt.service}
			entries := store.Tail(100, opts)
			if len(entries) != tt.wantCount {
				t.Errorf("got %d entries, want %d", len(entries), tt.wantCount)
			}
		})
	}

	// Verify Services() returns all unique services
	services := store.Services()
	if len(services) != 3 {
		t.Errorf("Services() returned %d services, want 3", len(services))
	}
}

func TestSubscribe(t *testing.T) {
	tests := []struct {
		name           string
		logs           []string
		wantReceived   int
		channelBufSize int
	}{
		{
			name:           "receives single log entry",
			logs:           []string{"log 1"},
			wantReceived:   1,
			channelBufSize: 10,
		},
		{
			name:           "receives multiple log entries",
			logs:           []string{"log 1", "log 2", "log 3"},
			wantReceived:   3,
			channelBufSize: 10,
		},
		{
			name:           "drops entries when channel is full",
			logs:           []string{"log 1", "log 2", "log 3", "log 4", "log 5"},
			wantReceived:   2,
			channelBufSize: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := New(nil, 0)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			defer func() { _ = store.Close() }()

			ch := make(chan LogEntry, tt.channelBufSize)
			unsubscribe := store.Subscribe(ch)
			defer unsubscribe()

			now := time.Now()
			for _, log := range tt.logs {
				store.Append("stdout", log, now)
			}

			// Collect received entries
			var received []LogEntry
			for {
				select {
				case entry := <-ch:
					received = append(received, entry)
				default:
					// No more entries
					goto done
				}
			}
		done:

			if len(received) != tt.wantReceived {
				t.Errorf("received %d entries, want %d", len(received), tt.wantReceived)
			}
		})
	}
}

func TestSubscribeUnsubscribe(t *testing.T) {
	store, err := New(nil, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	ch := make(chan LogEntry, 10)
	unsubscribe := store.Subscribe(ch)

	now := time.Now()
	store.Append("stdout", "before unsubscribe", now)

	// Should have received the entry
	select {
	case entry := <-ch:
		if entry.Text != "before unsubscribe" {
			t.Errorf("received wrong entry: %s", entry.Text)
		}
	default:
		t.Error("expected to receive entry before unsubscribe")
	}

	// Unsubscribe
	unsubscribe()

	// Append after unsubscribe
	store.Append("stdout", "after unsubscribe", now)

	// Should not receive anything
	select {
	case entry := <-ch:
		t.Errorf("should not receive after unsubscribe, got: %s", entry.Text)
	default:
		// Expected
	}
}

func TestSubscribeMultipleSubscribers(t *testing.T) {
	store, err := New(nil, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	ch1 := make(chan LogEntry, 10)
	ch2 := make(chan LogEntry, 10)
	ch3 := make(chan LogEntry, 10)

	unsub1 := store.Subscribe(ch1)
	unsub2 := store.Subscribe(ch2)
	unsub3 := store.Subscribe(ch3)
	defer unsub1()
	defer unsub2()
	defer unsub3()

	now := time.Now()
	store.Append("stdout", "test log", now)

	// All subscribers should receive the entry
	for i, ch := range []chan LogEntry{ch1, ch2, ch3} {
		select {
		case entry := <-ch:
			if entry.Text != "test log" {
				t.Errorf("subscriber %d received wrong entry: %s", i+1, entry.Text)
			}
		default:
			t.Errorf("subscriber %d did not receive entry", i+1)
		}
	}
}

func TestGetByID(t *testing.T) {
	tests := []struct {
		name       string
		logCount   int
		targetID   uint64
		wantFound  bool
		wantText   string
	}{
		{
			name:      "find existing entry",
			logCount:  5,
			targetID:  3,
			wantFound: true,
			wantText:  "line 3",
		},
		{
			name:      "find first entry",
			logCount:  5,
			targetID:  1,
			wantFound: true,
			wantText:  "line 1",
		},
		{
			name:      "find last entry",
			logCount:  5,
			targetID:  5,
			wantFound: true,
			wantText:  "line 5",
		},
		{
			name:      "nonexistent ID returns nil",
			logCount:  5,
			targetID:  999,
			wantFound: false,
		},
		{
			name:      "ID 0 returns nil",
			logCount:  5,
			targetID:  0,
			wantFound: false,
		},
		{
			name:      "empty store returns nil",
			logCount:  0,
			targetID:  1,
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := New(nil, 0)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			defer func() { _ = store.Close() }()

			now := time.Now()
			for i := 1; i <= tt.logCount; i++ {
				store.Append("stdout", fmt.Sprintf("line %d", i), now)
			}

			result := store.GetByID(tt.targetID)

			if tt.wantFound {
				if result == nil {
					t.Errorf("GetByID(%d) returned nil, want entry", tt.targetID)
					return
				}
				if result.Text != tt.wantText {
					t.Errorf("GetByID(%d).Text = %q, want %q", tt.targetID, result.Text, tt.wantText)
				}
				if result.ID != tt.targetID {
					t.Errorf("GetByID(%d).ID = %d, want %d", tt.targetID, result.ID, tt.targetID)
				}
			} else {
				if result != nil {
					t.Errorf("GetByID(%d) = %+v, want nil", tt.targetID, result)
				}
			}
		})
	}
}

func TestGetByIDAfterEviction(t *testing.T) {
	store, err := New(nil, 3)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Now()
	// Add 5 entries to a store with max 3, so entries 1 and 2 are evicted
	for i := 1; i <= 5; i++ {
		store.Append("stdout", fmt.Sprintf("line %d", i), now)
	}

	// Entry ID 1 and 2 should not exist
	result := store.GetByID(1)
	if result != nil {
		t.Errorf("GetByID(1) for evicted entry should return nil, got %+v", result)
	}

	result = store.GetByID(2)
	if result != nil {
		t.Errorf("GetByID(2) for evicted entry should return nil, got %+v", result)
	}

	// Entry ID 3 should exist
	result = store.GetByID(3)
	if result == nil {
		t.Errorf("GetByID(3) should return entry, got nil")
	} else if result.Text != "line 3" {
		t.Errorf("GetByID(3).Text = %q, want %q", result.Text, "line 3")
	}

	// Entry ID 5 should exist
	result = store.GetByID(5)
	if result == nil {
		t.Errorf("GetByID(5) should return entry, got nil")
	} else if result.Text != "line 5" {
		t.Errorf("GetByID(5).Text = %q, want %q", result.Text, "line 5")
	}
}

func TestSubscribeEntryContent(t *testing.T) {
	pattern := regexp.MustCompile(`\[(?P<service>\w+)\]`)
	store, err := New(pattern, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	ch := make(chan LogEntry, 10)
	unsubscribe := store.Subscribe(ch)
	defer unsubscribe()

	now := time.Now()
	store.Append("stderr", "[api] ERROR: connection failed", now)

	select {
	case entry := <-ch:
		if entry.Stream != "stderr" {
			t.Errorf("Stream = %q, want %q", entry.Stream, "stderr")
		}
		if entry.Service != "api" {
			t.Errorf("Service = %q, want %q", entry.Service, "api")
		}
		if entry.Level != LevelError {
			t.Errorf("Level = %q, want %q", entry.Level, LevelError)
		}
		if entry.Text != "[api] ERROR: connection failed" {
			t.Errorf("Text = %q, want %q", entry.Text, "[api] ERROR: connection failed")
		}
	default:
		t.Error("expected to receive entry")
	}
}

package logstore

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/mapping"
)

type LogLevel string

const (
	LevelUnknown LogLevel = ""
	LevelTrace   LogLevel = "trace"
	LevelDebug   LogLevel = "debug"
	LevelInfo    LogLevel = "info"
	LevelWarn    LogLevel = "warn"
	LevelError   LogLevel = "error"
	LevelFatal   LogLevel = "fatal"
)

type LogEntry struct {
	ID         uint64         `json:"id"`
	Stream     string         `json:"stream"`
	Service    string         `json:"service,omitempty"`
	Level      LogLevel       `json:"level,omitempty"`
	Message    string         `json:"message"`
	Text       string         `json:"text"`
	Timestamp  time.Time      `json:"timestamp"`
	Structured map[string]any `json:"structured,omitempty"`
}

type Store struct {
	mu          sync.RWMutex
	entries     []LogEntry
	nextID      uint64
	index       bleve.Index
	logPattern  *regexp.Regexp
	groupNames  map[string]int // maps group name to index
	maxEntries  int
	subscribers map[chan LogEntry]struct{}
}

func New(logPattern *regexp.Regexp, maxEntries int) (*Store, error) {
	indexMapping := buildIndexMapping()
	index, err := bleve.NewMemOnly(indexMapping)
	if err != nil {
		return nil, fmt.Errorf("failed to create index: %w", err)
	}

	groupNames := make(map[string]int)
	if logPattern != nil {
		for i, name := range logPattern.SubexpNames() {
			if name != "" {
				groupNames[name] = i
			}
		}
	}

	return &Store{
		entries:     make([]LogEntry, 0),
		nextID:      1,
		index:       index,
		logPattern:  logPattern,
		groupNames:  groupNames,
		maxEntries:  maxEntries,
		subscribers: make(map[chan LogEntry]struct{}),
	}, nil
}

func buildIndexMapping() *mapping.IndexMappingImpl {
	// Keyword field - exact match, no analysis
	keywordFieldMapping := bleve.NewTextFieldMapping()
	keywordFieldMapping.Analyzer = keyword.Name

	// Text field - full-text search with standard analyzer
	textFieldMapping := bleve.NewTextFieldMapping()

	// Datetime field
	datetimeFieldMapping := bleve.NewDateTimeFieldMapping()

	// LogEntry document mapping
	logEntryMapping := bleve.NewDocumentMapping()
	logEntryMapping.AddFieldMappingsAt("Stream", keywordFieldMapping)
	logEntryMapping.AddFieldMappingsAt("Service", keywordFieldMapping)
	logEntryMapping.AddFieldMappingsAt("Level", keywordFieldMapping)
	logEntryMapping.AddFieldMappingsAt("Text", textFieldMapping)
	logEntryMapping.AddFieldMappingsAt("Timestamp", datetimeFieldMapping)

	// Allow dynamic mapping for Structured fields
	logEntryMapping.Dynamic = true

	indexMapping := bleve.NewIndexMapping()
	indexMapping.DefaultMapping = logEntryMapping

	return indexMapping
}

func (s *Store) Append(stream, text string, timestamp time.Time) {
	s.AppendWithService("", stream, text, timestamp)
}

// AppendWithService appends a log entry with an explicit service name override.
// If serviceName is non-empty, it overrides any service extracted from the log pattern.
func (s *Store) AppendWithService(serviceName, stream, text string, timestamp time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Enforce max entries limit by removing oldest entry
	if s.maxEntries > 0 && len(s.entries) >= s.maxEntries {
		oldest := s.entries[0]
		_ = s.index.Delete(fmt.Sprintf("%d", oldest.ID))
		s.entries = s.entries[1:]
	}

	entry := LogEntry{
		ID:        s.nextID,
		Stream:    stream,
		Text:      text,
		Timestamp: timestamp,
	}

	// Try pattern-based extraction first
	patternMatches := s.extractFromPattern(text)

	entry.Structured = parseStructured(text)
	entry.Level = s.extractLevel(text, entry.Structured, patternMatches)
	entry.Message = s.extractMessage(text, entry.Structured, patternMatches)

	// Use explicit service name if provided, otherwise extract from log
	if serviceName != "" {
		entry.Service = serviceName
	} else {
		entry.Service = s.extractService(text, entry.Structured, patternMatches)
	}

	s.entries = append(s.entries, entry)
	s.nextID++

	_ = s.index.Index(fmt.Sprintf("%d", entry.ID), entry)

	// Notify subscribers (non-blocking)
	for ch := range s.subscribers {
		select {
		case ch <- entry:
		default:
			// Channel full, skip
		}
	}
}

// extractFromPattern extracts named groups from the log pattern
func (s *Store) extractFromPattern(text string) map[string]string {
	result := make(map[string]string)
	if s.logPattern == nil {
		return result
	}

	matches := s.logPattern.FindStringSubmatch(text)
	if matches == nil {
		return result
	}

	for name, idx := range s.groupNames {
		if idx < len(matches) {
			result[name] = matches[idx]
		}
	}
	return result
}

func (s *Store) extractService(text string, structured map[string]any, patternMatches map[string]string) string {
	// Try pattern match first
	if svc, ok := patternMatches["service"]; ok && svc != "" {
		return svc
	}

	// Try structured JSON
	if structured != nil {
		for _, key := range []string{"service", "svc", "component", "app"} {
			if v, ok := structured[key]; ok {
				if str, ok := v.(string); ok {
					return str
				}
			}
		}
	}

	return ""
}

var levelPatterns = []struct {
	level   LogLevel
	pattern *regexp.Regexp
}{
	{LevelFatal, regexp.MustCompile(`(?i)\b(fatal|panic)\b`)},
	{LevelError, regexp.MustCompile(`(?i)\b(error|err)\b`)},
	{LevelWarn, regexp.MustCompile(`(?i)\b(warn|warning)\b`)},
	{LevelInfo, regexp.MustCompile(`(?i)\b(info)\b`)},
	{LevelDebug, regexp.MustCompile(`(?i)\b(debug)\b`)},
	{LevelTrace, regexp.MustCompile(`(?i)\b(trace)\b`)},
}

func (s *Store) extractLevel(text string, structured map[string]any, patternMatches map[string]string) LogLevel {
	// Try pattern match first
	if lvl, ok := patternMatches["level"]; ok && lvl != "" {
		return normalizeLevel(lvl)
	}

	// Try structured JSON
	if structured != nil {
		for _, key := range []string{"level", "lvl", "severity"} {
			if v, ok := structured[key]; ok {
				if str, ok := v.(string); ok {
					return normalizeLevel(str)
				}
			}
		}
	}

	// Fall back to pattern matching in text
	for _, p := range levelPatterns {
		if p.pattern.MatchString(text) {
			return p.level
		}
	}
	return LevelUnknown
}

// extractMessage extracts the core message from a log line
func (s *Store) extractMessage(text string, structured map[string]any, patternMatches map[string]string) string {
	// Try pattern match first
	if msg, ok := patternMatches["message"]; ok && msg != "" {
		return msg
	}

	// For JSON logs, extract the message field
	if structured != nil {
		for _, key := range []string{"msg", "message", "text", "error"} {
			if v, ok := structured[key]; ok {
				if str, ok := v.(string); ok && str != "" {
					return str
				}
			}
		}
		// If no message field, return original (might be structured with no msg)
		return text
	}

	// For text logs, strip known prefixes
	msg := text

	// Strip pattern match entirely if we have one (even without message group)
	if s.logPattern != nil {
		msg = s.logPattern.ReplaceAllString(msg, "")
	}

	// Strip common log level prefixes
	msg = levelPrefixRegex.ReplaceAllString(msg, "")

	// Strip leading/trailing whitespace and common separators
	msg = strings.TrimSpace(msg)
	msg = strings.TrimPrefix(msg, ":")
	msg = strings.TrimPrefix(msg, "-")
	msg = strings.TrimSpace(msg)

	if msg == "" {
		return text // Fall back to original if we stripped everything
	}
	return msg
}

// Matches common log level prefixes like "INFO:", "ERROR -", "[DEBUG]", etc.
var levelPrefixRegex = regexp.MustCompile(`(?i)^\s*\[?(trace|debug|info|warn|warning|error|err|fatal|panic)\]?[\s:\-]*`)

func normalizeLevel(s string) LogLevel {
	switch strings.ToLower(s) {
	case "trace", "trc", "t":
		return LevelTrace
	case "debug", "dbg", "d":
		return LevelDebug
	case "info", "inf", "i":
		return LevelInfo
	case "warn", "warning", "wrn", "w":
		return LevelWarn
	case "error", "err", "e":
		return LevelError
	case "fatal", "panic", "critical", "crit", "f":
		return LevelFatal
	default:
		return LevelUnknown
	}
}

func parseStructured(text string) map[string]any {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "{") {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		return nil
	}
	return obj
}

type FilterOptions struct {
	Stream  string
	Service string
	Level   LogLevel
	Since   time.Time
	Until   time.Time
}

func (s *Store) Tail(n int, opts FilterOptions) []LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filtered := s.filter(opts)

	if n <= 0 || n > len(filtered) {
		n = len(filtered)
	}

	start := len(filtered) - n
	result := make([]LogEntry, n)
	copy(result, filtered[start:])
	return result
}

func (s *Store) filter(opts FilterOptions) []LogEntry {
	var filtered []LogEntry
	for _, e := range s.entries {
		if !opts.Since.IsZero() && e.Timestamp.Before(opts.Since) {
			continue
		}
		if !opts.Until.IsZero() && e.Timestamp.After(opts.Until) {
			continue
		}
		if opts.Stream != "" && e.Stream != opts.Stream {
			continue
		}
		if opts.Service != "" && e.Service != opts.Service {
			continue
		}
		if opts.Level != "" && e.Level != opts.Level {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}

func (s *Store) Search(query string, limit int, opts FilterOptions) ([]LogEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	q := bleve.NewQueryStringQuery(query)
	req := bleve.NewSearchRequest(q)
	req.Size = 1000 // Get more to filter

	result, err := s.index.Search(req)
	if err != nil {
		return nil, err
	}

	idMap := make(map[string]LogEntry)
	for _, e := range s.entries {
		idMap[fmt.Sprintf("%d", e.ID)] = e
	}

	entries := make([]LogEntry, 0)
	for _, hit := range result.Hits {
		entry, ok := idMap[hit.ID]
		if !ok {
			continue
		}
		if opts.Stream != "" && entry.Stream != opts.Stream {
			continue
		}
		if opts.Service != "" && entry.Service != opts.Service {
			continue
		}
		if opts.Level != "" && entry.Level != opts.Level {
			continue
		}
		entries = append(entries, entry)
		if limit > 0 && len(entries) >= limit {
			break
		}
	}

	return entries, nil
}

func (s *Store) Services() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := make(map[string]bool)
	var services []string
	for _, e := range s.entries {
		if e.Service != "" && !seen[e.Service] {
			seen[e.Service] = true
			services = append(services, e.Service)
		}
	}
	return services
}

func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Close old index and create new one
	_ = s.index.Close()
	s.index, _ = bleve.NewMemOnly(buildIndexMapping())
	s.entries = make([]LogEntry, 0)
	// Keep nextID incrementing for uniqueness
}

// Subscribe registers a channel to receive new log entries.
// Returns an unsubscribe function that must be called to clean up.
func (s *Store) Subscribe(ch chan LogEntry) func() {
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()

	return func() {
		s.mu.Lock()
		delete(s.subscribers, ch)
		s.mu.Unlock()
	}
}

func (s *Store) Close() error {
	return s.index.Close()
}

// Stats holds statistics about the log entries in the store
type Stats struct {
	Total         int
	ByLevel       map[LogLevel]int
	ByService     map[string]int
	ByStream      map[string]int
	EarliestTime  time.Time
	LatestTime    time.Time
}

// Stats returns statistics about the log entries in the store
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := Stats{
		Total:     len(s.entries),
		ByLevel:   make(map[LogLevel]int),
		ByService: make(map[string]int),
		ByStream:  make(map[string]int),
	}

	for _, e := range s.entries {
		// Count by level
		stats.ByLevel[e.Level]++

		// Count by service (only if service is set)
		if e.Service != "" {
			stats.ByService[e.Service]++
		}

		// Count by stream
		stats.ByStream[e.Stream]++

		// Track earliest/latest timestamps
		if stats.EarliestTime.IsZero() || e.Timestamp.Before(stats.EarliestTime) {
			stats.EarliestTime = e.Timestamp
		}
		if stats.LatestTime.IsZero() || e.Timestamp.After(stats.LatestTime) {
			stats.LatestTime = e.Timestamp
		}
	}

	return stats
}

// GetByID returns the log entry with the given ID, or nil if not found.
func (s *Store) GetByID(id uint64) *LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for i := range s.entries {
		if s.entries[i].ID == id {
			entry := s.entries[i]
			return &entry
		}
	}
	return nil
}

// GetContext returns entries surrounding a given entry ID.
// Returns entries with IDs in range [id-before, id+after].
// Handles edge cases where IDs may not exist or are near start/end of log.
func (s *Store) GetContext(id uint64, before, after int) []LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.entries) == 0 {
		return nil
	}

	// Find the index of the entry with the given ID using binary search
	// Since entries are appended in order, IDs are monotonically increasing
	idx := -1
	for i, e := range s.entries {
		if e.ID == id {
			idx = i
			break
		}
	}

	if idx == -1 {
		return nil
	}

	// Calculate start and end indices
	startIdx := idx - before
	if startIdx < 0 {
		startIdx = 0
	}

	endIdx := idx + after
	if endIdx >= len(s.entries) {
		endIdx = len(s.entries) - 1
	}

	// Copy entries in the range
	result := make([]LogEntry, endIdx-startIdx+1)
	copy(result, s.entries[startIdx:endIdx+1])
	return result
}

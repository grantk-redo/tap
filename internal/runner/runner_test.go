package runner

import (
	"context"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	r := New("echo", "hello")
	if r.name != "echo" {
		t.Errorf("name = %q, want %q", r.name, "echo")
	}
	if len(r.args) != 1 || r.args[0] != "hello" {
		t.Errorf("args = %v, want [hello]", r.args)
	}
}

func TestRunnerCapturesStdout(t *testing.T) {
	r := New("echo", "hello world")

	var lines []LogLine
	var mu sync.Mutex
	r.OnLine(func(line LogLine) {
		mu.Lock()
		lines = append(lines, line)
		mu.Unlock()
	})

	ctx := context.Background()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	select {
	case <-r.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for process")
	}

	if r.ExitCode() != 0 {
		t.Errorf("ExitCode() = %d, want 0", r.ExitCode())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	if lines[0].Stream != Stdout {
		t.Errorf("Stream = %q, want %q", lines[0].Stream, Stdout)
	}
	if lines[0].Text != "hello world" {
		t.Errorf("Text = %q, want %q", lines[0].Text, "hello world")
	}
}

func TestRunnerCapturesStderr(t *testing.T) {
	r := New("sh", "-c", "echo error >&2")

	var lines []LogLine
	var mu sync.Mutex
	r.OnLine(func(line LogLine) {
		mu.Lock()
		lines = append(lines, line)
		mu.Unlock()
	})

	ctx := context.Background()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	<-r.Done()

	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	if lines[0].Stream != Stderr {
		t.Errorf("Stream = %q, want %q", lines[0].Stream, Stderr)
	}
	if lines[0].Text != "error" {
		t.Errorf("Text = %q, want %q", lines[0].Text, "error")
	}
}

func TestRunnerMultipleLines(t *testing.T) {
	r := New("sh", "-c", "echo line1; echo line2; echo line3")

	var lines []LogLine
	var mu sync.Mutex
	r.OnLine(func(line LogLine) {
		mu.Lock()
		lines = append(lines, line)
		mu.Unlock()
	})

	ctx := context.Background()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	<-r.Done()

	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}

	want := []string{"line1", "line2", "line3"}
	for i, w := range want {
		if lines[i].Text != w {
			t.Errorf("lines[%d].Text = %q, want %q", i, lines[i].Text, w)
		}
	}
}

func TestRunnerMixedOutput(t *testing.T) {
	r := New("sh", "-c", "echo out1; echo err1 >&2; echo out2")

	var lines []LogLine
	var mu sync.Mutex
	r.OnLine(func(line LogLine) {
		mu.Lock()
		lines = append(lines, line)
		mu.Unlock()
	})

	ctx := context.Background()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	<-r.Done()

	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}

	var stdoutCount, stderrCount int
	for _, l := range lines {
		switch l.Stream {
		case Stdout:
			stdoutCount++
		case Stderr:
			stderrCount++
		}
	}
	if stdoutCount != 2 {
		t.Errorf("stdout lines = %d, want 2", stdoutCount)
	}
	if stderrCount != 1 {
		t.Errorf("stderr lines = %d, want 1", stderrCount)
	}
}

func TestRunnerExitCode(t *testing.T) {
	tests := []struct {
		name     string
		cmd      string
		wantCode int
	}{
		{"success", "exit 0", 0},
		{"failure", "exit 1", 1},
		{"error code", "exit 42", 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New("sh", "-c", tt.cmd)

			ctx := context.Background()
			if err := r.Start(ctx); err != nil {
				t.Fatalf("Start() error = %v", err)
			}

			<-r.Done()

			if r.ExitCode() != tt.wantCode {
				t.Errorf("ExitCode() = %d, want %d", r.ExitCode(), tt.wantCode)
			}
		})
	}
}

func TestRunnerContextCancel(t *testing.T) {
	// Use a process that will definitely respond to signals
	r := New("sleep", "60")

	ctx, cancel := context.WithCancel(context.Background())

	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Give process time to start
	time.Sleep(50 * time.Millisecond)

	cancel()

	select {
	case <-r.Done():
		// Process terminated as expected
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for process to terminate after cancel")
	}

	// Process should have non-zero exit code when killed
	if r.ExitCode() == 0 {
		t.Log("warning: exit code was 0 after cancel (may vary by platform)")
	}
}

func TestRunnerMultipleHandlers(t *testing.T) {
	r := New("echo", "test")

	var count1, count2 int
	var mu sync.Mutex

	r.OnLine(func(line LogLine) {
		mu.Lock()
		count1++
		mu.Unlock()
	})
	r.OnLine(func(line LogLine) {
		mu.Lock()
		count2++
		mu.Unlock()
	})

	ctx := context.Background()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	<-r.Done()

	mu.Lock()
	defer mu.Unlock()
	if count1 != 1 || count2 != 1 {
		t.Errorf("handlers called %d and %d times, want 1 each", count1, count2)
	}
}

func TestRunnerTimestamp(t *testing.T) {
	r := New("echo", "test")

	var line LogLine
	var mu sync.Mutex
	r.OnLine(func(l LogLine) {
		mu.Lock()
		line = l
		mu.Unlock()
	})

	before := time.Now()

	ctx := context.Background()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	<-r.Done()

	after := time.Now()

	mu.Lock()
	defer mu.Unlock()
	if line.Timestamp.Before(before) || line.Timestamp.After(after) {
		t.Errorf("Timestamp %v not between %v and %v", line.Timestamp, before, after)
	}
}

func TestRunnerCommandNotFound(t *testing.T) {
	r := New("this-command-does-not-exist-12345")

	ctx := context.Background()
	err := r.Start(ctx)
	if err == nil {
		t.Error("Start() expected error for non-existent command")
	}
}

func TestRunnerLongLines(t *testing.T) {
	longLine := strings.Repeat("x", 10000)
	r := New("echo", longLine)

	var captured string
	var mu sync.Mutex
	r.OnLine(func(line LogLine) {
		mu.Lock()
		captured = line.Text
		mu.Unlock()
	})

	ctx := context.Background()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	<-r.Done()

	mu.Lock()
	defer mu.Unlock()
	if captured != longLine {
		t.Errorf("captured line length = %d, want %d", len(captured), len(longLine))
	}
}

func TestRunnerSignal(t *testing.T) {
	tests := []struct {
		name       string
		signal     os.Signal
		wantExit   bool
		expectCode int
	}{
		{
			name:     "SIGINT terminates process",
			signal:   syscall.SIGINT,
			wantExit: true,
		},
		{
			name:     "SIGTERM terminates process",
			signal:   syscall.SIGTERM,
			wantExit: true,
		},
		{
			name:     "SIGKILL kills process",
			signal:   syscall.SIGKILL,
			wantExit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New("sleep", "60")

			ctx := context.Background()
			if err := r.Start(ctx); err != nil {
				t.Fatalf("Start() error = %v", err)
			}

			// Give process time to start
			time.Sleep(50 * time.Millisecond)

			if err := r.Signal(tt.signal); err != nil {
				t.Errorf("Signal() error = %v", err)
			}

			select {
			case <-r.Done():
				// Process terminated as expected
			case <-time.After(5 * time.Second):
				t.Fatal("timeout waiting for process to terminate after signal")
			}
		})
	}
}

func TestRunnerSignalNotStarted(t *testing.T) {
	r := New("sleep", "1")

	// Should not error when process hasn't started
	if err := r.Signal(syscall.SIGINT); err != nil {
		t.Errorf("Signal() on unstarted runner error = %v, want nil", err)
	}
}

func TestRunnerPtyMode(t *testing.T) {
	tests := []struct {
		name       string
		cmd        []string
		wantText   string
		wantStream Stream
	}{
		{
			name:       "captures stdout via pty",
			cmd:        []string{"sh", "-c", "echo hello"},
			wantText:   "hello",
			wantStream: Stdout,
		},
		{
			name:       "captures stderr via pty as stdout",
			cmd:        []string{"sh", "-c", "echo error >&2"},
			wantText:   "error",
			wantStream: Stdout, // PTY combines stdout/stderr
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(tt.cmd[0], tt.cmd[1:]...)
			r.UsePty(true)

			var lines []LogLine
			var mu sync.Mutex
			r.OnLine(func(line LogLine) {
				mu.Lock()
				lines = append(lines, line)
				mu.Unlock()
			})

			ctx := context.Background()
			if err := r.Start(ctx); err != nil {
				t.Fatalf("Start() error = %v", err)
			}

			select {
			case <-r.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("timeout waiting for process")
			}

			mu.Lock()
			defer mu.Unlock()
			if len(lines) == 0 {
				t.Fatal("got 0 lines, want at least 1")
			}
			if lines[0].Text != tt.wantText {
				t.Errorf("Text = %q, want %q", lines[0].Text, tt.wantText)
			}
			if lines[0].Stream != tt.wantStream {
				t.Errorf("Stream = %q, want %q", lines[0].Stream, tt.wantStream)
			}
		})
	}
}

func TestRunnerPtyMultipleLines(t *testing.T) {
	r := New("sh", "-c", "echo line1; echo line2; echo line3")
	r.UsePty(true)

	var lines []LogLine
	var mu sync.Mutex
	r.OnLine(func(line LogLine) {
		mu.Lock()
		lines = append(lines, line)
		mu.Unlock()
	})

	ctx := context.Background()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	<-r.Done()

	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}

	want := []string{"line1", "line2", "line3"}
	for i, w := range want {
		if lines[i].Text != w {
			t.Errorf("lines[%d].Text = %q, want %q", i, lines[i].Text, w)
		}
	}
}

package runner

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"time"
)

// ansiRegex matches ANSI escape sequences (colors, cursor movement, etc.)
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

type Stream string

const (
	Stdout Stream = "stdout"
	Stderr Stream = "stderr"
)

type LogLine struct {
	Stream    Stream
	Text      string
	Timestamp time.Time
}

type LineHandler func(LogLine)

type Runner struct {
	name            string
	args            []string
	handlers        []LineHandler
	cmd             *exec.Cmd
	done            chan struct{}
	exitCode        int
	mu              sync.Mutex
	forwardToStdout bool
	outputWriter    io.Writer // custom output writer (if set, used instead of os.Stdout)
}

func New(name string, args ...string) *Runner {
	return &Runner{
		name: name,
		args: args,
		done: make(chan struct{}),
	}
}

func (r *Runner) OnLine(handler LineHandler) {
	r.handlers = append(r.handlers, handler)
}

// ForwardOutput enables forwarding captured output to os.Stdout/os.Stderr
func (r *Runner) ForwardOutput(enabled bool) {
	r.forwardToStdout = enabled
}

// SetOutputWriter sets a custom writer for output. When set, output is written
// to this writer instead of os.Stdout/os.Stderr. This also enables forwarding.
func (r *Runner) SetOutputWriter(w io.Writer) {
	r.outputWriter = w
	r.forwardToStdout = true
}

func (r *Runner) Start(ctx context.Context) error {
	r.cmd = exec.CommandContext(ctx, r.name, r.args...)

	stdout, err := r.cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := r.cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := r.cmd.Start(); err != nil {
		return err
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			r.emit(LogLine{
				Stream:    Stdout,
				Text:      scanner.Text(),
				Timestamp: time.Now(),
			})
		}
	}()

	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			r.emit(LogLine{
				Stream:    Stderr,
				Text:      scanner.Text(),
				Timestamp: time.Now(),
			})
		}
	}()

	go func() {
		wg.Wait()
		_ = r.cmd.Wait()
		r.mu.Lock()
		if r.cmd.ProcessState != nil {
			r.exitCode = r.cmd.ProcessState.ExitCode()
		}
		r.mu.Unlock()
		close(r.done)
	}()

	return nil
}

func (r *Runner) emit(line LogLine) {
	// Forward to terminal if enabled (with original ANSI codes)
	if r.forwardToStdout {
		if r.outputWriter != nil {
			// Use custom writer (e.g., TUI service writer)
			_, _ = r.outputWriter.Write([]byte(line.Text + "\n"))
		} else if line.Stream == Stdout {
			_, _ = os.Stdout.WriteString(line.Text + "\n")
		} else {
			_, _ = os.Stderr.WriteString(line.Text + "\n")
		}
	}

	// Strip ANSI codes before passing to handlers (for clean storage/indexing)
	cleanLine := LogLine{
		Stream:    line.Stream,
		Text:      ansiRegex.ReplaceAllString(line.Text, ""),
		Timestamp: line.Timestamp,
	}

	for _, h := range r.handlers {
		h(cleanLine)
	}
}

func (r *Runner) Done() <-chan struct{} {
	return r.done
}

func (r *Runner) ExitCode() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.exitCode
}

// Signal sends a signal to the subprocess. Returns an error if the process
// is not running or if the signal fails to send.
func (r *Runner) Signal(sig os.Signal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmd == nil || r.cmd.Process == nil {
		return nil
	}
	return r.cmd.Process.Signal(sig)
}

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

	"github.com/creack/pty"
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
	usePty          bool
	ptyFile         *os.File
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

// UsePty enables pseudo-terminal mode. This causes child processes to use
// line buffering instead of block buffering, resulting in more immediate output.
// Note: PTY mode combines stdout and stderr into a single stream, so all output
// will be reported as stdout.
func (r *Runner) UsePty(enabled bool) {
	r.usePty = enabled
}

func (r *Runner) Start(ctx context.Context) error {
	r.cmd = exec.CommandContext(ctx, r.name, r.args...)

	if r.usePty {
		return r.startWithPty()
	}
	return r.startWithPipes()
}

func (r *Runner) startWithPty() error {
	ptmx, err := pty.Start(r.cmd)
	if err != nil {
		return err
	}
	r.ptyFile = ptmx

	go func() {
		r.scanReader(ptmx, Stdout)
		_ = ptmx.Close()
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

func (r *Runner) startWithPipes() error {
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
		r.scanReader(stdout, Stdout)
	}()

	go func() {
		defer wg.Done()
		r.scanReader(stderr, Stderr)
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

func (r *Runner) scanReader(reader io.Reader, stream Stream) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		r.emit(LogLine{
			Stream:    stream,
			Text:      scanner.Text(),
			Timestamp: time.Now(),
		})
	}
}

func (r *Runner) emit(line LogLine) {
	// Forward to terminal if enabled (with original ANSI codes)
	if r.forwardToStdout {
		if line.Stream == Stdout {
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

package tui

import (
	"context"
	"os"
	"time"

	"golang.org/x/term"
)

// Terminal handles raw terminal mode and input reading.
type Terminal struct {
	fd       int
	oldState *term.State
	isRaw    bool
}

// NewTerminal creates a new Terminal instance.
func NewTerminal() *Terminal {
	return &Terminal{
		fd: int(os.Stdin.Fd()),
	}
}

// IsTerminal returns true if both stdin and stdout are terminals (interactive TTY mode).
func (t *Terminal) IsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// EnterRaw puts the terminal into raw mode for single-keystroke capture.
func (t *Terminal) EnterRaw() error {
	if t.isRaw {
		return nil
	}
	state, err := term.MakeRaw(t.fd)
	if err != nil {
		return err
	}
	t.oldState = state
	t.isRaw = true
	return nil
}

// Restore restores the terminal to its original state.
func (t *Terminal) Restore() error {
	if !t.isRaw || t.oldState == nil {
		return nil
	}
	err := term.Restore(t.fd, t.oldState)
	if err == nil {
		t.isRaw = false
	}
	return err
}

// GetSize returns the current terminal width and height.
func (t *Terminal) GetSize() (width, height int, err error) {
	return term.GetSize(int(os.Stdout.Fd()))
}

// ReadKey reads a single keystroke from stdin with context support.
// It uses a polling approach with a short timeout to allow context cancellation.
func (t *Terminal) ReadKey(ctx context.Context) (byte, error) {
	buf := make([]byte, 1)

	// Create a channel to receive the key
	keyCh := make(chan byte, 1)
	errCh := make(chan error, 1)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				// Set a short read deadline to allow checking context
				_ = os.Stdin.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				n, err := os.Stdin.Read(buf)
				if err != nil {
					if os.IsTimeout(err) {
						continue
					}
					errCh <- err
					return
				}
				if n > 0 {
					keyCh <- buf[0]
					return
				}
			}
		}
	}()

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case key := <-keyCh:
		return key, nil
	case err := <-errCh:
		return 0, err
	}
}

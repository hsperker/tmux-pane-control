package tmuxctl

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/hsperker/tmux-pane-control/internal/domain"
)

// Opts configures how the Adapter invokes tmux.
//
// SocketPath maps to tmux -S; SocketName maps to tmux -L. Both empty
// means rely on tmux's own default resolution (including $TMUX).
// Passing both is a command-level error (spec §11.4) and is the
// caller's responsibility to reject before constructing an Adapter.
type Opts struct {
	SocketPath string
	SocketName string
}

// Adapter is the real tmux Port implementation. It shells out to tmux
// for each call rather than holding a control-mode connection; later
// slices replace this with the control-mode transport.
type Adapter struct {
	opts Opts
}

// NewAdapter returns an Adapter wired to the tmux server identified by
// opts.
func NewAdapter(opts Opts) *Adapter { return &Adapter{opts: opts} }

// cmd builds a tmux argv with the socket-selection flags prefixed.
func (a *Adapter) cmd(args ...string) *exec.Cmd {
	full := make([]string, 0, len(args)+4)
	if a.opts.SocketPath != "" {
		full = append(full, "-S", a.opts.SocketPath)
	}
	if a.opts.SocketName != "" {
		full = append(full, "-L", a.opts.SocketName)
	}
	full = append(full, args...)
	return exec.Command("tmux", full...)
}

// ListPanes runs `tmux list-panes -a -F '#{pane_id}'` and parses the
// output, which is one pane id per line.
func (a *Adapter) ListPanes() ([]domain.PaneID, error) {
	var stdout, stderr bytes.Buffer
	c := a.cmd("list-panes", "-a", "-F", "#{pane_id}")
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	out := strings.TrimRight(stdout.String(), "\n")
	if out == "" {
		return []domain.PaneID{}, nil
	}
	lines := strings.Split(out, "\n")
	panes := make([]domain.PaneID, 0, len(lines))
	for _, l := range lines {
		panes = append(panes, domain.PaneID(l))
	}
	return panes, nil
}

// isPaneMissingStderr recognizes tmux's stderr signals for a missing
// pane. Exact strings vary by tmux version; we match substrings.
func isPaneMissingStderr(s string) bool {
	return strings.Contains(s, "can't find pane") ||
		strings.Contains(s, "no such pane") ||
		strings.Contains(s, "pane not found")
}

// SendText sends literal text via `tmux send-keys -l`. When enter is
// true a second send-keys invocation appends the Enter key — exactly
// as the spec §9.4 requires (not a literal \n).
func (a *Adapter) SendText(id domain.PaneID, text string, enter bool) error {
	var stderr bytes.Buffer
	c := a.cmd("send-keys", "-l", "-t", string(id), "--", text)
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		if isPaneMissingStderr(stderr.String()) {
			return ErrPaneNotFound
		}
		return fmt.Errorf("tmux send-keys -l: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if enter {
		stderr.Reset()
		c2 := a.cmd("send-keys", "-t", string(id), "--", "Enter")
		c2.Stderr = &stderr
		if err := c2.Run(); err != nil {
			if isPaneMissingStderr(stderr.String()) {
				return ErrPaneNotFound
			}
			return fmt.Errorf("tmux send-keys Enter: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
	}
	return nil
}

// SendKeys sends the listed key tokens via `tmux send-keys`. Tokens
// are treated as data; the `--` guard stops tmux from parsing them as
// flags. Invalid tokens fail with ErrInvalidKey.
func (a *Adapter) SendKeys(id domain.PaneID, keys []string) error {
	if len(keys) == 0 {
		return fmt.Errorf("send-keys: no keys")
	}
	args := []string{"send-keys", "-t", string(id), "--"}
	args = append(args, keys...)
	var stderr bytes.Buffer
	c := a.cmd(args...)
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		msg := stderr.String()
		if isPaneMissingStderr(msg) {
			return ErrPaneNotFound
		}
		if strings.Contains(msg, "unknown key") ||
			strings.Contains(msg, "invalid key") ||
			strings.Contains(msg, "bad key") {
			return fmt.Errorf("%w: %s", ErrInvalidKey, strings.TrimSpace(msg))
		}
		return fmt.Errorf("tmux send-keys: %w: %s", err, strings.TrimSpace(msg))
	}
	return nil
}

// CapturePane runs `tmux capture-pane -p -t <id>` which prints the
// current visible screen to stdout as plain text (no ANSI). Scrollback
// is not included in v1 slice 5; §9.2 scrollback_text is handled in a
// later slice.
func (a *Adapter) CapturePane(id domain.PaneID) (string, error) {
	var stdout, stderr bytes.Buffer
	c := a.cmd("capture-pane", "-p", "-t", string(id))
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		if strings.Contains(stderr.String(), "can't find pane") ||
			strings.Contains(stderr.String(), "no such pane") {
			return "", ErrPaneNotFound
		}
		return "", fmt.Errorf("tmux capture-pane: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// Ensure Adapter satisfies Port at compile time.
var _ Port = (*Adapter)(nil)

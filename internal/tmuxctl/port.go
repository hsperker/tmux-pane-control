// Package tmuxctl defines the Port interface (hexagonal) for talking to
// tmux. The real adapter lives alongside this interface; tests use fakes.
// Callers outside this package depend only on Port — not on tmux protocol
// details (spec §14).
package tmuxctl

import (
	"context"
	"errors"

	"github.com/hsperker/tmux-pane-control/internal/domain"
)

// PaneOutput carries one event from the subscription. Exactly one of
// the three event shapes is set per message:
//
//   - Data != nil: fresh bytes appended to pane ID.
//   - Closed == true: pane ID has been destroyed (spec §11.9).
//   - ServerLost == true: the tmux server itself has gone away. Per
//     spec §11.9 this is a controller-level event, not a pane-level
//     one; ID is ignored. The adapter emits this at most once per
//     subscription lifetime, after detecting persistent failure to
//     reach the server.
//
// Ordering per pane is preserved by the adapter.
type PaneOutput struct {
	ID         domain.PaneID
	Data       []byte
	Closed     bool
	ServerLost bool
}

// ErrPaneNotFound is returned by Port operations when tmux reports that
// the target pane does not exist. Handlers map it onto the canonical
// PANE_NOT_FOUND command-level error (spec §7.5).
var ErrPaneNotFound = errors.New("pane not found")

// Port is the minimal contract the rest of the system uses to interact
// with tmux. It is intentionally small; later slices extend it.
type Port interface {
	// ListPanes returns the ids of all panes currently known to the
	// tmux server, in tmux's own iteration order.
	ListPanes() ([]domain.PaneID, error)

	// CapturePane returns the current visible screen of the pane as
	// raw text (pre-normalization). Returns ErrPaneNotFound if tmux
	// reports the pane is missing.
	CapturePane(id domain.PaneID) (string, error)

	// CaptureScrollback returns up to lines of pane scrollback (the
	// portion above the visible screen), as raw text. lines <= 0
	// returns an empty string without invoking tmux.
	CaptureScrollback(id domain.PaneID, lines int) (string, error)

	// Subscribe returns a channel that receives PaneOutput events
	// for every byte appended to any pane on the tmux server. The
	// channel is closed when ctx is cancelled or the subscription
	// fails.
	Subscribe(ctx context.Context) (<-chan PaneOutput, error)

	// SendText sends literal text to the pane. If enter is true, an
	// Enter key press is appended after the text — not a literal \n
	// (spec §9.4). Returns only after tmux has acknowledged the send.
	SendText(id domain.PaneID, text string, enter bool) error

	// SendKeys sends named key tokens to the pane. Each key follows
	// tmux's send-keys vocabulary. Returns only after tmux has
	// acknowledged the send (spec §9.5).
	SendKeys(id domain.PaneID, keys []string) error
}

// ErrInvalidKey is returned by SendKeys when tmux rejects a key token.
var ErrInvalidKey = errors.New("invalid key token")

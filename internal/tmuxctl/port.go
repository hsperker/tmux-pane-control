// Package tmuxctl defines the Port interface (hexagonal) for talking to
// tmux. The real adapter lives alongside this interface; tests use fakes.
// Callers outside this package depend only on Port — not on tmux protocol
// details (spec §14).
package tmuxctl

import (
	"errors"

	"github.com/hsperker/tmux-pane-control/internal/domain"
)

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
}

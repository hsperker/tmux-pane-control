// Package tmuxctl defines the Port interface (hexagonal) for talking to
// tmux. The real adapter lives alongside this interface; tests use fakes.
// Callers outside this package depend only on Port — not on tmux protocol
// details (spec §14).
package tmuxctl

import "github.com/hsperker/tmux-pane-control/internal/domain"

// Port is the minimal contract the rest of the system uses to interact
// with tmux. It is intentionally small; later slices extend it.
type Port interface {
	// ListPanes returns the ids of all panes currently known to the
	// tmux server, in tmux's own iteration order.
	ListPanes() ([]domain.PaneID, error)
}

package controller

import (
	"errors"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/tmuxctl"
)

// Snapshot handles the `tpctl snapshot` command (spec §9.2). It
// captures the visible screen and returns a checkpoint token.
//
// Errors:
//   - *domain.ErrorResponse for command-level failures
//     (e.g. PANE_NOT_FOUND).
//   - other error for runtime failures (CLI surfaces to stderr per §7.3).
func Snapshot(port tmuxctl.Port, issuer TokenIssuer, id domain.PaneID) (*domain.SnapshotResponse, error) {
	text, err := port.CapturePane(id)
	if err != nil {
		if errors.Is(err, tmuxctl.ErrPaneNotFound) {
			return nil, &domain.ErrorResponse{
				PaneID:  string(id),
				Code:    domain.ErrPaneNotFound,
				Message: "pane not found",
			}
		}
		return nil, err
	}
	return &domain.SnapshotResponse{
		PaneID: id,
		Next:   issuer.Next(id),
		Text:   text,
	}, nil
}

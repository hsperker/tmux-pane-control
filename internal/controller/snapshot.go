package controller

import (
	"errors"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/store"
	"github.com/hsperker/tmux-pane-control/internal/textnorm"
	"github.com/hsperker/tmux-pane-control/internal/tmuxctl"
)

// Snapshot handles the `tpctl snapshot` command (spec §9.2). It
// captures the visible screen and returns a checkpoint token pointing
// at the current stream head.
//
// Errors:
//   - *domain.ErrorResponse for command-level failures
//     (e.g. PANE_NOT_FOUND).
//   - other error for runtime failures (CLI surfaces to stderr per §7.3).
func Snapshot(port tmuxctl.Port, st *store.Store, id domain.PaneID) (*domain.SnapshotResponse, error) {
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
		Next:   st.NewToken(id),
		Text:   textnorm.Normalize(text),
	}, nil
}

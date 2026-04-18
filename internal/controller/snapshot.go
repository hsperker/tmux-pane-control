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
// at the current stream head. If historyLines is non-nil, scrollback
// is captured and returned in ScrollbackText — even for n=0, which
// yields an explicit empty string per spec §9.2 field-presence rules.
//
// Errors:
//   - *domain.ErrorResponse for command-level failures
//     (e.g. PANE_NOT_FOUND).
//   - other error for runtime failures (CLI surfaces to stderr per §7.3).
func Snapshot(port tmuxctl.Port, st *store.Store, id domain.PaneID, historyLines *int) (*domain.SnapshotResponse, error) {
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
	resp := &domain.SnapshotResponse{
		PaneID: id,
		Next:   st.NewToken(id),
		Text:   textnorm.Normalize(text),
	}
	if historyLines != nil {
		sb, err := port.CaptureScrollback(id, *historyLines)
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
		resp.ScrollbackText = domain.NewString(textnorm.Normalize(sb))
	}
	return resp, nil
}

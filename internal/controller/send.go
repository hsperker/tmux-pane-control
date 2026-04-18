package controller

import (
	"errors"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/tmuxctl"
)

// SendText handles `tpctl text` (spec §9.4). It returns an
// *domain.ErrorResponse for command-level failures and nil on success
// (the CLI prints nothing).
func SendText(port tmuxctl.Port, id domain.PaneID, text string, enter bool) error {
	if err := port.SendText(id, text, enter); err != nil {
		if errors.Is(err, tmuxctl.ErrPaneNotFound) {
			return &domain.ErrorResponse{
				PaneID:  string(id),
				Code:    domain.ErrPaneNotFound,
				Message: "pane not found",
			}
		}
		return err
	}
	return nil
}

// SendKeys handles `tpctl key` (spec §9.5). It returns an
// *domain.ErrorResponse for command-level failures and nil on success.
func SendKeys(port tmuxctl.Port, id domain.PaneID, keys []string) error {
	if err := port.SendKeys(id, keys); err != nil {
		if errors.Is(err, tmuxctl.ErrPaneNotFound) {
			return &domain.ErrorResponse{
				PaneID:  string(id),
				Code:    domain.ErrPaneNotFound,
				Message: "pane not found",
			}
		}
		if errors.Is(err, tmuxctl.ErrInvalidKey) {
			return &domain.ErrorResponse{
				PaneID:  string(id),
				Code:    domain.ErrInvalidKey,
				Message: err.Error(),
			}
		}
		return err
	}
	return nil
}

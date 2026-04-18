package controller

import (
	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/tmuxctl"
)

// List handles the `tpctl list` command (spec §9.1). It returns pane
// ids only; metadata is out of scope for v1.
func List(port tmuxctl.Port) (*domain.ListResponse, error) {
	panes, err := port.ListPanes()
	if err != nil {
		return nil, err
	}
	// Spec §8.1: panes field is always present, even when empty.
	if panes == nil {
		panes = []domain.PaneID{}
	}
	return &domain.ListResponse{Panes: panes}, nil
}

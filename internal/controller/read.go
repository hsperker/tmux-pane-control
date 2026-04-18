package controller

import (
	"errors"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/store"
	"github.com/hsperker/tmux-pane-control/internal/textnorm"
)

// Read handles the `tpctl read` command (spec §9.3).
//
// The caller must already have validated --after presence at the CLI
// layer (MISSING_AFTER is reported there).
//
// Returns *domain.ErrorResponse for command-level failures and
// otherwise a ReadResponse with normalized text and a fresh token.
func Read(st *store.Store, id domain.PaneID, after domain.Token) (*domain.ReadResponse, error) {
	bytes, next, err := st.Read(id, after)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrPaneUnknown):
			return nil, &domain.ErrorResponse{
				PaneID:  string(id),
				Code:    domain.ErrPaneNotFound,
				Message: "pane not found",
			}
		case errors.Is(err, store.ErrTokenMalformed),
			errors.Is(err, store.ErrTokenWrongInstance),
			errors.Is(err, store.ErrTokenWrongPane),
			errors.Is(err, store.ErrTokenEvicted):
			return nil, &domain.ErrorResponse{
				PaneID:  string(id),
				Code:    domain.ErrInvalidAfter,
				Message: "checkpoint token is not valid for this pane or is no longer retained",
			}
		default:
			return nil, err
		}
	}
	return &domain.ReadResponse{
		PaneID: id,
		Next:   next,
		Text:   textnorm.Normalize(string(bytes)),
	}, nil
}

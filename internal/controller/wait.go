package controller

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/store"
	"github.com/hsperker/tmux-pane-control/internal/textnorm"
	"github.com/hsperker/tmux-pane-control/internal/waiter"
)

// WaitMode selects which match evaluator Wait runs (spec §9.6).
type WaitMode int

const (
	WaitModeSentinel WaitMode = iota + 1
	WaitModeRegex
	WaitModeQuiescence
)

// WaitRequest bundles the parameters accepted by `tpctl wait`. Not
// all fields are meaningful for every mode: SentinelToken is for
// sentinel mode only (analogous fields for regex/quiescence land in
// later slices).
type WaitRequest struct {
	PaneID        domain.PaneID
	After         domain.Token
	Timeout       time.Duration
	Mode          WaitMode
	SentinelToken string
}

// Wait implements `tpctl wait` (spec §9.6).
//
// Returns *domain.ErrorResponse for command-level failures
// (MISSING_AFTER, INVALID_AFTER, PANE_NOT_FOUND, TIMEOUT) and a
// successful WaitResponse otherwise. Runtime failures propagate as
// plain errors for CLI stderr.
func Wait(ctx context.Context, st *store.Store, req WaitRequest) (*domain.WaitResponse, error) {
	if req.After == "" {
		return nil, &domain.ErrorResponse{
			PaneID:  string(req.PaneID),
			Code:    domain.ErrMissingAfter,
			Message: "wait requires --after; use snapshot to bootstrap",
		}
	}
	if req.Timeout <= 0 {
		return nil, &domain.ErrorResponse{
			PaneID:  string(req.PaneID),
			Code:    domain.ErrInvalidArgs,
			Message: "wait requires a positive --timeout-ms",
		}
	}
	switch req.Mode {
	case WaitModeSentinel:
		if err := validateSentinelToken(req.SentinelToken); err != nil {
			return nil, &domain.ErrorResponse{
				PaneID:  string(req.PaneID),
				Code:    domain.ErrInvalidArgs,
				Message: err.Error(),
			}
		}
	default:
		return nil, &domain.ErrorResponse{
			PaneID:  string(req.PaneID),
			Code:    domain.ErrInvalidArgs,
			Message: "unsupported --for mode",
		}
	}

	// Register the watcher BEFORE the first read so no append between
	// the read and the signal subscription is missed (spec §9.6's
	// "cannot miss fast output that occurs after the checkpoint").
	sig, cancel := st.Watch(req.PaneID)
	defer cancel()

	timer := time.NewTimer(req.Timeout)
	defer timer.Stop()

	for {
		bytes, next, err := st.Read(req.PaneID, req.After)
		if err != nil {
			return nil, mapWaitStoreError(req.PaneID, err)
		}

		stripped := textnorm.StripANSI(string(bytes))
		switch req.Mode {
		case WaitModeSentinel:
			if m, ok := waiter.MatchSentinel([]byte(stripped), req.SentinelToken); ok {
				return &domain.WaitResponse{
					PaneID:   req.PaneID,
					Next:     next,
					Result:   domain.WaitSentinel,
					Matched:  domain.NewString(m.Matched),
					ExitCode: domain.NewInt(m.ExitCode),
				}, nil
			}
		}

		select {
		case <-sig:
			// New output or pane forgotten; re-evaluate on next loop.
		case <-timer.C:
			return nil, &domain.ErrorResponse{
				PaneID:  string(req.PaneID),
				Code:    domain.ErrTimeout,
				Message: "wait timed out",
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// validateSentinelToken enforces spec §9.6 constraints on --token.
func validateSentinelToken(t string) error {
	if t == "" {
		return errors.New("sentinel --token is required")
	}
	if strings.ContainsAny(t, ":\n") {
		return errors.New("sentinel --token must not contain ':' or newline")
	}
	return nil
}

// mapWaitStoreError translates store errors to command-level responses.
func mapWaitStoreError(id domain.PaneID, err error) *domain.ErrorResponse {
	switch {
	case errors.Is(err, store.ErrPaneUnknown):
		return &domain.ErrorResponse{
			PaneID:  string(id),
			Code:    domain.ErrPaneNotFound,
			Message: "pane not found",
		}
	case errors.Is(err, store.ErrTokenMalformed),
		errors.Is(err, store.ErrTokenWrongInstance),
		errors.Is(err, store.ErrTokenWrongPane),
		errors.Is(err, store.ErrTokenEvicted):
		return &domain.ErrorResponse{
			PaneID:  string(id),
			Code:    domain.ErrInvalidAfter,
			Message: "checkpoint token is not valid for this pane or is no longer retained",
		}
	}
	// Runtime error — return a generic command-level shape; CLI may
	// also forward the raw error to stderr.
	return &domain.ErrorResponse{
		PaneID:  string(id),
		Code:    domain.ErrInvalidArgs,
		Message: err.Error(),
	}
}

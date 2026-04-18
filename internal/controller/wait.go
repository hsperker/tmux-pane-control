package controller

import (
	"context"
	"errors"
	"regexp"
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
// all fields are meaningful for every mode.
type WaitRequest struct {
	PaneID        domain.PaneID
	After         domain.Token
	Timeout       time.Duration
	Mode          WaitMode
	SentinelToken string         // sentinel: required
	QuietWindow   time.Duration  // quiescence: required
	Regex         *regexp.Regexp // regex: required
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
	case WaitModeQuiescence:
		if req.QuietWindow <= 0 {
			return nil, &domain.ErrorResponse{
				PaneID:  string(req.PaneID),
				Code:    domain.ErrInvalidArgs,
				Message: "quiescence mode requires a positive --ms",
			}
		}
	case WaitModeRegex:
		if req.Regex == nil {
			return nil, &domain.ErrorResponse{
				PaneID:  string(req.PaneID),
				Code:    domain.ErrInvalidArgs,
				Message: "regex mode requires a compiled --pattern",
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

	timeout := time.NewTimer(req.Timeout)
	defer timeout.Stop()

	// Decode the checkpoint mint time for quiescence's formula.
	checkpointAt, _ := st.TokenTime(req.After)

	for {
		// Validate token + pane each iteration so a Forget (pane
		// destroyed) surfaces as PANE_NOT_FOUND even if already
		// buffered bytes still match. The current cost is low.
		bytes, next, err := st.Read(req.PaneID, req.After)
		if err != nil {
			return nil, mapWaitStoreError(req.PaneID, err)
		}

		switch req.Mode {
		case WaitModeSentinel:
			stripped := textnorm.StripANSI(string(bytes))
			if m, ok := waiter.MatchSentinel([]byte(stripped), req.SentinelToken); ok {
				return &domain.WaitResponse{
					PaneID:   req.PaneID,
					Next:     next,
					Result:   domain.WaitSentinel,
					Matched:  domain.NewString(m.Matched),
					ExitCode: domain.NewInt(m.ExitCode),
				}, nil
			}
		case WaitModeRegex:
			stripped := textnorm.StripANSI(string(bytes))
			if m, ok := waiter.MatchRegex([]byte(stripped), req.Regex); ok {
				return &domain.WaitResponse{
					PaneID:  req.PaneID,
					Next:    next,
					Result:  domain.WaitRegex,
					Matched: domain.NewString(m.Matched),
				}, nil
			}
		case WaitModeQuiescence:
			// Spec §9.6: quiescence at time `now` iff
			// now - max(T_checkpoint, T_last_if_present) >= ms.
			ref := checkpointAt
			if la, ok := st.LastAppend(req.PaneID); ok && la.After(ref) {
				ref = la
			}
			elapsed := time.Since(ref)
			if elapsed >= req.QuietWindow {
				return &domain.WaitResponse{
					PaneID: req.PaneID,
					Next:   st.NewToken(req.PaneID),
					Result: domain.WaitQuiescence,
				}, nil
			}
			// Wake up again at most when the quiet window would be
			// satisfied; re-check earlier if new output arrives.
			remaining := req.QuietWindow - elapsed
			quietTimer := time.NewTimer(remaining)
			select {
			case <-sig:
				quietTimer.Stop()
				continue
			case <-quietTimer.C:
				// Re-check the formula; new activity may have
				// landed between the timer expiring and now.
				continue
			case <-timeout.C:
				quietTimer.Stop()
				return nil, &domain.ErrorResponse{
					PaneID:  string(req.PaneID),
					Code:    domain.ErrTimeout,
					Message: "wait timed out",
				}
			case <-ctx.Done():
				quietTimer.Stop()
				return nil, ctx.Err()
			}
		}

		select {
		case <-sig:
		case <-timeout.C:
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

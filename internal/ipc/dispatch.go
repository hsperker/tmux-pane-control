package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/controller"
	"github.com/hsperker/tmux-pane-control/internal/domain"
)

// Dispatch runs one request against the controller and returns the
// corresponding Response. It never touches stdio; the server and test
// harness wrap this with transport.
func Dispatch(ctx context.Context, c *controller.Controller, req *Request) *Response {
	switch req.Op {
	case OpList:
		return marshalBody(c.List())
	case OpSnapshot:
		var hl *int
		if req.HasHistory {
			hl = &req.HistoryLines
		}
		return marshalBody(c.Snapshot(req.Pane, hl))
	case OpRead:
		if req.After == "" {
			return cmdErr(&domain.ErrorResponse{
				PaneID:  string(req.Pane),
				Code:    domain.ErrMissingAfter,
				Message: "read requires --after; use snapshot to bootstrap",
			})
		}
		return marshalBody(c.Read(req.Pane, req.After))
	case OpText:
		if err := c.SendText(req.Pane, req.Text, req.Enter); err != nil {
			return classifyErr(err)
		}
		return &Response{OK: true}
	case OpKey:
		if len(req.Keys) == 0 {
			return cmdErr(&domain.ErrorResponse{
				PaneID:  string(req.Pane),
				Code:    domain.ErrInvalidArgs,
				Message: "key requires at least one token",
			})
		}
		if err := c.SendKeys(req.Pane, req.Keys); err != nil {
			return classifyErr(err)
		}
		return &Response{OK: true}
	case OpWait:
		return dispatchWait(ctx, c, req)
	default:
		return runtimeErr(fmt.Errorf("unknown op %q", req.Op))
	}
}

func dispatchWait(ctx context.Context, c *controller.Controller, req *Request) *Response {
	if req.TimeoutMs <= 0 {
		return cmdErr(&domain.ErrorResponse{
			PaneID:  string(req.Pane),
			Code:    domain.ErrInvalidArgs,
			Message: "wait requires a positive timeout_ms",
		})
	}
	var mode controller.WaitMode
	var re *regexp.Regexp
	switch req.Mode {
	case "sentinel":
		mode = controller.WaitModeSentinel
	case "quiescence":
		mode = controller.WaitModeQuiescence
	case "regex":
		mode = controller.WaitModeRegex
		if req.Pattern == "" {
			return cmdErr(&domain.ErrorResponse{
				PaneID:  string(req.Pane),
				Code:    domain.ErrInvalidArgs,
				Message: "regex mode requires pattern",
			})
		}
		r, err := regexp.Compile(req.Pattern)
		if err != nil {
			return cmdErr(&domain.ErrorResponse{
				PaneID:  string(req.Pane),
				Code:    domain.ErrInvalidRegex,
				Message: "invalid pattern: " + err.Error(),
			})
		}
		re = r
	default:
		return cmdErr(&domain.ErrorResponse{
			PaneID:  string(req.Pane),
			Code:    domain.ErrInvalidArgs,
			Message: "unknown wait mode: " + req.Mode,
		})
	}
	// Give the controller the caller's timeout plus a grace period;
	// the outer ctx ultimately caps us. The Wait handler itself honors
	// req.Timeout.
	return marshalBody(c.Wait(ctx, controller.WaitRequest{
		PaneID:        req.Pane,
		After:         req.After,
		Timeout:       time.Duration(req.TimeoutMs) * time.Millisecond,
		Mode:          mode,
		SentinelToken: req.SentinelToken,
		QuietWindow:   time.Duration(req.QuietMs) * time.Millisecond,
		Regex:         re,
	}))
}

// marshalBody turns a handler (payload, err) return into a Response.
// Accepts any pointer-typed payload; err may be a command-level
// *domain.ErrorResponse or a runtime error.
func marshalBody(payload any, err error) *Response {
	if err != nil {
		return classifyErr(err)
	}
	raw, merr := json.Marshal(payload)
	if merr != nil {
		return runtimeErr(merr)
	}
	return &Response{OK: true, Body: raw}
}

func classifyErr(err error) *Response {
	var ce *domain.ErrorResponse
	if errors.As(err, &ce) {
		return cmdErr(ce)
	}
	return runtimeErr(err)
}

func cmdErr(e *domain.ErrorResponse) *Response {
	return &Response{OK: false, Error: e}
}

func runtimeErr(err error) *Response {
	return &Response{OK: false, Runtime: err.Error()}
}


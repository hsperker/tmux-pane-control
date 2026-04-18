package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/tmuxctl"
)

// TestController_ForgetOnClosedEvent verifies §11.9: when the adapter
// signals a pane closure, the controller drops its buffer.
func TestController_ForgetOnClosedEvent(t *testing.T) {
	fake := &tmuxctl.Fake{Panes: []domain.PaneID{"%1"}}
	c := New(fake)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(c.Stop)

	// Touch the pane so the store has an entry.
	fake.Emit("%1", []byte("hello"))

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if c.Store().HasPane("%1") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !c.Store().HasPane("%1") {
		t.Fatal("pane never landed in store")
	}

	// Signal closure.
	fake.Emit("%1", []byte{}) // keep channel hot
	fake.Emit("%1", nil)      // still no-op for Closed=false
	// Emit a closed event directly via the fake's normal channel:
	// reuse Emit by sending a zero-Data PaneOutput through a helper.
	fake.EmitClosed("%1")

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !c.Store().HasPane("%1") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("pane still in store after close event")
}

// TestWait_PaneClosedWhilePending verifies §11.9: a pending wait
// whose pane is destroyed returns PANE_CLOSED (distinct from both
// TIMEOUT and PANE_NOT_FOUND).
func TestWait_PaneClosedWhilePending(t *testing.T) {
	fake := &tmuxctl.Fake{Panes: []domain.PaneID{"%1"}}
	c := New(fake)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(c.Stop)

	snap, err := c.Snapshot("%1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := c.Wait(context.Background(), WaitRequest{
			PaneID: "%1", After: snap.Next, Timeout: 5 * time.Second,
			Mode: WaitModeSentinel, SentinelToken: "neverarrives",
		})
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	fake.EmitClosed("%1")

	select {
	case err := <-done:
		var cerr *domain.ErrorResponse
		if !errors.As(err, &cerr) {
			t.Fatalf("want ErrorResponse, got %v", err)
		}
		if cerr.Code != domain.ErrPaneClosed {
			t.Fatalf("code = %q", cerr.Code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not return after close")
	}
}

// TestWait_PaneMissingAtDispatch verifies §11.9: if the pane is
// already gone at dispatch, a *new* wait returns PANE_NOT_FOUND, not
// PANE_CLOSED.
func TestWait_PaneMissingAtDispatch(t *testing.T) {
	fake := &tmuxctl.Fake{Panes: []domain.PaneID{"%1"}}
	c := New(fake)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(c.Stop)

	// Mint a token, then destroy the pane before Wait is called.
	snap, err := c.Snapshot("%1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	fake.EmitClosed("%1")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && c.Store().HasPane("%1") {
		time.Sleep(5 * time.Millisecond)
	}

	_, err = c.Wait(context.Background(), WaitRequest{
		PaneID: "%1", After: snap.Next, Timeout: 500 * time.Millisecond,
		Mode: WaitModeSentinel, SentinelToken: "x",
	})
	var cerr *domain.ErrorResponse
	if !errors.As(err, &cerr) {
		t.Fatalf("want ErrorResponse, got %v", err)
	}
	if cerr.Code != domain.ErrPaneNotFound {
		t.Fatalf("code = %q want PANE_NOT_FOUND", cerr.Code)
	}
}

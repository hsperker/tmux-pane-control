package controller

import (
	"errors"
	"testing"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/tmuxctl"
)

func TestSnapshot_VisibleScreen(t *testing.T) {
	fake := &tmuxctl.Fake{
		Panes:   []domain.PaneID{"%42"},
		Screens: map[domain.PaneID]string{"%42": "hello\n$ "},
	}
	resp, err := Snapshot(fake, &CounterIssuer{}, "%42")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if resp.PaneID != "%42" {
		t.Fatalf("pane_id = %q", resp.PaneID)
	}
	if resp.Text != "hello\n$ " {
		t.Fatalf("text = %q", resp.Text)
	}
	if resp.Next == "" {
		t.Fatal("next must be set")
	}
	if resp.ScrollbackText != nil {
		t.Fatalf("scrollback_text must be absent, got %q", *resp.ScrollbackText)
	}
}

func TestSnapshot_PaneNotFound(t *testing.T) {
	fake := &tmuxctl.Fake{} // no panes
	_, err := Snapshot(fake, &CounterIssuer{}, "%99")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var cerr *domain.ErrorResponse
	if !errors.As(err, &cerr) {
		t.Fatalf("want *ErrorResponse, got %T", err)
	}
	if cerr.Code != domain.ErrPaneNotFound {
		t.Fatalf("code = %q", cerr.Code)
	}
	if cerr.PaneID != "%99" {
		t.Fatalf("pane_id = %q", cerr.PaneID)
	}
}

func TestSnapshot_RuntimeError(t *testing.T) {
	boom := errors.New("boom")
	fake := &tmuxctl.Fake{
		CapturePaneFn: func(domain.PaneID) (string, error) { return "", boom },
	}
	_, err := Snapshot(fake, &CounterIssuer{}, "%42")
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	var cerr *domain.ErrorResponse
	if errors.As(err, &cerr) {
		t.Fatal("runtime error should not be a command-level ErrorResponse")
	}
}

func TestCounterIssuer_Monotonic(t *testing.T) {
	var iss CounterIssuer
	seen := map[domain.Token]bool{}
	for i := 0; i < 100; i++ {
		tok := iss.Next("%42")
		if seen[tok] {
			t.Fatalf("duplicate token %q", tok)
		}
		seen[tok] = true
	}
}

package controller

import (
	"errors"
	"testing"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/store"
	"github.com/hsperker/tmux-pane-control/internal/tmuxctl"
)

func TestSnapshot_VisibleScreen(t *testing.T) {
	fake := &tmuxctl.Fake{
		Panes:   []domain.PaneID{"%42"},
		Screens: map[domain.PaneID]string{"%42": "hello\n$ "},
	}
	s := store.New(64)
	resp, err := Snapshot(fake, s, "%42")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if resp.PaneID != "%42" {
		t.Fatalf("pane_id = %q", resp.PaneID)
	}
	// Normalization trims the trailing space after "$" (spec §8.3).
	if resp.Text != "hello\n$" {
		t.Fatalf("text = %q", resp.Text)
	}
	if resp.Next == "" {
		t.Fatal("next must be set")
	}
	if resp.ScrollbackText != nil {
		t.Fatalf("scrollback_text must be absent, got %q", *resp.ScrollbackText)
	}
}

func TestSnapshot_TokenFeedsRead(t *testing.T) {
	// The token returned by snapshot must be a valid starting point
	// for a subsequent read; read sees only output appended after
	// snapshot (spec §9.2, §9.3).
	fake := &tmuxctl.Fake{
		Panes:   []domain.PaneID{"%42"},
		Screens: map[domain.PaneID]string{"%42": "prompt"},
	}
	s := store.New(64)
	resp, err := Snapshot(fake, s, "%42")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// Simulate pane output produced after snapshot.
	s.Append("%42", []byte("hello"))
	readResp, err := Read(s, "%42", resp.Next)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if readResp.Text != "hello" {
		t.Fatalf("read.text = %q", readResp.Text)
	}
	if readResp.Next == resp.Next {
		t.Fatal("read.next did not advance")
	}
}

func TestSnapshot_NormalizesText(t *testing.T) {
	fake := &tmuxctl.Fake{
		Panes: []domain.PaneID{"%42"},
		Screens: map[domain.PaneID]string{
			"%42": "\x1b[31mhello\x1b[0m  \r\n$  \n\n\n",
		},
	}
	resp, err := Snapshot(fake, store.New(64), "%42")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if resp.Text != "hello\n$" {
		t.Fatalf("text = %q, want %q", resp.Text, "hello\n$")
	}
}

func TestSnapshot_PaneNotFound(t *testing.T) {
	_, err := Snapshot(&tmuxctl.Fake{}, store.New(64), "%99")
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
	_, err := Snapshot(fake, store.New(64), "%42")
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	var cerr *domain.ErrorResponse
	if errors.As(err, &cerr) {
		t.Fatal("runtime error should not be a command-level ErrorResponse")
	}
}

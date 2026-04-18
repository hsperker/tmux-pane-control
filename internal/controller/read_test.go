package controller

import (
	"errors"
	"testing"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/store"
)

func TestRead_Delta(t *testing.T) {
	s := store.New(64)
	tok := s.NewToken("%42")
	s.Append("%42", []byte("hello\nworld"))
	resp, err := Read(s, "%42", tok)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if resp.Text != "hello\nworld" {
		t.Fatalf("text = %q", resp.Text)
	}
	if resp.PaneID != "%42" {
		t.Fatalf("pane_id = %q", resp.PaneID)
	}
}

func TestRead_EmptyIsSuccess(t *testing.T) {
	// Spec §9.3: empty text is still a success.
	s := store.New(64)
	tok := s.NewToken("%42")
	resp, err := Read(s, "%42", tok)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if resp.Text != "" {
		t.Fatalf("text = %q", resp.Text)
	}
}

func TestRead_NormalizesOutput(t *testing.T) {
	s := store.New(64)
	tok := s.NewToken("%42")
	s.Append("%42", []byte("\x1b[32mok\x1b[0m \r\n"))
	resp, err := Read(s, "%42", tok)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if resp.Text != "ok" {
		t.Fatalf("text = %q, want %q", resp.Text, "ok")
	}
}

func TestRead_InvalidAfterOnEviction(t *testing.T) {
	s := store.New(4)
	tok := s.NewToken("%42")
	s.Append("%42", []byte("abcdefgh"))
	_, err := Read(s, "%42", tok)
	var cerr *domain.ErrorResponse
	if !errors.As(err, &cerr) {
		t.Fatalf("want ErrorResponse, got %v", err)
	}
	if cerr.Code != domain.ErrInvalidAfter {
		t.Fatalf("code = %q", cerr.Code)
	}
}

func TestRead_InvalidAfterOnWrongPane(t *testing.T) {
	s := store.New(64)
	tok := s.NewToken("%42")
	_, err := Read(s, "%43", tok)
	var cerr *domain.ErrorResponse
	if !errors.As(err, &cerr) {
		t.Fatalf("want ErrorResponse, got %v", err)
	}
	if cerr.Code != domain.ErrInvalidAfter {
		t.Fatalf("code = %q", cerr.Code)
	}
}

func TestRead_PaneNotFound(t *testing.T) {
	s := store.New(64)
	// Synthesize a token for a pane the store has never heard of.
	tok := s.NewToken("%42")
	s.Forget("%42")
	_, err := Read(s, "%42", tok)
	var cerr *domain.ErrorResponse
	if !errors.As(err, &cerr) {
		t.Fatalf("want ErrorResponse, got %v", err)
	}
	if cerr.Code != domain.ErrPaneNotFound {
		t.Fatalf("code = %q", cerr.Code)
	}
}

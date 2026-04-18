package controller

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/tmuxctl"
)

func TestSendText_RecordsPayload(t *testing.T) {
	fake := &tmuxctl.Fake{Panes: []domain.PaneID{"%42"}}
	err := SendText(fake, "%42", "kubectl get pods", true)
	if err != nil {
		t.Fatalf("SendText: %v", err)
	}
	sent := fake.SentText()
	if len(sent) != 1 {
		t.Fatalf("calls = %d", len(sent))
	}
	if sent[0].ID != "%42" || sent[0].Text != "kubectl get pods" || !sent[0].Enter {
		t.Fatalf("got %+v", sent[0])
	}
}

func TestSendText_EmptyWithEnter(t *testing.T) {
	// Spec §9.4: empty payload with --enter is "press Enter after
	// sending nothing".
	fake := &tmuxctl.Fake{Panes: []domain.PaneID{"%42"}}
	if err := SendText(fake, "%42", "", true); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	sent := fake.SentText()
	if len(sent) != 1 || sent[0].Text != "" || !sent[0].Enter {
		t.Fatalf("got %+v", sent)
	}
}

func TestSendText_PaneNotFound(t *testing.T) {
	fake := &tmuxctl.Fake{
		SendTextFn: func(domain.PaneID, string, bool) error {
			return tmuxctl.ErrPaneNotFound
		},
	}
	err := SendText(fake, "%99", "hi", false)
	var ce *domain.ErrorResponse
	if !errors.As(err, &ce) {
		t.Fatalf("want ErrorResponse, got %v", err)
	}
	if ce.Code != domain.ErrPaneNotFound {
		t.Fatalf("code = %q", ce.Code)
	}
}

func TestSendKeys_RecordsKeys(t *testing.T) {
	fake := &tmuxctl.Fake{Panes: []domain.PaneID{"%42"}}
	err := SendKeys(fake, "%42", []string{"Escape", ":", "q", "Enter"})
	if err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	sent := fake.SentKeys()
	if len(sent) != 1 {
		t.Fatalf("calls = %d", len(sent))
	}
	want := []string{"Escape", ":", "q", "Enter"}
	if !reflect.DeepEqual(sent[0].Keys, want) {
		t.Fatalf("got %v, want %v", sent[0].Keys, want)
	}
}

func TestSendKeys_InvalidKey(t *testing.T) {
	fake := &tmuxctl.Fake{
		SendKeysFn: func(domain.PaneID, []string) error {
			return tmuxctl.ErrInvalidKey
		},
	}
	err := SendKeys(fake, "%42", []string{"BogusKey"})
	var ce *domain.ErrorResponse
	if !errors.As(err, &ce) {
		t.Fatalf("want ErrorResponse, got %v", err)
	}
	if ce.Code != domain.ErrInvalidKey {
		t.Fatalf("code = %q", ce.Code)
	}
}

func TestSendKeys_PaneNotFound(t *testing.T) {
	fake := &tmuxctl.Fake{
		SendKeysFn: func(domain.PaneID, []string) error {
			return tmuxctl.ErrPaneNotFound
		},
	}
	err := SendKeys(fake, "%99", []string{"Enter"})
	var ce *domain.ErrorResponse
	if !errors.As(err, &ce) {
		t.Fatalf("want ErrorResponse, got %v", err)
	}
	if ce.Code != domain.ErrPaneNotFound {
		t.Fatalf("code = %q", ce.Code)
	}
}

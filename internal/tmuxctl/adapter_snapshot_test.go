package tmuxctl

import (
	"errors"
	"strings"
	"testing"

	"github.com/hsperker/tmux-pane-control/internal/domain"
)

func TestAdapter_CapturePane_Blank(t *testing.T) {
	sock := newTmuxServer(t)
	a := NewAdapter(Opts{SocketPath: sock})
	panes, err := a.ListPanes()
	if err != nil {
		t.Fatalf("ListPanes: %v", err)
	}
	if len(panes) == 0 {
		t.Fatal("no panes")
	}
	text, err := a.CapturePane(panes[0])
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	// A fresh pane is not necessarily empty (shell may print a prompt).
	// We only assert the call succeeds and the length is within the
	// pane dimensions we requested (80x24 in newTmuxServer).
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > 24 {
		t.Fatalf("captured %d lines, want ≤24", len(lines))
	}
}

func TestAdapter_CapturePane_MissingPane(t *testing.T) {
	sock := newTmuxServer(t)
	a := NewAdapter(Opts{SocketPath: sock})
	_, err := a.CapturePane(domain.PaneID("%999"))
	if !errors.Is(err, ErrPaneNotFound) {
		t.Fatalf("want ErrPaneNotFound, got %v", err)
	}
}

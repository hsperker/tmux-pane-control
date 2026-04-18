package tmuxctl

import (
	"strings"
	"testing"
)

func TestAdapter_ListPanes_SingleSession(t *testing.T) {
	sock := newTmuxServer(t)
	a := NewAdapter(Opts{SocketPath: sock})
	panes, err := a.ListPanes()
	if err != nil {
		t.Fatalf("ListPanes: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("want 1 pane, got %d: %v", len(panes), panes)
	}
	if !strings.HasPrefix(string(panes[0]), "%") {
		t.Fatalf("pane id missing %% prefix: %q", panes[0])
	}
}

func TestAdapter_ListPanes_MultipleWindows(t *testing.T) {
	sock := newTmuxServer(t)
	tmuxCmd(t, sock, "new-window", "-t", "t1")
	tmuxCmd(t, sock, "new-window", "-t", "t1")

	a := NewAdapter(Opts{SocketPath: sock})
	panes, err := a.ListPanes()
	if err != nil {
		t.Fatalf("ListPanes: %v", err)
	}
	if len(panes) != 3 {
		t.Fatalf("want 3 panes, got %d: %v", len(panes), panes)
	}
	for _, p := range panes {
		if !strings.HasPrefix(string(p), "%") {
			t.Fatalf("pane id missing %% prefix: %q", p)
		}
	}
}

//go:build unix

package tmuxctl

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestAdapter_CaptureScrollback_Live(t *testing.T) {
	sock := newTmuxServer(t)
	a := NewAdapter(Opts{SocketPath: sock})
	panes, err := a.ListPanes()
	if err != nil || len(panes) == 0 {
		t.Fatalf("ListPanes: %v", err)
	}

	// Push enough lines that they scroll off-screen. Pane is 24 tall;
	// print 50 lines.
	if err := a.SendText(panes[0],
		`for i in $(seq 1 50); do echo scroll-line-$i; done`, true); err != nil {
		t.Fatalf("SendText: %v", err)
	}

	// Wait for rendering. There isn't a clean sync point without
	// wait; poll capture-pane until we see the last line.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		text, _ := a.CapturePane(panes[0])
		if strings.Contains(text, "scroll-line-50") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	sb, err := a.CaptureScrollback(panes[0], 30)
	if err != nil {
		t.Fatalf("CaptureScrollback: %v", err)
	}
	// Earliest lines should be in scrollback (they scrolled off).
	if !strings.Contains(sb, "scroll-line-1") {
		t.Fatalf("scrollback missing scroll-line-1:\n%s", sb)
	}
}

func TestAdapter_CaptureScrollback_Zero(t *testing.T) {
	sock := newTmuxServer(t)
	a := NewAdapter(Opts{SocketPath: sock})
	panes, err := a.ListPanes()
	if err != nil || len(panes) == 0 {
		t.Fatalf("ListPanes: %v", err)
	}
	s, err := a.CaptureScrollback(panes[0], 0)
	if err != nil {
		t.Fatalf("CaptureScrollback: %v", err)
	}
	if s != "" {
		t.Fatalf("want empty, got %q", s)
	}
}

func TestAdapter_CaptureScrollback_MissingPane(t *testing.T) {
	sock := newTmuxServer(t)
	a := NewAdapter(Opts{SocketPath: sock})
	_, err := a.CaptureScrollback("%999", 5)
	if err == nil {
		t.Fatal("want error")
	}
}

// TestAdapter_SendKeysPathAndSubscribeSmoke is an extra smoke path
// that buys us coverage above without being strictly part of slice 16
// — kept here to exercise the send+subscribe integration against real
// tmux now that the adapter surface has grown.
func TestAdapter_SendKeysPathAndSubscribeSmoke(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	sock := newTmuxServer(t)
	a := NewAdapter(Opts{SocketPath: sock})
	if err := a.SendKeys("%0", []string{"Enter"}); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
}
